package graph

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// setupDB creates just the tables the graph package touches. Building the real
// schema would drag in sqlite-vec for no benefit: nothing here reads a vector.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_type TEXT NOT NULL,
			source_path TEXT NOT NULL
		);
		CREATE TABLE documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			chunk_index INTEGER NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			metadata TEXT
		);
		CREATE TABLE graph_edges (
			src_source_id INTEGER NOT NULL,
			dst_source_id INTEGER NOT NULL,
			rel TEXT NOT NULL,
			origin TEXT NOT NULL,
			PRIMARY KEY (src_source_id, dst_source_id, rel)
		);`); err != nil {
		t.Fatal(err)
	}
	return db
}

// addSource inserts a markdown source with the metadata of its first chunk.
func addSource(t *testing.T, db *sql.DB, path string, meta map[string]any, content string) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO sources (source_type, source_path) VALUES ('markdown', ?)", path)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	var metaJSON any
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		metaJSON = string(b)
	}
	if _, err := db.Exec(
		"INSERT INTO documents (source_id, chunk_index, content, metadata) VALUES (?, 0, ?, ?)",
		id, content, metaJSON,
	); err != nil {
		t.Fatal(err)
	}
	return id
}

type storedEdge struct {
	Src, Dst int64
	Rel      string
	Origin   string
}

func edges(t *testing.T, db *sql.DB) []storedEdge {
	t.Helper()
	rows, err := db.Query("SELECT src_source_id, dst_source_id, rel, origin FROM graph_edges")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []storedEdge
	for rows.Next() {
		var e storedEdge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Rel, &e.Origin); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		return out[i].Rel < out[j].Rel
	})
	return out
}

func TestRebuildConfluenceHierarchy(t *testing.T) {
	db := setupDB(t)
	parent := addSource(t, db, "/sync/confluence/1.md", map[string]any{"page_id": "1"}, "")
	child := addSource(t, db, "/sync/confluence/2.md", map[string]any{"page_id": "2", "parent_id": "1"}, "")
	// A parent outside the index must be counted, not stored.
	addSource(t, db, "/sync/confluence/3.md", map[string]any{"page_id": "3", "parent_id": "999"}, "")

	stats, err := Rebuild(db, []string{OriginConfluence})
	if err != nil {
		t.Fatal(err)
	}

	want := []storedEdge{{Src: child, Dst: parent, Rel: RelChildOf, Origin: OriginConfluence}}
	if got := edges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
	if st := stats.ByOrigin[OriginConfluence]; st.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", st.Unresolved)
	}
}

// A page id stored as a JSON number must resolve the same as a string one --
// the sync writes whichever YAML produced.
func TestRebuildConfluenceNumericPageID(t *testing.T) {
	db := setupDB(t)
	parent := addSource(t, db, "/sync/a.md", map[string]any{"page_id": 11}, "")
	child := addSource(t, db, "/sync/b.md", map[string]any{"page_id": 12, "parent_id": 11}, "")

	if _, err := Rebuild(db, []string{OriginConfluence}); err != nil {
		t.Fatal(err)
	}
	want := []storedEdge{{Src: child, Dst: parent, Rel: RelChildOf, Origin: OriginConfluence}}
	if got := edges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
}

func TestRebuildFrontmatterRelationsAreTyped(t *testing.T) {
	db := setupDB(t)
	target := addSource(t, db, "/vault/Target Note.md", nil, "")
	other := addSource(t, db, "/vault/Other.md", nil, "")
	note := addSource(t, db, "/vault/Note.md", map[string]any{
		"related":   []any{"[[Target Note]]"},
		"parent":    "[[Other]]",
		"My Field":  "[[Target Note|shown as this]]",
		"unrelated": "no links here",
	}, "")

	if _, err := Rebuild(db, []string{OriginFrontmatter}); err != nil {
		t.Fatal(err)
	}

	want := []storedEdge{
		{Src: note, Dst: target, Rel: "my_field", Origin: OriginFrontmatter},
		{Src: note, Dst: target, Rel: "related", Origin: OriginFrontmatter},
		{Src: note, Dst: other, Rel: "parent", Origin: OriginFrontmatter},
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].Dst != want[j].Dst {
			return want[i].Dst < want[j].Dst
		}
		return want[i].Rel < want[j].Rel
	})
	if got := edges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v\nwant %+v", got, want)
	}
}

// The parser merges frontmatter links into the flat links list, so the wikilink
// pass must not emit a second edge for a relation the frontmatter pass already
// described more precisely.
func TestRebuildWikilinkSkipsFrontmatterTargets(t *testing.T) {
	db := setupDB(t)
	fmTarget := addSource(t, db, "/vault/Declared.md", nil, "")
	bodyTarget := addSource(t, db, "/vault/Mentioned.md", nil, "")
	note := addSource(t, db, "/vault/Note.md", map[string]any{
		"related": []any{"[[Declared]]"},
		"links":   []any{"Declared", "Mentioned"},
	}, "")

	if _, err := Rebuild(db, []string{OriginFrontmatter, OriginWikilink}); err != nil {
		t.Fatal(err)
	}

	want := []storedEdge{
		{Src: note, Dst: fmTarget, Rel: "related", Origin: OriginFrontmatter},
		{Src: note, Dst: bodyTarget, Rel: RelLinksTo, Origin: OriginWikilink},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Dst < want[j].Dst })
	if got := edges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v\nwant %+v", got, want)
	}
}

func TestRebuildWikilinkResolution(t *testing.T) {
	db := setupDB(t)
	target := addSource(t, db, "/vault/Deep/Target.md", nil, "")
	note := addSource(t, db, "/vault/Note.md", map[string]any{
		"links": []any{
			"Target",              // plain name
			"target",              // case-insensitive
			"Deep/Target",         // path form
			"Target#Some Heading", // anchor
			"Nowhere",             // unresolved
			"Note",                // self-link
		},
	}, "")

	stats, err := Rebuild(db, []string{OriginWikilink})
	if err != nil {
		t.Fatal(err)
	}

	want := []storedEdge{{Src: note, Dst: target, Rel: RelLinksTo, Origin: OriginWikilink}}
	if got := edges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
	if st := stats.ByOrigin[OriginWikilink]; st.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", st.Unresolved)
	}
}

func TestRebuildTicketMentionsCrossCorpora(t *testing.T) {
	db := setupDB(t)
	issue := addSource(t, db, "/sync/jira/CB-42.md",
		map[string]any{"issue_key": "CB-42"}, "CB-42: do the thing")
	note := addSource(t, db, "/vault/Note.md", nil, "Discussed CB-42 with the team")
	mail := addSource(t, db, "/mail/thread.md", nil, "re: CB-42 and CB-999 (not indexed)")

	stats, err := Rebuild(db, []string{OriginTicket})
	if err != nil {
		t.Fatal(err)
	}

	want := []storedEdge{
		{Src: note, Dst: issue, Rel: RelMentions, Origin: OriginTicket},
		{Src: mail, Dst: issue, Rel: RelMentions, Origin: OriginTicket},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Src < want[j].Src })
	if got := edges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v\nwant %+v", got, want)
	}
	// The issue file repeats its own key; that is not a relation.
	for _, e := range edges(t, db) {
		if e.Src == issue {
			t.Errorf("issue source mentions itself: %+v", e)
		}
	}
	if st := stats.ByOrigin[OriginTicket]; st.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1 (CB-999)", st.Unresolved)
	}
}

// Rebuilding one origin must not disturb another's edges.
func TestRebuildReplacesOnlyRequestedOrigin(t *testing.T) {
	db := setupDB(t)
	parent := addSource(t, db, "/sync/1.md", map[string]any{"page_id": "1"}, "")
	addSource(t, db, "/sync/2.md", map[string]any{"page_id": "2", "parent_id": "1"}, "")
	target := addSource(t, db, "/vault/Target.md", nil, "")
	addSource(t, db, "/vault/Note.md", map[string]any{"links": []any{"Target"}}, "")

	if _, err := Rebuild(db, nil); err != nil { // nil means every origin
		t.Fatal(err)
	}
	before := len(edges(t, db))
	if before != 2 {
		t.Fatalf("expected 2 edges after a full rebuild, got %d", before)
	}

	if _, err := Rebuild(db, []string{OriginWikilink}); err != nil {
		t.Fatal(err)
	}
	after := edges(t, db)
	if len(after) != 2 {
		t.Errorf("rebuilding one origin changed the edge count: %d -> %d (%+v)", before, len(after), after)
	}
	var sawConfluence, sawWikilink bool
	for _, e := range after {
		switch e.Origin {
		case OriginConfluence:
			sawConfluence = true
			if e.Dst != parent {
				t.Errorf("confluence edge points at %d, want %d", e.Dst, parent)
			}
		case OriginWikilink:
			sawWikilink = true
			if e.Dst != target {
				t.Errorf("wikilink edge points at %d, want %d", e.Dst, target)
			}
		}
	}
	if !sawConfluence || !sawWikilink {
		t.Errorf("expected both origins to survive, got %+v", after)
	}
}

// A rebuild of unchanged data is idempotent, so re-running it is always safe.
func TestRebuildIsIdempotent(t *testing.T) {
	db := setupDB(t)
	addSource(t, db, "/vault/Target.md", nil, "")
	addSource(t, db, "/vault/Note.md", map[string]any{"links": []any{"Target", "Target"}}, "")

	first, err := Rebuild(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := edges(t, db)
	second, err := Rebuild(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, edges(t, db)) {
		t.Error("a second rebuild changed the stored edges")
	}
	if first.Total() != second.Total() {
		t.Errorf("edge count changed between rebuilds: %d -> %d", first.Total(), second.Total())
	}
	if first.Total() != 1 {
		t.Errorf("a repeated link produced %d edges, want 1", first.Total())
	}
}

func TestRebuildUnknownOrigin(t *testing.T) {
	db := setupDB(t)
	if _, err := Rebuild(db, []string{"invented"}); err == nil {
		t.Error("expected an error for an unknown origin")
	}
}

func TestHelpers(t *testing.T) {
	t.Run("baseKey", func(t *testing.T) {
		for in, want := range map[string]string{
			"/a/b/Note.md":    "note",
			"/a/b/UPPER.MD":   "upper",
			"Note":            "note",
			"/a/Two Words.md": "two words",
		} {
			if got := baseKey(in); got != want {
				t.Errorf("baseKey(%q) = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("linkTarget", func(t *testing.T) {
		for in, want := range map[string]string{
			"Target":         "Target",
			"Target|Display": "Target",
			"  Spaced  ":     "Spaced",
		} {
			if got := linkTarget(in); got != want {
				t.Errorf("linkTarget(%q) = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("relForProperty", func(t *testing.T) {
		for in, want := range map[string]string{
			"related":    "related",
			"My Field":   "my_field",
			"depends-on": "depends_on",
			"  ":         RelLinksTo,
		} {
			if got := relForProperty(in); got != want {
				t.Errorf("relForProperty(%q) = %q, want %q", in, got, want)
			}
		}
	})

	// Frontmatter holds arbitrary values, so only a bracketed one is a link.
	t.Run("bracketedTargetsIn", func(t *testing.T) {
		cases := []struct {
			name string
			in   any
			want []string
		}{
			{"bracketed value", "[[Target]]", []string{"Target"}},
			{"list", []any{"[[A]]", "[[B]]"}, []string{"A", "B"}},
			{"nested map", map[string]any{"k": "[[A]]"}, []string{"A"}},
			{"display half dropped", "[[A|shown]]", []string{"A"}},
			{"a plain string is not a link", "Target", nil},
			{"a page id is not a link", "1", nil},
			{"a date is not a link", "2026-09-04", nil},
			{"number", 42, nil},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got := bracketedTargetsIn(c.in)
				if len(got) == 0 && len(c.want) == 0 {
					return
				}
				if !reflect.DeepEqual(got, c.want) {
					t.Errorf("bracketedTargetsIn(%v) = %v, want %v", c.in, got, c.want)
				}
			})
		}
	})

	// The flat links list is already resolved to bare names.
	t.Run("flatTargetsIn", func(t *testing.T) {
		cases := []struct {
			name string
			in   any
			want []string
		}{
			{"bare name", "Target", []string{"Target"}},
			{"list of bare names", []any{"A", "B"}, []string{"A", "B"}},
			{"bracketed still tolerated", "[[A]]", []string{"A"}},
			{"number", 42, nil},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got := flatTargetsIn(c.in)
				if len(got) == 0 && len(c.want) == 0 {
					return
				}
				if !reflect.DeepEqual(got, c.want) {
					t.Errorf("flatTargetsIn(%v) = %v, want %v", c.in, got, c.want)
				}
			})
		}
	})
}
