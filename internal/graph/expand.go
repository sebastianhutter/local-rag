package graph

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Defaults for expansion. They are deliberately conservative: the cost of a
// traversal is bounded by the degree of the nodes it starts from, not by the
// number of nodes, and a corpus of this shape always has a few sources that
// enumerate hundreds of others.
const (
	DefaultHops         = 1
	DefaultHubCap       = 25
	DefaultLimit        = 20
	DefaultPerSeedLimit = 5
	MaxHops             = 2
)

// Neighbour is one source reached from a seed.
type Neighbour struct {
	SourceID   int64
	Collection string
	SourcePath string
	Title      string
	Snippet    string
	Rel        string
	Origin     string
	Degree     int
	Hops       int
	ViaID      int64 // the seed or intermediate node this was reached from
}

// ExpandOptions controls a traversal.
type ExpandOptions struct {
	// Rels and Origins restrict which edges are followed. Empty means all.
	Rels    []string
	Origins []string

	// Hops is the traversal depth, capped at MaxHops. Three hops over a
	// densely mentioned corpus returns most of it.
	Hops int

	// HubCap stops expansion *through* a node whose degree exceeds it. A hub
	// is a fine destination and a terrible corridor: an issue that 439
	// documents mention says nothing about which of them belong together, and
	// following it would return all 439. Nodes above the cap are still
	// returned when something else reaches them.
	HubCap int

	// Limit caps the returned neighbours after ranking.
	Limit int

	// PerSeedLimit caps how many neighbours a single seed may contribute,
	// 0 meaning DefaultPerSeedLimit. Without it one prolific node monopolises
	// the result: a wiki page with thirty children contributes thirty
	// siblings-by-proxy and buries what every other seed found.
	PerSeedLimit int

	// IncludeHubs returns neighbours whose own degree exceeds HubCap. Off by
	// default: a source with hundreds of edges is a table of contents, not an
	// answer to "what is this connected to". Ranking already puts them last,
	// but that is not enough when a seed has few neighbours and the hub makes
	// the cut anyway.
	IncludeHubs bool

	// KeepDuplicateTitles disables the collapsing of neighbours that share a
	// title. Off by default because near-identical sources are common and
	// ruinous here: automated notification mail about one ticket produces
	// dozens of near-copies, each with a degree of 1, so rarest-first ranking
	// hands them every slot. Collapsing keeps the first (the most specific by
	// the ranking above) and drops the rest.
	KeepDuplicateTitles bool
}

func (o ExpandOptions) withDefaults() ExpandOptions {
	if o.Hops <= 0 {
		o.Hops = DefaultHops
	}
	if o.Hops > MaxHops {
		o.Hops = MaxHops
	}
	if o.HubCap <= 0 {
		o.HubCap = DefaultHubCap
	}
	if o.Limit <= 0 {
		o.Limit = DefaultLimit
	}
	if o.PerSeedLimit <= 0 {
		o.PerSeedLimit = DefaultPerSeedLimit
	}
	return o
}

// Expand walks the stored graph out from the given seed sources and returns
// what they are connected to, nearest first.
//
// Ranking is by hops, then by ascending degree of the neighbour. Degree stands
// in for specificity: a source that two things point at is far more telling
// than one that four hundred things mention, and without that ordering a
// traversal surfaces the corpus's most generic documents first.
func Expand(conn *sql.DB, seeds []int64, opts ExpandOptions) ([]Neighbour, error) {
	opts = opts.withDefaults()
	if len(seeds) == 0 {
		return nil, nil
	}

	degrees, err := loadDegrees(conn)
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]bool, len(seeds))
	for _, id := range seeds {
		seen[id] = true
	}

	var found []Neighbour
	frontier := seeds
	for hop := 1; hop <= opts.Hops && len(frontier) > 0; hop++ {
		// Only expand through nodes that are not hubs. Seeds are subject to
		// the same rule: a seed that enumerates 570 tickets is a hub whatever
		// put it in the result set.
		var expandable []int64
		for _, id := range frontier {
			if degrees[id] <= opts.HubCap {
				expandable = append(expandable, id)
			}
		}
		if len(expandable) == 0 {
			break
		}

		edges, err := adjacentEdges(conn, expandable, opts)
		if err != nil {
			return nil, err
		}

		// Order candidates by the specificity of the destination before the
		// per-seed cap is applied, so a seed spends its allowance on its
		// rarest neighbours rather than its lowest row ids.
		sort.SliceStable(edges, func(i, j int) bool {
			di, dj := degrees[edges[i].dst], degrees[edges[j].dst]
			if di != dj {
				return di < dj
			}
			return edges[i].dst < edges[j].dst
		})

		var next []int64
		perSeed := make(map[int64]int, len(expandable))
		for _, e := range edges {
			if seen[e.dst] {
				continue
			}
			if !opts.IncludeHubs && degrees[e.dst] > opts.HubCap {
				continue
			}
			if perSeed[e.src] >= opts.PerSeedLimit {
				continue
			}
			perSeed[e.src]++
			seen[e.dst] = true
			found = append(found, Neighbour{
				SourceID: e.dst,
				Rel:      e.rel,
				Origin:   e.origin,
				Degree:   degrees[e.dst],
				Hops:     hop,
				ViaID:    e.src,
			})
			next = append(next, e.dst)
		}
		frontier = next
	}

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Hops != found[j].Hops {
			return found[i].Hops < found[j].Hops
		}
		if found[i].Degree != found[j].Degree {
			return found[i].Degree < found[j].Degree
		}
		return found[i].SourceID < found[j].SourceID
	})

	// Describe before limiting, because collapsing duplicates needs titles;
	// describing a few more rows than are returned is one indexed lookup each.
	described, err := describe(conn, found, opts.Limit)
	if err != nil {
		return nil, err
	}
	if !opts.KeepDuplicateTitles {
		described = collapseByTitle(described)
	}
	if len(described) > opts.Limit {
		described = described[:opts.Limit]
	}
	return described, nil
}

// loadDegrees reads the undirected degree of every node that has an edge. The
// whole table is aggregated in one pass rather than per-node: the graph is tens
// of thousands of rows, and a query per seed would dominate the traversal.
func loadDegrees(conn *sql.DB) (map[int64]int, error) {
	rows, err := conn.Query(`
		SELECT id, COUNT(*) FROM (
			SELECT src_source_id AS id FROM graph_edges
			UNION ALL
			SELECT dst_source_id FROM graph_edges
		) GROUP BY id`)
	if err != nil {
		return nil, fmt.Errorf("load degrees: %w", err)
	}
	defer rows.Close()

	degrees := make(map[int64]int)
	for rows.Next() {
		var id int64
		var degree int
		if err := rows.Scan(&id, &degree); err != nil {
			return nil, fmt.Errorf("scan degree: %w", err)
		}
		degrees[id] = degree
	}
	return degrees, rows.Err()
}

type adjacentEdge struct {
	src, dst int64
	rel      string
	origin   string
}

// adjacentEdges returns the edges touching the given nodes in either
// direction. Direction is not meaningful for "what is this connected to": a
// page's children are as relevant as its parent.
func adjacentEdges(conn *sql.DB, ids []int64, opts ExpandOptions) ([]adjacentEdge, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)*2+len(opts.Rels)+len(opts.Origins))
	for _, id := range ids {
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}

	// The OR must stay parenthesised: without it, a trailing "AND rel IN (...)"
	// binds to the dst branch alone and the filter silently applies to half
	// the edges.
	query := `
		SELECT src_source_id, dst_source_id, rel, origin FROM graph_edges
		WHERE (src_source_id IN (` + placeholders + `)
		    OR dst_source_id IN (` + placeholders + `))`

	if filter, filterArgs := inFilter("rel", opts.Rels); filter != "" {
		query += " AND " + filter
		args = append(args, filterArgs...)
	}
	if filter, filterArgs := inFilter("origin", opts.Origins); filter != "" {
		query += " AND " + filter
		args = append(args, filterArgs...)
	}

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query adjacent edges: %w", err)
	}
	defer rows.Close()

	from := make(map[int64]bool, len(ids))
	for _, id := range ids {
		from[id] = true
	}

	var out []adjacentEdge
	for rows.Next() {
		var e adjacentEdge
		if err := rows.Scan(&e.src, &e.dst, &e.rel, &e.origin); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		// Orient the edge away from the node we came from, so ViaID always
		// names the node that produced this neighbour.
		if !from[e.src] && from[e.dst] {
			e.src, e.dst = e.dst, e.src
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Deterministic order in, deterministic order out.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].dst != out[j].dst {
			return out[i].dst < out[j].dst
		}
		return out[i].rel < out[j].rel
	})
	return out, nil
}

func inFilter(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	args := make([]any, 0, len(values))
	for _, v := range values {
		args = append(args, v)
	}
	return column + " IN (" + placeholders + ")", args
}

// describe fills in what a caller needs to decide whether to read a neighbour:
// where it lives, what it is called, and one line of it. Never the full text --
// the point of a traversal is to spend a fraction of what another search costs,
// and a full chunk per neighbour spends more.
//
// It stops once enough rows are described to satisfy limit after duplicates
// have been collapsed, so a seed adjacent to thousands of edges does not turn
// into thousands of lookups.
func describe(conn *sql.DB, found []Neighbour, limit int) ([]Neighbour, error) {
	const overshoot = 4 // room for duplicates that will collapse away
	budget := limit * overshoot
	if budget > 0 && len(found) > budget {
		found = found[:budget]
	}
	for i := range found {
		var (
			collection, path string
			title, content   sql.NullString
		)
		err := conn.QueryRow(`
			SELECT c.name, s.source_path, d.title, d.content
			FROM sources s
			JOIN collections c ON c.id = s.collection_id
			LEFT JOIN documents d ON d.source_id = s.id AND d.chunk_index = 0
			WHERE s.id = ?`, found[i].SourceID,
		).Scan(&collection, &path, &title, &content)
		if err != nil {
			return nil, fmt.Errorf("describe source %d: %w", found[i].SourceID, err)
		}
		found[i].Collection = collection
		found[i].SourcePath = path
		found[i].Title = title.String
		found[i].Snippet = snippet(content.String)
	}
	return found, nil
}

// collapseByTitle keeps the first neighbour of each title. Sources with no
// title are compared by path instead, so untitled files are not all treated as
// one.
func collapseByTitle(found []Neighbour) []Neighbour {
	seen := make(map[string]bool, len(found))
	out := found[:0]
	for _, n := range found {
		key := strings.ToLower(strings.TrimSpace(n.Title))
		if key == "" {
			key = "path:" + n.SourcePath
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, n)
	}
	return out
}

// snippet reduces a chunk to a single readable line, cut on a word boundary.
const snippetLen = 180

func snippet(content string) string {
	s := strings.Join(strings.Fields(content), " ")
	runes := []rune(s)
	if len(runes) <= snippetLen {
		return s
	}
	cut := string(runes[:snippetLen])
	if i := strings.LastIndex(cut, " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return cut + "..."
}
