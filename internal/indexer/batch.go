package indexer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
)

// indexItem is one thing to index — a file, an RSS article, an email, a commit
// — reduced to what storage needs: an identity, its chunks, and the metadata to
// record alongside them.
type indexItem struct {
	SourcePath string // absolute file path, article ID, message ID, git://…
	SourceType string // markdown, pdf, code, rss, email, commit, …
	Title      string
	Chunks     []chunker.Chunk

	// Metadata is shared by every chunk of this item. Chunks may also carry
	// their own (page number, symbol path, …), which takes precedence.
	Metadata map[string]any

	// FileHash and Mtime drive incremental indexing for file-backed sources;
	// both are empty for sources identified by ID rather than content.
	FileHash string
	Mtime    string
}

// itemBatch is a group of items whose chunks are embedded in one request.
//
// Message-like sources yield only a handful of chunks per item, so embedding
// one item at a time spends a network round trip per item — the dominant cost
// when Ollama runs on another host, and the reason a full reindex crawls.
// Grouping items until the batch reaches embedding_batch_size is what makes
// that setting mean anything for these sources.
type itemBatch struct {
	items []*indexItem
	texts []string    // all chunk texts, flattened in item then chunk order
	vecs  [][]float32 // filled in by the embedding worker
	err   error

	// dropped counts items removed by the individual-retry fallback. They are
	// no longer in items, but still have to be reported and counted.
	dropped int
}

// itemFunc returns the item at index i, or nil to skip it — unchanged since the
// last run, or nothing extractable. Items are built lazily so only the batches
// currently in flight hold chunked text, and so expensive work (PDF text
// extraction, OCR, tree-sitter parsing) is never done for an item that will be
// skipped anyway.
type itemFunc func(i int) *indexItem

// indexItemsBatched chunks, embeds and stores a run of items.
//
// Embedding requests for several batches are in flight at once — a single
// request leaves a remote GPU idle between round trips — while every database
// write happens on this goroutine, since SQLite takes no concurrent writers.
func indexItemsBatched(
	conn *sql.DB,
	cfg *config.Config,
	collectionID int64,
	label string,
	total int,
	itemAt itemFunc,
	result *IndexResult,
	progress ProgressCallback,
) {
	if total == 0 {
		return
	}

	workers := cfg.EmbeddingWorkers
	if workers < 1 {
		workers = 1
	}
	slog.Info("embedding items", "source", label, "count", total,
		"workers", workers, "batch_size", cfg.EmbeddingBatchSize)

	b := &itemBatcher{cfg: cfg, total: total, itemAt: itemAt}
	defer func() { result.Skipped += b.skipped }()
	done := 0

	for {
		// Fill a wave: at most one batch per worker, so memory stays bounded to
		// workers × batch_size chunks regardless of how many items there are.
		wave := make([]*itemBatch, 0, workers)
		for len(wave) < workers {
			next := b.nextBatch()
			if next == nil {
				break
			}
			wave = append(wave, next)
		}
		if len(wave) == 0 {
			return
		}

		var wg sync.WaitGroup
		for _, batch := range wave {
			wg.Add(1)
			go func(batch *itemBatch) {
				defer wg.Done()
				batch.vecs, batch.err = embed(batch.texts, cfg)
				if batch.err != nil {
					embedItemsIndividually(batch, cfg)
				}
			}(batch)
		}
		wg.Wait()

		for _, batch := range wave {
			writeItemBatch(conn, collectionID, batch, result)

			done += len(batch.items) + batch.dropped
			if progress != nil && len(batch.items) > 0 {
				progress(done, total, batch.items[len(batch.items)-1].Title)
			}
		}
	}
}

// embedItemsIndividually retries a failed batch one item at a time, so a single
// unembeddable item costs only itself.
//
// Batching means one rejected input fails the whole request — Ollama refuses an
// input longer than the physical batch ("input (N tokens) is too large to
// process"), and a single oversized chunk would otherwise discard every other
// item that happened to travel with it. Items that succeed on retry keep their
// embeddings; the ones that genuinely fail are marked so writeItemBatch reports
// them individually.
func embedItemsIndividually(b *itemBatch, cfg *config.Config) {
	slog.Warn("embedding batch failed, retrying items individually",
		"items", len(b.items), "err", b.err)

	kept := make([]*indexItem, 0, len(b.items))
	texts := make([]string, 0, len(b.texts))
	vecs := make([][]float32, 0, len(b.texts))
	var failed int

	for _, item := range b.items {
		itemTexts := make([]string, len(item.Chunks))
		for i, c := range item.Chunks {
			itemTexts[i] = c.Text
		}

		itemVecs, err := embed(itemTexts, cfg)
		if err != nil || len(itemVecs) != len(itemTexts) {
			failed++
			if failed <= 5 {
				slog.Warn("skipping item that cannot be embedded",
					"type", item.SourceType, "path", item.SourcePath,
					"chunks", len(item.Chunks), "err", err)
			}
			continue
		}

		kept = append(kept, item)
		texts = append(texts, itemTexts...)
		vecs = append(vecs, itemVecs...)
	}

	if failed > 0 {
		slog.Warn("items dropped from batch", "failed", failed, "recovered", len(kept))
	}

	// Rebuild the batch around only what embedded successfully, so the offsets
	// writeItemBatch relies on still line up.
	b.items, b.texts, b.vecs, b.err = kept, texts, vecs, nil
	b.dropped = failed
}

// itemBatcher walks items in order, grouping them into batches of roughly
// embedding_batch_size chunks. An item's chunks are never split across batches,
// so a batch may slightly exceed the target — writeItemBatch relies on each
// item's vectors being contiguous.
type itemBatcher struct {
	cfg     *config.Config
	total   int
	next    int
	itemAt  itemFunc
	skipped int // items the itemFunc declined, or that had no usable text
}

// nextBatch returns the next batch, or nil once the items are exhausted.
func (b *itemBatcher) nextBatch() *itemBatch {
	target := b.cfg.EmbeddingBatchSize
	if target < 1 {
		target = 1
	}

	batch := &itemBatch{}
	for b.next < b.total {
		item := b.itemAt(b.next)
		b.next++

		// Either the itemFunc declined it (unchanged, unreadable), or it chunked
		// to nothing but blank text — an item with neither title nor body
		// produces a single empty string, and embedding that would waste a slot
		// and store a blank document.
		if item == nil || !hasContent(item.Chunks) {
			b.skipped++
			continue
		}

		batch.items = append(batch.items, item)
		for _, c := range item.Chunks {
			batch.texts = append(batch.texts, c.Text)
		}

		if len(batch.texts) >= target {
			return batch
		}
	}

	if len(batch.items) > 0 {
		return batch
	}
	return nil
}

// hasContent reports whether any chunk carries non-blank text.
func hasContent(chunks []chunker.Chunk) bool {
	for _, c := range chunks {
		if strings.TrimSpace(c.Text) != "" {
			return true
		}
	}
	return false
}

// writeItemBatch stores an embedded batch, attributing failures per item so one
// bad item does not sink the rest.
func writeItemBatch(conn *sql.DB, collectionID int64, b *itemBatch, result *IndexResult) {
	// Items the individual retry could not embed are gone from b.items but still
	// have to be counted.
	if b.dropped > 0 {
		result.Errors += b.dropped
		msg := fmt.Sprintf("%d item(s) could not be embedded and were skipped", b.dropped)
		result.ErrorMessages = append(result.ErrorMessages, msg)
	}

	if b.err != nil {
		result.Errors += len(b.items)
		if result.Errors <= 10 {
			msg := fmt.Sprintf("error embedding batch of %d items: %v", len(b.items), b.err)
			slog.Warn(msg)
			result.ErrorMessages = append(result.ErrorMessages, msg)
		}
		return
	}

	// Offsets into b.vecs are only meaningful if Ollama returned one vector per
	// text; storing them otherwise would attach the wrong embedding to a chunk.
	if len(b.vecs) != len(b.texts) {
		msg := fmt.Sprintf("embedding count mismatch: got %d vectors for %d chunks", len(b.vecs), len(b.texts))
		slog.Error(msg)
		result.Errors += len(b.items)
		result.ErrorMessages = append(result.ErrorMessages, msg)
		return
	}

	// Clear out the previous version of everything in this batch in one pass,
	// before storing any of it.
	purgeSourceDocuments(conn, collectionID, b.items)

	offset := 0
	for _, item := range b.items {
		vecs := b.vecs[offset : offset+len(item.Chunks)]
		offset += len(item.Chunks)

		if err := storeItem(conn, collectionID, item, vecs); err != nil {
			result.Errors++
			if result.Errors <= 10 {
				msg := fmt.Sprintf("error indexing %s %s: %v", item.SourceType, item.SourcePath, err)
				slog.Warn(msg)
				result.ErrorMessages = append(result.ErrorMessages, msg)
			}
			continue
		}

		result.Indexed++
		slog.Debug("indexed item", "type", item.SourceType,
			"title", truncate(item.Title, 60), "chunks", len(item.Chunks))
	}
}

// sqlParamLimit bounds how many bind parameters go into one statement. SQLite's
// default ceiling is higher, but staying well under it keeps the generated SQL
// small and predictable.
const sqlParamLimit = 500

// purgeSourceDocuments removes the existing documents and embeddings for every
// item in a batch, in as few statements as possible.
//
// This has to be batched. vec_documents is a vec0 virtual table whose
// document_id is not indexed, so every DELETE ... WHERE document_id IN (…)
// scans the entire vector table. Doing that once per item made a force reindex
// cost ~170ms per item against 100k vectors — over a second against the ~800k
// here — which is hours of full scans for a corpus this size. Hoisting it to
// once per batch cuts the number of scans by the batch size.
func purgeSourceDocuments(conn *sql.DB, collectionID int64, items []*indexItem) {
	if len(items) == 0 {
		return
	}

	var docIDs []any
	for start := 0; start < len(items); start += sqlParamLimit {
		end := start + sqlParamLimit
		if end > len(items) {
			end = len(items)
		}

		args := make([]any, 0, len(items[start:end])+1)
		args = append(args, collectionID)
		for _, item := range items[start:end] {
			args = append(args, item.SourcePath)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")

		rows, err := conn.Query(`
			SELECT d.id FROM documents d
			JOIN sources s ON d.source_id = s.id
			WHERE s.collection_id = ? AND s.source_path IN (`+placeholders+`)`, args...)
		if err != nil {
			slog.Warn("cannot list documents to purge", "err", err)
			return
		}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				docIDs = append(docIDs, id)
			}
		}
		rows.Close()
	}

	if len(docIDs) == 0 {
		return
	}

	for start := 0; start < len(docIDs); start += sqlParamLimit {
		end := start + sqlParamLimit
		if end > len(docIDs) {
			end = len(docIDs)
		}
		chunk := docIDs[start:end]

		if err := db.DeleteEmbeddings(conn, chunk); err != nil {
			slog.Warn("cannot delete embeddings", "err", err)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		if _, err := conn.Exec("DELETE FROM documents WHERE id IN ("+placeholders+")", chunk...); err != nil {
			slog.Warn("cannot delete documents", "err", err)
		}
	}
}

// storeItem writes one item's chunks and their embeddings. Embedding has
// already happened in batch, so vecs is parallel to item.Chunks.
//
// upsertSource is used rather than a plain DELETE + INSERT because it also
// clears the old rows out of vec_documents. Deleting a source cascades to its
// documents, but the vector tables are vec0 virtual tables with no foreign
// keys, so a cascade alone would strand their embeddings.
func storeItem(conn *sql.DB, collectionID int64, item *indexItem, vecs [][]float32) error {
	// purged=true: writeItemBatch already removed the old documents and vectors
	// for the whole batch.
	sourceID, err := upsertSourceRow(conn, collectionID, item.SourcePath, item.SourceType,
		item.FileHash, item.Mtime, true)
	if err != nil {
		return err
	}

	for i, c := range item.Chunks {
		metaJSON, err := chunkMetadata(item, c)
		if err != nil {
			return err
		}

		title := c.Title
		if item.Title != "" {
			title = item.Title
		}

		docRes, err := conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
			sourceID, collectionID, c.ChunkIndex, title, c.Text, metaJSON,
		)
		if err != nil {
			return fmt.Errorf("insert document: %w", err)
		}
		docID, _ := docRes.LastInsertId()
		if err := db.InsertEmbedding(conn, docID, embeddings.SerializeFloat32(vecs[i])); err != nil {
			return fmt.Errorf("insert embedding: %w", err)
		}
	}

	return nil
}

// chunkMetadata merges the item-wide metadata with the chunk's own. Chunk keys
// win: a page number or symbol path describes that chunk specifically, while
// the item's metadata (sender, feed, book author) describes the whole source.
func chunkMetadata(item *indexItem, c chunker.Chunk) (string, error) {
	if len(item.Metadata) == 0 && len(c.Metadata) == 0 {
		return "", nil
	}

	merged := make(map[string]any, len(item.Metadata)+len(c.Metadata))
	for k, v := range item.Metadata {
		merged[k] = v
	}
	for k, v := range c.Metadata {
		merged[k] = v
	}

	b, err := json.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}
	return string(b), nil
}
