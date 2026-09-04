// Package graph derives relations between indexed sources and stores them in
// the graph_edges table, so a search result can be expanded into the documents
// it is connected to rather than only the documents that resemble it.
//
// Every relation here is parsed, not inferred: a Confluence parent id, a
// frontmatter property, a wikilink, a ticket key. Nothing calls a model, so a
// rebuild costs a scan and no embeddings.
package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Relation kinds. rel answers "what does this edge mean", which is what a
// caller filters on when it wants parents but not passing mentions.
const (
	RelLinksTo  = "links_to"
	RelChildOf  = "child_of"
	RelMentions = "mentions"
)

// Edge origins. origin answers "where did this edge come from", recorded per
// edge so one class can be rebuilt or discarded without disturbing the others.
// That separation is cheap now and necessary later: a parsed wikilink and an
// LLM-extracted relation have nothing in common in cost or error rate.
const (
	OriginConfluence  = "confluence"
	OriginJira        = "jira"
	OriginFrontmatter = "frontmatter"
	OriginWikilink    = "wikilink"
	OriginTicket      = "ticket-regex"
)

// AllOrigins is the set Rebuild derives when no subset is requested, ordered
// cheapest and most certain first.
var AllOrigins = []string{OriginConfluence, OriginJira, OriginFrontmatter, OriginWikilink, OriginTicket}

// OriginStats reports what one origin produced.
type OriginStats struct {
	Edges      int // distinct edges stored
	Unresolved int // references whose target is not an indexed source
	Ambiguous  int // references matching more than one source by name
	Skipped    int // references deliberately ignored, e.g. an excluded sender
}

// RebuildOptions controls a rebuild. The graph package takes plain values
// rather than the config struct so that what it needs stays visible here.
type RebuildOptions struct {
	// Origins limits the rebuild. Empty means AllOrigins.
	Origins []string

	// MentionExcludeSenders suppresses ticket mentions from mail whose sender
	// contains one of these, matched case-insensitively. Tracker notification
	// mail names a ticket without referring to it, and left in it wins the
	// traversal's rarest-first ranking outright.
	MentionExcludeSenders []string
}

// Stats reports the outcome of a rebuild.
type Stats struct {
	ByOrigin map[string]*OriginStats
	Elapsed  time.Duration
}

// Total returns the number of edges stored across every origin rebuilt.
func (s *Stats) Total() int {
	var n int
	for _, st := range s.ByOrigin {
		n += st.Edges
	}
	return n
}

type edge struct {
	src, dst int64
	rel      string
}

// Rebuild derives the requested edge origins and replaces the stored edges for
// each. Origins are replaced one at a time rather than the whole table being
// cleared, so rebuilding one class leaves the others in place.
func Rebuild(conn *sql.DB, opts RebuildOptions) (*Stats, error) {
	start := time.Now()
	origins := opts.Origins
	if len(origins) == 0 {
		origins = AllOrigins
	}

	idx, err := loadIndex(conn)
	if err != nil {
		return nil, err
	}
	slog.Info("graph: loaded resolvable sources",
		"markdown_sources", len(idx.sources), "confluence_pages", len(idx.byPageID), "jira_issues", len(idx.byKey))

	stats := &Stats{ByOrigin: make(map[string]*OriginStats, len(origins))}
	for _, origin := range origins {
		var edges []edge
		var st OriginStats

		switch origin {
		case OriginConfluence:
			edges, st = idx.confluenceEdges()
		case OriginJira:
			edges, st = idx.jiraEdges()
		case OriginFrontmatter:
			edges, st = idx.frontmatterEdges()
		case OriginWikilink:
			edges, st = idx.wikilinkEdges()
		case OriginTicket:
			edges, st, err = idx.ticketEdges(conn, opts.MentionExcludeSenders)
			if err != nil {
				return nil, fmt.Errorf("derive %s edges: %w", origin, err)
			}
		default:
			return nil, fmt.Errorf("unknown edge origin %q (known: %s)", origin, strings.Join(AllOrigins, ", "))
		}

		stored, err := replaceOrigin(conn, origin, edges)
		if err != nil {
			return nil, fmt.Errorf("store %s edges: %w", origin, err)
		}
		st.Edges = stored
		stats.ByOrigin[origin] = &st
		slog.Info("graph: origin rebuilt", "origin", origin,
			"edges", stored, "unresolved", st.Unresolved, "ambiguous", st.Ambiguous, "skipped", st.Skipped)
	}

	stats.Elapsed = time.Since(start)
	return stats, nil
}

// sourceMeta is one indexed file plus the metadata of its first chunk. Chunk 0
// is enough because frontmatter-derived keys are copied onto every chunk of a
// source, so reading one avoids scanning the whole documents table.
type sourceMeta struct {
	ID   int64
	Path string
	Meta map[string]any
}

type index struct {
	sources  []*sourceMeta
	byPath   map[string]int64   // lowercased vault-relative-ish path without extension
	byBase   map[string][]int64 // lowercased file name without extension
	byPageID map[string]int64
	byKey    map[string]int64 // Jira issue key -> the source holding that issue
	// fmTargets records, per source, the link targets that came from
	// frontmatter, so the wikilink pass can skip them: the parser merges
	// frontmatter links into the flat links list, and emitting both would
	// double-count the same relation.
	fmTargets map[int64]map[string]bool
}

// loadIndex reads the markdown sources that a relation can point at. Only
// markdown carries wikilinks, Confluence ids or issue keys, and restricting to
// it keeps the scan to thousands of rows instead of the hundreds of thousands
// that email and RSS add.
func loadIndex(conn *sql.DB) (*index, error) {
	rows, err := conn.Query(`
		SELECT s.id, s.source_path, d.metadata
		FROM sources s
		JOIN documents d ON d.source_id = s.id AND d.chunk_index = 0
		WHERE s.source_type = 'markdown'`)
	if err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}
	defer rows.Close()

	idx := &index{
		byPath:    make(map[string]int64),
		byBase:    make(map[string][]int64),
		byPageID:  make(map[string]int64),
		byKey:     make(map[string]int64),
		fmTargets: make(map[int64]map[string]bool),
	}
	for rows.Next() {
		var (
			id       int64
			path     string
			metaJSON sql.NullString
		)
		if err := rows.Scan(&id, &path, &metaJSON); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sm := &sourceMeta{ID: id, Path: path, Meta: map[string]any{}}
		if metaJSON.Valid {
			// Invalid JSON is not fatal: a source with unreadable metadata
			// simply contributes no metadata-derived edges.
			if err := json.Unmarshal([]byte(metaJSON.String), &sm.Meta); err != nil {
				slog.Debug("graph: unreadable metadata, ignoring", "source", path, "err", err)
				sm.Meta = map[string]any{}
			}
		}
		idx.sources = append(idx.sources, sm)
		idx.byBase[baseKey(path)] = append(idx.byBase[baseKey(path)], id)
		idx.byPath[pathKey(path)] = id
		if v := metaString(sm.Meta, "page_id"); v != "" {
			idx.byPageID[v] = id
		}
		if v := metaString(sm.Meta, "issue_key"); v != "" {
			idx.byKey[strings.ToUpper(v)] = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}

	// Ambiguity is resolved by Obsidian's own rule -- the shortest path wins --
	// with the source id as a tiebreak so two runs agree. Sorting once here
	// means resolveName can take the first candidate.
	pathByID := make(map[int64]string, len(idx.sources))
	for _, sm := range idx.sources {
		pathByID[sm.ID] = sm.Path
	}
	for _, ids := range idx.byBase {
		sort.Slice(ids, func(i, j int) bool {
			pi, pj := pathByID[ids[i]], pathByID[ids[j]]
			di, dj := strings.Count(pi, string(filepath.Separator)), strings.Count(pj, string(filepath.Separator))
			if di != dj {
				return di < dj
			}
			if len(pi) != len(pj) {
				return len(pi) < len(pj)
			}
			return ids[i] < ids[j]
		})
	}
	return idx, nil
}

// baseKey is the identity a wikilink resolves against: the file name without
// its extension, lowercased. Obsidian's own rules are richer (aliases, shortest
// unique path) and a resolver that implements them belongs here later; this is
// the subset that covers a link written as a plain note name.
func baseKey(path string) string {
	base := filepath.Base(path)
	return strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
}

// pathKey is the identity a `[[folder/Note]]` link resolves against: the path
// without its extension, lowercased, separators normalised.
func pathKey(path string) string {
	trimmed := strings.TrimSuffix(path, filepath.Ext(path))
	return strings.ToLower(filepath.ToSlash(trimmed))
}

// nonMarkdownTarget reports whether a link points at something that is not an
// indexed note -- an image, a PDF, an Excalidraw drawing. Such a link is not a
// resolution failure and counting it as one hides the real ones: 31% of the
// unresolved references in a real vault were attachments.
func nonMarkdownTarget(target string) bool {
	ext := strings.ToLower(filepath.Ext(target))
	switch ext {
	case "", ".md", ".markdown":
		return false
	}
	return true
}

func metaString(meta map[string]any, key string) string {
	switch v := meta[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprintf("%.0f", v)
	case json.Number:
		return v.String()
	}
	return ""
}

// resolution describes what happened to one link target.
type resolution int

const (
	resolvedExactly resolution = iota
	resolvedAmbiguously
	unresolvable // no such note
	notANote     // an attachment, or a heading inside the linking note
)

// resolveTarget maps a link target to a source. A target carrying a path is
// matched on the path first, so `[[Processes/Handover]]` reaches that file
// rather than whichever `Handover.md` sorts first.
func (idx *index) resolveTarget(target string) (int64, resolution) {
	target = strings.TrimSpace(target)

	// `[[#Heading]]` and `[[^block]]` point inside the linking note itself.
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "^") {
		return 0, notANote
	}
	// Strip a heading or block reference from the tail.
	if i := strings.IndexAny(target, "#^"); i > 0 {
		target = strings.TrimSpace(target[:i])
	}
	if target == "" {
		return 0, notANote
	}
	if nonMarkdownTarget(target) {
		return 0, notANote
	}

	if strings.ContainsAny(target, "/\\") {
		if id, ok := idx.byPath[pathKey(target)]; ok {
			return id, resolvedExactly
		}
		// Fall back to a suffix match, since a link is written relative to the
		// vault while a stored path is absolute.
		suffix := "/" + pathKey(target)
		for storedPath, id := range idx.byPath {
			if strings.HasSuffix(storedPath, suffix) {
				return id, resolvedExactly
			}
		}
	}

	ids := idx.byBase[baseKey(target)]
	switch len(ids) {
	case 0:
		return 0, unresolvable
	case 1:
		return ids[0], resolvedExactly
	default:
		return ids[0], resolvedAmbiguously
	}
}

// confluenceEdges links a page to its parent. The most reliable relation
// available: both ends are integer ids written by the sync, so nothing is
// resolved by name.
func (idx *index) confluenceEdges() ([]edge, OriginStats) {
	var st OriginStats
	out := newEdgeSet()
	for _, s := range idx.sources {
		parent := metaString(s.Meta, "parent_id")
		if parent == "" {
			continue
		}
		dst, ok := idx.byPageID[parent]
		if !ok {
			st.Unresolved++
			continue
		}
		out.add(s.ID, dst, RelChildOf)
	}
	return out.slice(), st
}

// jiraEdges links an issue to its parent -- a story to its epic, a sub-task to
// its story. Kept separate from confluenceEdges because origin says where an
// edge came from and the two syncs are independent: a Jira re-sync should be
// able to rebuild these without touching the wiki hierarchy.
//
// The relation is RelChildOf, the same as a wiki parent, because rel says what
// an edge *means* and a caller asking "what does this belong to" wants both.
func (idx *index) jiraEdges() ([]edge, OriginStats) {
	var st OriginStats
	out := newEdgeSet()
	for _, s := range idx.sources {
		parent := strings.ToUpper(metaString(s.Meta, "parent_key"))
		if parent == "" {
			continue
		}
		dst, ok := idx.byKey[parent]
		if !ok {
			// The parent exists in the tracker but is outside what was synced.
			st.Unresolved++
			continue
		}
		if dst != s.ID {
			out.add(s.ID, dst, RelChildOf)
		}
	}
	return out.slice(), st
}

// frontmatterEdges turns a relation written into a named property into a typed
// edge, so `parent:` and `related:` stay distinguishable instead of collapsing
// into one undifferentiated link. The property name becomes the relation.
func (idx *index) frontmatterEdges() ([]edge, OriginStats) {
	var st OriginStats
	out := newEdgeSet()
	for _, s := range idx.sources {
		for _, key := range sortedKeys(s.Meta) {
			if key == "links" {
				continue // the flat list, handled by the wikilink pass
			}
			for _, target := range bracketedTargetsIn(s.Meta[key]) {
				idx.noteFrontmatterTarget(s.ID, target)
				dst, res := idx.resolveTarget(target)
				switch res {
				case notANote:
					continue
				case unresolvable:
					st.Unresolved++
					continue
				case resolvedAmbiguously:
					st.Ambiguous++
				}
				if dst != s.ID {
					out.add(s.ID, dst, relForProperty(key))
				}
			}
		}
	}
	return out.slice(), st
}

func (idx *index) noteFrontmatterTarget(src int64, target string) {
	if idx.fmTargets[src] == nil {
		idx.fmTargets[src] = make(map[string]bool)
	}
	idx.fmTargets[src][strings.TrimSpace(target)] = true
}

// relForProperty normalises a property name into a relation name.
func relForProperty(key string) string {
	rel := strings.ToLower(strings.TrimSpace(key))
	rel = strings.NewReplacer(" ", "_", "-", "_").Replace(rel)
	if rel == "" {
		return RelLinksTo
	}
	return rel
}

// wikilinkEdges covers links written in the body. Targets already claimed by a
// frontmatter property are skipped, because the parser merges those into the
// same flat list and they carry a better relation name there.
func (idx *index) wikilinkEdges() ([]edge, OriginStats) {
	var st OriginStats
	out := newEdgeSet()
	for _, s := range idx.sources {
		for _, target := range flatTargetsIn(s.Meta["links"]) {
			if idx.fmTargets[s.ID][strings.TrimSpace(target)] {
				continue
			}
			dst, res := idx.resolveTarget(target)
			switch res {
			case notANote:
				st.Skipped++
				continue
			case unresolvable:
				st.Unresolved++
				continue
			case resolvedAmbiguously:
				st.Ambiguous++
			}
			if dst != s.ID {
				out.add(s.ID, dst, RelLinksTo)
			}
		}
	}
	return out.slice(), st
}

// ticketEdges connects anything that names a ticket to the ticket itself. This
// is the only relation that crosses corpora -- a mail, a commit and a note all
// reach the same issue -- and it needs no more than a regex over content the
// database already holds.
func (idx *index) ticketEdges(conn *sql.DB, excludeSenders []string) ([]edge, OriginStats, error) {
	var st OriginStats
	out := newEdgeSet()
	if len(idx.byKey) == 0 {
		return nil, st, nil
	}

	excluded, err := sourcesFromSenders(conn, excludeSenders)
	if err != nil {
		return nil, st, err
	}

	// Prefixes come from the issues that are actually indexed, so no project
	// key is hardcoded and a new Jira project needs no code change.
	prefixSet := make(map[string]bool)
	for key := range idx.byKey {
		if i := strings.LastIndex(key, "-"); i > 0 {
			prefixSet[key[:i]] = true
		}
	}
	prefixes := make([]string, 0, len(prefixSet))
	for p := range prefixSet {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)

	// Narrow the scan inside SQLite: only rows that could contain a key are
	// shipped to Go. GLOB is case-sensitive, which is what we want -- issue
	// keys are upper case, and matching loosely here would pull most of the
	// corpus across for nothing.
	var clauses []string
	args := make([]any, 0, len(prefixes))
	for _, p := range prefixes {
		clauses = append(clauses, "d.content GLOB ?")
		args = append(args, "*"+p+"-[0-9]*")
	}
	query := `SELECT d.source_id, d.content FROM documents d WHERE ` + strings.Join(clauses, " OR ")

	pattern := regexp.MustCompile(`\b(` + strings.Join(prefixes, "|") + `)-([0-9]{1,6})\b`)

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, st, fmt.Errorf("scan for ticket keys: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			srcID   int64
			content string
		)
		if err := rows.Scan(&srcID, &content); err != nil {
			return nil, st, fmt.Errorf("scan document: %w", err)
		}
		if excluded[srcID] {
			st.Skipped++
			continue
		}
		for _, m := range pattern.FindAllStringSubmatch(content, -1) {
			dst, ok := idx.byKey[m[1]+"-"+m[2]]
			if !ok {
				st.Unresolved++
				continue
			}
			// An issue file naturally repeats its own key.
			if dst != srcID {
				out.add(srcID, dst, RelMentions)
			}
		}
	}
	return out.slice(), st, rows.Err()
}

// sourcesFromSenders returns the sources whose mail comes from one of the given
// senders. Matching is a case-insensitive substring of the whole sender field,
// so "jira@" covers `Someone (Jira) <jira@example.atlassian.net>` without
// needing the display name. Resolved in one query up front rather than per
// document: the scan that follows visits tens of thousands of rows.
func sourcesFromSenders(conn *sql.DB, senders []string) (map[int64]bool, error) {
	excluded := make(map[int64]bool)
	if len(senders) == 0 {
		return excluded, nil
	}

	clauses := make([]string, 0, len(senders))
	args := make([]any, 0, len(senders))
	for _, sender := range senders {
		sender = strings.TrimSpace(sender)
		if sender == "" {
			continue
		}
		clauses = append(clauses, "lower(COALESCE(json_extract(metadata, '$.sender'), '')) LIKE ?")
		args = append(args, "%"+strings.ToLower(sender)+"%")
	}
	if len(clauses) == 0 {
		return excluded, nil
	}

	rows, err := conn.Query(`
		SELECT DISTINCT source_id FROM documents
		WHERE chunk_index = 0 AND metadata IS NOT NULL AND json_valid(metadata)
		  AND (`+strings.Join(clauses, " OR ")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve excluded senders: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan excluded source: %w", err)
		}
		excluded[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slog.Info("graph: senders excluded from ticket mentions",
		"patterns", len(clauses), "sources", len(excluded))
	return excluded, nil
}

// bracketedTargetsIn pulls link targets out of a frontmatter value, which YAML
// leaves as a string, a list or a nested map. Only `[[...]]` counts: frontmatter
// holds arbitrary data -- dates, page ids, titles, tags -- and reading a bare
// string as a link target invents relations out of ordinary values. A page id
// of "1" would otherwise resolve to a note called 1.md.
func bracketedTargetsIn(value any) []string {
	return targetsIn(value, false)
}

// flatTargetsIn reads the parser's `links` list, whose entries are already
// resolved to bare target names. Bracketed entries are tolerated so that
// metadata written by an older version still yields edges.
func flatTargetsIn(value any) []string {
	return targetsIn(value, true)
}

func targetsIn(value any, allowBare bool) []string {
	var out []string
	switch v := value.(type) {
	case string:
		if strings.Contains(v, "[[") {
			for _, m := range bracketedLink.FindAllStringSubmatch(v, -1) {
				if t := linkTarget(m[1]); t != "" {
					out = append(out, t)
				}
			}
			return out
		}
		if allowBare {
			if t := linkTarget(v); t != "" && !strings.ContainsAny(t, "[]") {
				out = append(out, t)
			}
		}
	case []any:
		for _, item := range v {
			out = append(out, targetsIn(item, allowBare)...)
		}
	case map[string]any:
		for _, key := range sortedKeys(v) {
			out = append(out, targetsIn(v[key], allowBare)...)
		}
	}
	return out
}

var bracketedLink = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// linkTarget strips the display half of a `target|display` link.
func linkTarget(inner string) string {
	if i := strings.Index(inner, "|"); i >= 0 {
		inner = inner[:i]
	}
	return strings.TrimSpace(inner)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// edgeSet deduplicates while preserving a deterministic order, so two rebuilds
// of unchanged data produce identical rows.
type edgeSet struct {
	seen  map[edge]bool
	order []edge
}

func newEdgeSet() *edgeSet {
	return &edgeSet{seen: make(map[edge]bool)}
}

func (s *edgeSet) add(src, dst int64, rel string) {
	e := edge{src: src, dst: dst, rel: rel}
	if s.seen[e] {
		return
	}
	s.seen[e] = true
	s.order = append(s.order, e)
}

func (s *edgeSet) slice() []edge { return s.order }

// replaceOrigin swaps in the edges for one origin inside a single transaction,
// so a failed rebuild leaves the previous edges intact rather than half of a
// new set.
func replaceOrigin(conn *sql.DB, origin string, edges []edge) (int, error) {
	tx, err := conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM graph_edges WHERE origin = ?", origin); err != nil {
		return 0, fmt.Errorf("clear origin: %w", err)
	}

	stmt, err := tx.Prepare(
		"INSERT OR IGNORE INTO graph_edges (src_source_id, dst_source_id, rel, origin) VALUES (?, ?, ?, ?)")
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	var stored int
	for _, e := range edges {
		res, err := stmt.Exec(e.src, e.dst, e.rel, origin)
		if err != nil {
			return 0, fmt.Errorf("insert edge: %w", err)
		}
		// A (src, dst, rel) triple already claimed by another origin is left
		// with its first owner rather than counted twice.
		if n, err := res.RowsAffected(); err == nil {
			stored += int(n)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return stored, nil
}
