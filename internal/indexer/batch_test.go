package indexer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
)

func testBatchConfig(batchSize int) *config.Config {
	return &config.Config{
		EmbeddingBatchSize: batchSize,
		EmbeddingWorkers:   2,
		ChunkSizeTokens:    500,
		ChunkOverlapTokens: 50,
	}
}

// makeItems builds n items, each holding chunksPer chunks of non-blank text.
func makeItems(n, chunksPer int) []*indexItem {
	items := make([]*indexItem, n)
	for i := range items {
		chunks := make([]chunker.Chunk, chunksPer)
		for j := range chunks {
			chunks[j] = chunker.Chunk{
				Text:       fmt.Sprintf("item %d chunk %d", i, j),
				Title:      fmt.Sprintf("Item %d", i),
				ChunkIndex: j,
			}
		}
		items[i] = &indexItem{
			SourcePath: fmt.Sprintf("id-%d", i),
			SourceType: "rss",
			Title:      fmt.Sprintf("Item %d", i),
			Chunks:     chunks,
			Metadata:   map[string]any{"date": "2026-01-01T00:00:00Z"},
		}
	}
	return items
}

func collectBatches(items []*indexItem, cfg *config.Config) []*itemBatch {
	b := &itemBatcher{
		cfg:    cfg,
		total:  len(items),
		itemAt: func(i int) *indexItem { return items[i] },
	}
	var out []*itemBatch
	for {
		next := b.nextBatch()
		if next == nil {
			return out
		}
		out = append(out, next)
	}
}

// Every batch must be internally consistent: one text per chunk, flattened in
// item-then-chunk order. writeItemBatch slices the returned vectors by these
// offsets, so a mismatch would attach the wrong embedding to a document.
func assertBatchesConsistent(t *testing.T, batches []*itemBatch, wantItems int) {
	t.Helper()

	seen := 0
	for bi, b := range batches {
		pos := 0
		for _, item := range b.items {
			for _, c := range item.Chunks {
				if pos >= len(b.texts) {
					t.Fatalf("batch %d: more chunks than texts", bi)
				}
				if b.texts[pos] != c.Text {
					t.Fatalf("batch %d: text at %d does not match its chunk", bi, pos)
				}
				pos++
			}
		}
		if pos != len(b.texts) {
			t.Fatalf("batch %d: %d chunks but %d texts", bi, pos, len(b.texts))
		}
		seen += len(b.items)
	}

	if seen != wantItems {
		t.Errorf("batches cover %d items, want %d", seen, wantItems)
	}
}

func TestItemBatcherGroupsToTarget(t *testing.T) {
	items := makeItems(10, 1)
	batches := collectBatches(items, testBatchConfig(4))

	assertBatchesConsistent(t, batches, 10)

	if len(batches) != 3 {
		t.Fatalf("got %d batches, want 3", len(batches))
	}
	for i, want := range []int{4, 4, 2} {
		if got := len(batches[i].texts); got != want {
			t.Errorf("batch %d has %d texts, want %d", i, got, want)
		}
	}
}

// An item's chunks must never be split across batches, even when the item alone
// exceeds the target — writeItemBatch assumes one item's vectors are contiguous
// within a single batch.
func TestItemBatcherKeepsItemChunksTogether(t *testing.T) {
	items := makeItems(3, 5)
	batches := collectBatches(items, testBatchConfig(2))

	assertBatchesConsistent(t, batches, 3)

	for bi, b := range batches {
		for _, item := range b.items {
			if len(item.Chunks) != 5 {
				t.Errorf("batch %d: item %s has %d chunks, want all 5",
					bi, item.SourcePath, len(item.Chunks))
			}
		}
	}
}

func TestItemBatcherSkipsBlankAndNilItems(t *testing.T) {
	items := []*indexItem{
		{SourcePath: "a1", Title: "Real", Chunks: []chunker.Chunk{{Text: "has content"}}},
		nil,
		{SourcePath: "a2", Title: "Blank", Chunks: []chunker.Chunk{{Text: "   "}}},
		{SourcePath: "a3", Title: "No chunks"},
		{SourcePath: "a4", Title: "Also real", Chunks: []chunker.Chunk{{Text: "more content"}}},
	}

	batches := collectBatches(items, testBatchConfig(32))

	var got []string
	for _, b := range batches {
		for _, item := range b.items {
			got = append(got, item.SourcePath)
		}
	}

	if strings.Join(got, ",") != "a1,a4" {
		t.Errorf("batched items = %v, want [a1 a4]", got)
	}
}

func TestItemBatcherEmptyInput(t *testing.T) {
	if got := collectBatches(nil, testBatchConfig(32)); len(got) != 0 {
		t.Errorf("got %d batches for no items, want 0", len(got))
	}
}

// A failed embedding call must be charged to every item in the batch, so the
// run's totals still add up.
func TestWriteItemBatchEmbedError(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	b := &itemBatch{
		items: makeItems(3, 1),
		err:   fmt.Errorf("ollama unreachable"),
	}
	result := &IndexResult{}
	writeItemBatch(conn, collID, b, result, false)

	if result.Errors != 3 {
		t.Errorf("got %d errors, want 3 (one per item)", result.Errors)
	}
	if result.Indexed != 0 {
		t.Errorf("got %d indexed, want 0", result.Indexed)
	}
}

// If Ollama returns a different number of vectors than we sent texts, the
// offsets are meaningless — the batch must be rejected rather than store
// mismatched embeddings.
func TestWriteItemBatchVectorCountMismatch(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	b := collectBatches(makeItems(2, 1), testBatchConfig(32))[0]
	b.vecs = [][]float32{make([]float32, 1024)} // one vector, two texts

	result := &IndexResult{}
	writeItemBatch(conn, collID, b, result, false)

	if result.Indexed != 0 {
		t.Errorf("got %d indexed, want 0 on a count mismatch", result.Indexed)
	}
	if result.Errors == 0 {
		t.Error("count mismatch should be reported as an error")
	}

	var docs int
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", collID).Scan(&docs)
	if docs != 0 {
		t.Errorf("%d documents written despite a count mismatch, want 0", docs)
	}
}

// Each item's documents must carry that item's own vectors and metadata — the
// whole point of tracking offsets through the batch.
func TestWriteItemBatchStoresPerItemRows(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "email", "system")

	items := makeItems(3, 2)
	for _, item := range items {
		item.SourceType = "email"
	}
	items[1].Metadata = map[string]any{"sender": "someone@example.com"}

	b := collectBatches(items, testBatchConfig(32))[0]
	b.vecs = make([][]float32, len(b.texts))
	for i := range b.vecs {
		b.vecs[i] = make([]float32, 1024)
	}

	result := &IndexResult{}
	writeItemBatch(conn, collID, b, result, false)

	if result.Indexed != 3 {
		t.Fatalf("got %d indexed, want 3", result.Indexed)
	}

	var sources, docs int
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ? AND source_type = 'email'", collID).Scan(&sources)
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", collID).Scan(&docs)
	if sources != 3 {
		t.Errorf("got %d sources, want 3", sources)
	}
	if docs != 6 {
		t.Errorf("got %d documents, want 6 (3 items x 2 chunks)", docs)
	}

	var sender string
	err := conn.QueryRow(`
		SELECT json_extract(d.metadata, '$.sender') FROM documents d
		JOIN sources s ON d.source_id = s.id
		WHERE s.source_path = 'id-1' LIMIT 1`).Scan(&sender)
	if err != nil {
		t.Fatal(err)
	}
	if sender != "someone@example.com" {
		t.Errorf("metadata did not follow its own item: got %q", sender)
	}
}

// Items the individual retry could not embed are removed from the batch, so
// they must still be counted as errors rather than vanishing from the totals.
func TestWriteItemBatchCountsDroppedItems(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	// What embedItemsIndividually leaves behind when every item failed: no
	// items, no texts, no error, but a dropped count.
	b := &itemBatch{dropped: 3}

	result := &IndexResult{}
	writeItemBatch(conn, collID, b, result, false)

	if result.Errors != 3 {
		t.Errorf("got %d errors, want 3", result.Errors)
	}
	if result.Indexed != 0 {
		t.Errorf("got %d indexed, want 0", result.Indexed)
	}
	if len(result.ErrorMessages) == 0 {
		t.Error("dropped items should be reported")
	}
}

// A partially recovered batch stores what survived and counts what did not.
func TestWriteItemBatchPartialRecovery(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	b := collectBatches(makeItems(2, 1), testBatchConfig(32))[0]
	b.vecs = [][]float32{make([]float32, 1024), make([]float32, 1024)}
	b.dropped = 1 // a third item failed and was removed

	result := &IndexResult{}
	writeItemBatch(conn, collID, b, result, false)

	if result.Indexed != 2 {
		t.Errorf("got %d indexed, want 2 survivors stored", result.Indexed)
	}
	if result.Errors != 1 {
		t.Errorf("got %d errors, want 1 for the dropped item", result.Errors)
	}
}

// Item metadata describes the whole source (sender, feed, book author); chunk
// metadata describes one chunk (page number, symbol path). Both must survive,
// and the chunk's own keys must win.
func TestChunkMetadataMerge(t *testing.T) {
	item := &indexItem{Metadata: map[string]any{"feed_name": "Example", "date": "2026-01-01"}}
	chunk := chunker.Chunk{Metadata: map[string]any{"page_number": 7, "date": "chunk-wins"}}

	got, err := chunkMetadata(item, chunk)
	if err != nil {
		t.Fatal(err)
	}

	var merged map[string]any
	if err := json.Unmarshal([]byte(got), &merged); err != nil {
		t.Fatalf("not valid JSON: %v (%s)", err, got)
	}
	if merged["feed_name"] != "Example" {
		t.Errorf("item metadata lost: %v", merged)
	}
	if merged["page_number"] != float64(7) {
		t.Errorf("chunk metadata lost: %v", merged)
	}
	if merged["date"] != "chunk-wins" {
		t.Errorf("chunk metadata should win on conflict, got %v", merged["date"])
	}
}

// Nothing to record must stay NULL rather than becoming the string "{}", which
// would make json_extract filters see an empty object instead of no metadata.
func TestChunkMetadataEmpty(t *testing.T) {
	got, err := chunkMetadata(&indexItem{}, chunker.Chunk{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// Re-indexing replaces the previous version rather than accumulating
// duplicates, and leaves no stranded vectors behind. Purging is the batch's
// job — storeItem alone deliberately does not clear the old rows, because the
// per-source vector delete scans the whole vec0 table.
func TestWriteItemBatchReplacesExisting(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	newBatch := func() *itemBatch {
		b := collectBatches(makeItems(2, 2), testBatchConfig(32))[0]
		b.vecs = make([][]float32, len(b.texts))
		for i := range b.vecs {
			b.vecs[i] = make([]float32, 1024)
		}
		return b
	}

	for pass := 1; pass <= 2; pass++ {
		result := &IndexResult{}
		writeItemBatch(conn, collID, newBatch(), result, false)
		if result.Errors != 0 {
			t.Fatalf("pass %d: %d errors: %v", pass, result.Errors, result.ErrorMessages)
		}

		var sources, docs, vecs int
		conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ?", collID).Scan(&sources)
		conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", collID).Scan(&docs)
		conn.QueryRow("SELECT COUNT(*) FROM vec_documents").Scan(&vecs)

		if sources != 2 {
			t.Errorf("pass %d: got %d sources, want 2", pass, sources)
		}
		if docs != 4 {
			t.Errorf("pass %d: got %d documents, want 4", pass, docs)
		}
		if vecs != docs {
			t.Errorf("pass %d: %d vectors for %d documents — stranded embeddings", pass, vecs, docs)
		}
	}
}

// The purge must only touch the items in the batch, not the rest of the
// collection.
func TestPurgeSourceDocumentsLeavesOthersAlone(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	all := makeItems(4, 1)
	b := collectBatches(all, testBatchConfig(32))[0]
	b.vecs = make([][]float32, len(b.texts))
	for i := range b.vecs {
		b.vecs[i] = make([]float32, 1024)
	}
	writeItemBatch(conn, collID, b, &IndexResult{}, false)

	// Purge only two of the four.
	purgeSourceDocuments(conn, collID, all[:2])

	var docs, vecs int
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", collID).Scan(&docs)
	conn.QueryRow("SELECT COUNT(*) FROM vec_documents").Scan(&vecs)
	if docs != 2 {
		t.Errorf("got %d documents, want the 2 untouched ones", docs)
	}
	if vecs != 2 {
		t.Errorf("got %d vectors, want 2 — purge must remove exactly the batch's embeddings", vecs)
	}
}

// A rebuild clears the collection up front, so writeItemBatch must skip the
// per-batch purge — that purge full-scans the vector table and is the single
// most expensive thing in a force run.
func TestPreClearedSkipsPurge(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	seed := func() {
		b := collectBatches(makeItems(3, 1), testBatchConfig(32))[0]
		b.vecs = make([][]float32, len(b.texts))
		for i := range b.vecs {
			b.vecs[i] = make([]float32, 1024)
		}
		writeItemBatch(conn, collID, b, &IndexResult{}, false)
	}
	seed()

	// Clearing the collection is what a --force run does before indexing.
	if err := db.ClearCollectionData(conn, collID); err != nil {
		t.Fatal(err)
	}
	var docs, srcs int
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", collID).Scan(&docs)
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ?", collID).Scan(&srcs)
	if docs != 0 || srcs != 0 {
		t.Fatalf("clear left %d documents and %d sources", docs, srcs)
	}

	// With preCleared, writing the same items again must produce exactly one
	// copy — no duplicates, no stranded vectors.
	b := collectBatches(makeItems(3, 1), testBatchConfig(32))[0]
	b.vecs = make([][]float32, len(b.texts))
	for i := range b.vecs {
		b.vecs[i] = make([]float32, 1024)
	}
	result := &IndexResult{}
	writeItemBatch(conn, collID, b, result, true)

	if result.Errors != 0 {
		t.Fatalf("%d errors: %v", result.Errors, result.ErrorMessages)
	}
	var vecs int
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", collID).Scan(&docs)
	conn.QueryRow("SELECT COUNT(*) FROM vec_documents").Scan(&vecs)
	if docs != 3 || vecs != 3 {
		t.Errorf("got %d documents / %d vectors, want 3 / 3", docs, vecs)
	}
}

// Rebuilding one repo must not wipe the other repos sharing its collection.
func TestClearRepoForRebuildIsScoped(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "code", "code")

	items := makeItems(4, 1)
	items[0].SourcePath = "/repos/alpha/main.go"
	items[1].SourcePath = "/repos/alpha/util.go"
	items[2].SourcePath = "/repos/beta/main.go"
	items[3].SourcePath = "git:///repos/alpha#abc123"
	for _, it := range items {
		it.SourceType = "code"
	}

	b := collectBatches(items, testBatchConfig(32))[0]
	b.vecs = make([][]float32, len(b.texts))
	for i := range b.vecs {
		b.vecs[i] = make([]float32, 1024)
	}
	writeItemBatch(conn, collID, b, &IndexResult{}, false)

	if !clearRepoForRebuild(conn, collID, "/repos/alpha", true) {
		t.Fatal("clearRepoForRebuild reported failure")
	}

	var remaining []string
	rows, _ := conn.Query("SELECT source_path FROM sources WHERE collection_id = ? ORDER BY source_path", collID)
	for rows.Next() {
		var p string
		rows.Scan(&p)
		remaining = append(remaining, p)
	}
	rows.Close()

	if len(remaining) != 1 || remaining[0] != "/repos/beta/main.go" {
		t.Errorf("remaining sources = %v, want only beta's file", remaining)
	}

	var vecs int
	conn.QueryRow("SELECT COUNT(*) FROM vec_documents").Scan(&vecs)
	if vecs != 1 {
		t.Errorf("got %d vectors, want 1 — alpha's embeddings should be gone, beta's kept", vecs)
	}
}

// storeItem writes SourceType onto the sources row verbatim, so an item that
// forgets to set it produces an untyped source that no type filter can find.
func TestStoreItemRequiresSourceType(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	item := makeItems(1, 1)[0]
	item.SourceType = "" // what articleToItem used to produce
	if err := storeItem(conn, collID, item, [][]float32{make([]float32, 1024)}); err != nil {
		t.Fatal(err)
	}

	var got string
	conn.QueryRow("SELECT source_type FROM sources WHERE collection_id = ?", collID).Scan(&got)
	if got != "" {
		t.Fatalf("setup wrong: expected the empty type to be stored, got %q", got)
	}

	// And with a type set, it round-trips.
	item2 := makeItems(1, 1)[0]
	item2.SourcePath = "id-typed"
	item2.SourceType = "rss"
	if err := storeItem(conn, collID, item2, [][]float32{make([]float32, 1024)}); err != nil {
		t.Fatal(err)
	}
	conn.QueryRow("SELECT source_type FROM sources WHERE source_path = 'id-typed'").Scan(&got)
	if got != "rss" {
		t.Errorf("source_type = %q, want rss", got)
	}
}
