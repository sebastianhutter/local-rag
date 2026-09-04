package graph

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// addCollection and addEdge build a graph directly, so a traversal test does
// not depend on how edges were derived.
func addCollection(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO collections (name) VALUES (?)", name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func addEdge(t *testing.T, db *sql.DB, src, dst int64, rel, origin string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT OR IGNORE INTO graph_edges (src_source_id, dst_source_id, rel, origin) VALUES (?, ?, ?, ?)",
		src, dst, rel, origin,
	); err != nil {
		t.Fatal(err)
	}
}

// setupExpandDB adds the collections table and the collection_id column that
// describe() joins on, which the derivation tests do not need.
func setupExpandDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE collections (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL);
		CREATE TABLE sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER NOT NULL,
			source_type TEXT NOT NULL,
			source_path TEXT NOT NULL
		);
		CREATE TABLE documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			chunk_index INTEGER NOT NULL,
			title TEXT,
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
	return db, addCollection(t, db, "vault")
}

func addNode(t *testing.T, db *sql.DB, collID int64, name, content string) int64 {
	t.Helper()
	res, err := db.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path) VALUES (?, 'markdown', ?)",
		collID, "/vault/"+name+".md")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(
		"INSERT INTO documents (source_id, chunk_index, title, content) VALUES (?, 0, ?, ?)",
		id, name, content); err != nil {
		t.Fatal(err)
	}
	return id
}

func ids(neighbours []Neighbour) []int64 {
	out := make([]int64, len(neighbours))
	for i, n := range neighbours {
		out[i] = n.SourceID
	}
	return out
}

func TestExpandOneHop(t *testing.T) {
	db, coll := setupExpandDB(t)
	seed := addNode(t, db, coll, "Seed", "seed body")
	out := addNode(t, db, coll, "Outbound", "outbound body")
	in := addNode(t, db, coll, "Inbound", "inbound body")
	addNode(t, db, coll, "Unrelated", "nothing")

	addEdge(t, db, seed, out, RelLinksTo, OriginWikilink)
	// Direction must not matter: something linking *to* the seed is as
	// relevant as something the seed links to.
	addEdge(t, db, in, seed, RelLinksTo, OriginWikilink)

	got, err := Expand(db, []int64{seed}, ExpandOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d neighbours (%v), want 2", len(got), ids(got))
	}
	for _, n := range got {
		if n.SourceID != out && n.SourceID != in {
			t.Errorf("unexpected neighbour %d", n.SourceID)
		}
		if n.Hops != 1 {
			t.Errorf("Hops = %d, want 1", n.Hops)
		}
		if n.ViaID != seed {
			t.Errorf("ViaID = %d, want %d", n.ViaID, seed)
		}
		if n.Collection != "vault" || n.Title == "" || n.Snippet == "" {
			t.Errorf("neighbour not described: %+v", n)
		}
	}
}

// A seed is never returned as its own neighbour, and neither is a node already
// reached at a nearer hop.
func TestExpandExcludesSeedsAndRepeats(t *testing.T) {
	db, coll := setupExpandDB(t)
	a := addNode(t, db, coll, "A", "a")
	b := addNode(t, db, coll, "B", "b")
	addEdge(t, db, a, b, RelLinksTo, OriginWikilink)
	addEdge(t, db, b, a, RelMentions, OriginTicket)

	got, err := Expand(db, []int64{a, b}, ExpandOptions{Hops: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no neighbours when both ends are seeds", ids(got))
	}
}

func TestExpandTwoHops(t *testing.T) {
	db, coll := setupExpandDB(t)
	seed := addNode(t, db, coll, "Seed", "s")
	mid := addNode(t, db, coll, "Mid", "m")
	far := addNode(t, db, coll, "Far", "f")
	addEdge(t, db, seed, mid, RelLinksTo, OriginWikilink)
	addEdge(t, db, mid, far, RelLinksTo, OriginWikilink)

	oneHop, err := Expand(db, []int64{seed}, ExpandOptions{Hops: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(oneHop) != 1 || oneHop[0].SourceID != mid {
		t.Errorf("one hop = %v, want [%d]", ids(oneHop), mid)
	}

	twoHop, err := Expand(db, []int64{seed}, ExpandOptions{Hops: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(twoHop) != 2 {
		t.Fatalf("two hops = %v, want 2 neighbours", ids(twoHop))
	}
	if twoHop[0].SourceID != mid || twoHop[0].Hops != 1 {
		t.Errorf("nearest neighbour is %+v, want mid at hop 1", twoHop[0])
	}
	if twoHop[1].SourceID != far || twoHop[1].Hops != 2 {
		t.Errorf("far neighbour is %+v, want far at hop 2", twoHop[1])
	}
	if twoHop[1].ViaID != mid {
		t.Errorf("ViaID = %d, want %d", twoHop[1].ViaID, mid)
	}
}

// Hops beyond MaxHops are clamped rather than honoured.
func TestExpandClampsHops(t *testing.T) {
	db, coll := setupExpandDB(t)
	chain := make([]int64, 5)
	for i := range chain {
		chain[i] = addNode(t, db, coll, fmt.Sprintf("N%d", i), "x")
	}
	for i := 0; i < len(chain)-1; i++ {
		addEdge(t, db, chain[i], chain[i+1], RelLinksTo, OriginWikilink)
	}
	got, err := Expand(db, []int64{chain[0]}, ExpandOptions{Hops: 99, Limit: 100, HubCap: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxHops {
		t.Errorf("got %d neighbours (%v), want %d with hops clamped", len(got), ids(got), MaxHops)
	}
}

// The rule that matters most: a hub is a destination, not a corridor.
func TestExpandHubCap(t *testing.T) {
	db, coll := setupExpandDB(t)
	seed := addNode(t, db, coll, "Seed", "s")
	hub := addNode(t, db, coll, "Hub", "h")
	addEdge(t, db, seed, hub, RelMentions, OriginTicket)
	// Give the hub a large degree.
	for i := 0; i < 12; i++ {
		other := addNode(t, db, coll, fmt.Sprintf("Other%d", i), "o")
		addEdge(t, db, hub, other, RelMentions, OriginTicket)
	}

	// By default a hub is neither traversed through nor returned: a source
	// with a large degree is a table of contents, not an answer.
	got, err := Expand(db, []int64{seed}, ExpandOptions{Hops: 2, HubCap: 5, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want nothing -- the only neighbour is a hub", ids(got))
	}

	// IncludeHubs returns it without traversing through it.
	got, err = Expand(db, []int64{seed}, ExpandOptions{Hops: 2, HubCap: 5, Limit: 100, IncludeHubs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != hub {
		t.Fatalf("got %v, want just the hub %d", ids(got), hub)
	}

	// Raise the cap above the hub's degree and its neighbours appear. The
	// per-seed cap still applies, so the hub contributes at most that many.
	got, err = Expand(db, []int64{seed},
		ExpandOptions{Hops: 2, HubCap: 50, Limit: 100, PerSeedLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 13 {
		t.Errorf("got %d neighbours with a high cap, want 13", len(got))
	}
}

// A hub that arrived among several search results contributes nothing: a
// document enumerating hundreds of tickets is not a neighbourhood. (A hub the
// caller named explicitly is a different case -- see
// TestExpandSingleSeedIgnoresHubCap.)
func TestExpandIncidentalHubSeedIsNotExpanded(t *testing.T) {
	db, coll := setupExpandDB(t)
	hub := addNode(t, db, coll, "Hub", "h")
	for i := 0; i < 10; i++ {
		other := addNode(t, db, coll, fmt.Sprintf("Other%d", i), "o")
		addEdge(t, db, hub, other, RelMentions, OriginTicket)
	}
	// A second, unrelated seed makes this a search-shaped expansion.
	quiet := addNode(t, db, coll, "Quiet", "q")

	got, err := Expand(db, []int64{hub, quiet}, ExpandOptions{HubCap: 5, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing expanded from an incidental hub seed", ids(got))
	}
}

// Ranking: nearer first, then the more specific neighbour. A node that two
// things point at says more than one four hundred things mention.
func TestExpandRanksRareNeighboursFirst(t *testing.T) {
	db, coll := setupExpandDB(t)
	seed := addNode(t, db, coll, "Seed", "s")
	rare := addNode(t, db, coll, "Rare", "r")
	common := addNode(t, db, coll, "Common", "c")
	addEdge(t, db, seed, rare, RelLinksTo, OriginWikilink)
	addEdge(t, db, seed, common, RelLinksTo, OriginWikilink)
	for i := 0; i < 6; i++ {
		other := addNode(t, db, coll, fmt.Sprintf("Other%d", i), "o")
		addEdge(t, db, common, other, RelMentions, OriginTicket)
	}

	got, err := Expand(db, []int64{seed}, ExpandOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("got %v, want at least 2", ids(got))
	}
	if got[0].SourceID != rare {
		t.Errorf("first neighbour is %d (degree %d), want the rarer %d",
			got[0].SourceID, got[0].Degree, rare)
	}
}

func TestExpandFiltersAndLimit(t *testing.T) {
	db, coll := setupExpandDB(t)
	seed := addNode(t, db, coll, "Seed", "s")
	linked := addNode(t, db, coll, "Linked", "l")
	mentioned := addNode(t, db, coll, "Mentioned", "m")
	addEdge(t, db, seed, linked, RelLinksTo, OriginWikilink)
	addEdge(t, db, seed, mentioned, RelMentions, OriginTicket)

	byRel, err := Expand(db, []int64{seed}, ExpandOptions{Rels: []string{RelLinksTo}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byRel) != 1 || byRel[0].SourceID != linked {
		t.Errorf("rel filter gave %v, want [%d]", ids(byRel), linked)
	}

	byOrigin, err := Expand(db, []int64{seed}, ExpandOptions{Origins: []string{OriginTicket}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byOrigin) != 1 || byOrigin[0].SourceID != mentioned {
		t.Errorf("origin filter gave %v, want [%d]", ids(byOrigin), mentioned)
	}

	limited, err := Expand(db, []int64{seed}, ExpandOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Errorf("limit gave %d neighbours, want 1", len(limited))
	}
}

func TestExpandNoSeeds(t *testing.T) {
	db, _ := setupExpandDB(t)
	got, err := Expand(db, nil, ExpandOptions{})
	if err != nil || got != nil {
		t.Errorf("Expand(nil) = %v, %v; want nil, nil", got, err)
	}
}

func TestSnippet(t *testing.T) {
	if got := snippet("  spaced   out\ntext  "); got != "spaced out text" {
		t.Errorf("snippet collapsed to %q", got)
	}
	long := strings.Repeat("word ", 100)
	got := snippet(long)
	if len([]rune(got)) > snippetLen+3 {
		t.Errorf("snippet is %d runes, want <= %d", len([]rune(got)), snippetLen+3)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated snippet does not end in an ellipsis: %q", got)
	}
	// Multi-byte content must not be cut mid-rune.
	if got := snippet(strings.Repeat("äöü ", 100)); !strings.HasSuffix(got, "...") {
		t.Errorf("unexpected multi-byte snippet: %q", got)
	}
}

// One prolific seed must not monopolise the result: a wiki page with thirty
// children would otherwise bury what every other seed found.
func TestExpandPerSeedLimit(t *testing.T) {
	db, coll := setupExpandDB(t)
	prolific := addNode(t, db, coll, "Prolific", "p")
	modest := addNode(t, db, coll, "Modest", "m")
	only := addNode(t, db, coll, "Only", "o")
	addEdge(t, db, modest, only, RelLinksTo, OriginWikilink)
	for i := 0; i < 10; i++ {
		child := addNode(t, db, coll, fmt.Sprintf("Child%d", i), "c")
		addEdge(t, db, prolific, child, RelChildOf, OriginConfluence)
	}

	got, err := Expand(db, []int64{prolific, modest},
		ExpandOptions{HubCap: 50, PerSeedLimit: 3, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	// 3 from the prolific seed plus the modest seed's single neighbour.
	if len(got) != 4 {
		t.Errorf("got %d neighbours (%v), want 4", len(got), ids(got))
	}
	var sawOnly bool
	for _, n := range got {
		if n.SourceID == only {
			sawOnly = true
		}
	}
	if !sawOnly {
		t.Error("the modest seed's neighbour was crowded out by the prolific one")
	}
}

// The rule that makes hierarchy usable: an epic with more children than the
// hub cap is still returned as its story's parent. "What does this belong to"
// is the question, and the answer is one node, not a fan-out.
func TestExpandReturnsParentEvenWhenItIsAHub(t *testing.T) {
	db, coll := setupExpandDB(t)
	epic := addNode(t, db, coll, "Epic", "e")
	story := addNode(t, db, coll, "Story", "s")
	addEdge(t, db, story, epic, RelChildOf, OriginJira)
	// Give the epic many other children, taking it past the cap.
	for i := 0; i < 12; i++ {
		sibling := addNode(t, db, coll, fmt.Sprintf("Sibling%d", i), "x")
		addEdge(t, db, sibling, epic, RelChildOf, OriginJira)
	}

	got, err := Expand(db, []int64{story}, ExpandOptions{HubCap: 5, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != epic {
		t.Fatalf("got %v, want just the epic %d", ids(got), epic)
	}

	// Two hops must still not travel through it and drag in the siblings.
	got, err = Expand(db, []int64{story}, ExpandOptions{Hops: 2, HubCap: 5, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d neighbours at two hops (%v), want 1 -- the hub is not a route",
			len(got), ids(got))
	}
}

// A parent must also survive the per-seed cap. Ranking by ascending degree
// would otherwise sort a high-degree epic last and cut it.
func TestExpandRanksParentAheadOfMentions(t *testing.T) {
	db, coll := setupExpandDB(t)
	story := addNode(t, db, coll, "Story", "s")
	epic := addNode(t, db, coll, "Epic", "e")
	addEdge(t, db, story, epic, RelChildOf, OriginJira)
	for i := 0; i < 8; i++ {
		sibling := addNode(t, db, coll, fmt.Sprintf("Sibling%d", i), "x")
		addEdge(t, db, sibling, epic, RelChildOf, OriginJira)
	}
	// Several low-degree mentions that would otherwise fill the allowance.
	for i := 0; i < 4; i++ {
		mention := addNode(t, db, coll, fmt.Sprintf("Mail%d", i), "m")
		addEdge(t, db, mention, story, RelMentions, OriginTicket)
	}

	// Two seeds, so the per-seed cap applies: with a single seed it is waived
	// and this would not test the ranking that keeps a parent inside it.
	other := addNode(t, db, coll, "Other", "o")
	got, err := Expand(db, []int64{story, other}, ExpandOptions{HubCap: 50, PerSeedLimit: 2, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d neighbours (%v), want 2 -- the story's allowance", len(got), ids(got))
	}
	if got[0].SourceID != epic {
		t.Errorf("first neighbour is %d (rel %s, degree %d), want the parent %d",
			got[0].SourceID, got[0].Rel, got[0].Degree, epic)
	}
}

func TestEffectiveDegree(t *testing.T) {
	if got := effectiveDegree(RelChildOf, 400); got != 0 {
		t.Errorf("a parent ranks at %d, want 0 regardless of its children", got)
	}
	if got := effectiveDegree(RelMentions, 7); got != 7 {
		t.Errorf("effectiveDegree(mentions, 7) = %d, want 7", got)
	}
}

// A caller naming one node is asking about that node, so its degree is beside
// the point: "what depends on this module" is a question about a module with
// hundreds of callers, and refusing to expand answers it with silence.
func TestExpandSingleSeedIgnoresHubCap(t *testing.T) {
	db, coll := setupExpandDB(t)
	module := addNode(t, db, coll, "Module", "m")
	for i := 0; i < 12; i++ {
		caller := addNode(t, db, coll, fmt.Sprintf("Caller%d", i), "c")
		addEdge(t, db, caller, module, RelDependsOn, OriginTerraform)
	}

	got, err := Expand(db, []int64{module}, ExpandOptions{HubCap: 5, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Errorf("got %d neighbours, want all 12 callers of an explicitly named seed", len(got))
	}
}

// Several seeds arrived from a search rather than a decision, so one prolific
// seed among them must not flood the answer.
func TestExpandMultipleSeedsStillCapped(t *testing.T) {
	db, coll := setupExpandDB(t)
	hub := addNode(t, db, coll, "Hub", "h")
	for i := 0; i < 12; i++ {
		other := addNode(t, db, coll, fmt.Sprintf("Other%d", i), "o")
		addEdge(t, db, hub, other, RelMentions, OriginTicket)
	}
	modest := addNode(t, db, coll, "Modest", "m")
	only := addNode(t, db, coll, "Only", "o")
	addEdge(t, db, modest, only, RelLinksTo, OriginWikilink)

	got, err := Expand(db, []int64{hub, modest}, ExpandOptions{HubCap: 5, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != only {
		t.Errorf("got %v, want only the modest seed's neighbour %d -- the hub must not flood",
			ids(got), only)
	}
}

// The cap still governs later hops even for a single seed: a hub reached in
// passing is a route to everything and a statement about nothing.
func TestExpandSingleSeedStillStopsAtHubsLater(t *testing.T) {
	db, coll := setupExpandDB(t)
	seed := addNode(t, db, coll, "Seed", "s")
	hub := addNode(t, db, coll, "Hub", "h")
	addEdge(t, db, seed, hub, RelMentions, OriginTicket)
	for i := 0; i < 12; i++ {
		other := addNode(t, db, coll, fmt.Sprintf("Other%d", i), "o")
		addEdge(t, db, hub, other, RelMentions, OriginTicket)
	}

	got, err := Expand(db, []int64{seed},
		ExpandOptions{Hops: 2, HubCap: 5, Limit: 100, IncludeHubs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != hub {
		t.Errorf("got %v, want just the hub -- hop 2 must not pass through it", ids(got))
	}
}
