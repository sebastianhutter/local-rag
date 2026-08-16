package indexer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
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
	writeItemBatch(conn, collID, b, result)

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
	writeItemBatch(conn, collID, b, result)

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
	writeItemBatch(conn, collID, b, result)

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

// Re-indexing an item replaces it rather than accumulating duplicates.
func TestStoreItemReplacesExisting(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "rss", "system")

	item := makeItems(1, 2)[0]
	vecs := [][]float32{make([]float32, 1024), make([]float32, 1024)}

	for i := 0; i < 2; i++ {
		if err := storeItem(conn, collID, item, vecs); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}

	var sources, docs int
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ?", collID).Scan(&sources)
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", collID).Scan(&docs)
	if sources != 1 {
		t.Errorf("got %d sources after re-indexing, want 1", sources)
	}
	if docs != 2 {
		t.Errorf("got %d documents after re-indexing, want 2", docs)
	}
}
