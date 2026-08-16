package indexer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
)

// indexItem is one message-like thing to index: an RSS article, an email — any
// source whose unit of indexing is identified by a single path/ID and carries
// the same metadata across all of its chunks.
type indexItem struct {
	SourcePath string // article ID, message ID, …
	Title      string
	Chunks     []chunker.Chunk
	Metadata   map[string]any
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
}

// itemFunc returns the item at index i, or nil to skip it. Items are built
// lazily so only the batches currently in flight hold chunked text.
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
	sourceType string,
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
	slog.Info("embedding items", "type", sourceType, "count", total,
		"workers", workers, "batch_size", cfg.EmbeddingBatchSize)

	b := &itemBatcher{cfg: cfg, total: total, itemAt: itemAt}
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
			}(batch)
		}
		wg.Wait()

		for _, batch := range wave {
			writeItemBatch(conn, collectionID, sourceType, batch, result)

			done += len(batch.items)
			if progress != nil {
				progress(done, total, batch.items[len(batch.items)-1].Title)
			}
		}
	}
}

// itemBatcher walks items in order, grouping them into batches of roughly
// embedding_batch_size chunks. An item's chunks are never split across batches,
// so a batch may slightly exceed the target — writeItemBatch relies on each
// item's vectors being contiguous.
type itemBatcher struct {
	cfg    *config.Config
	total  int
	next   int
	itemAt itemFunc
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

		// An item with neither title nor body chunks to a single empty string;
		// embedding that wastes a slot and stores a blank document.
		if item == nil || !hasContent(item.Chunks) {
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
func writeItemBatch(conn *sql.DB, collectionID int64, sourceType string, b *itemBatch, result *IndexResult) {
	if b.err != nil {
		result.Errors += len(b.items)
		if result.Errors <= 10 {
			msg := fmt.Sprintf("error embedding batch of %d %s items: %v", len(b.items), sourceType, b.err)
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

	offset := 0
	for _, item := range b.items {
		vecs := b.vecs[offset : offset+len(item.Chunks)]
		offset += len(item.Chunks)

		if err := storeItem(conn, collectionID, sourceType, item, vecs); err != nil {
			result.Errors++
			if result.Errors <= 10 {
				msg := fmt.Sprintf("error indexing %s %s: %v", sourceType, item.SourcePath, err)
				slog.Warn(msg)
				result.ErrorMessages = append(result.ErrorMessages, msg)
			}
			continue
		}

		result.Indexed++
		slog.Debug("indexed item", "type", sourceType,
			"title", truncate(item.Title, 60), "chunks", len(item.Chunks))
	}
}

// storeItem writes one item's chunks and their embeddings. Embedding has
// already happened in batch, so vecs is parallel to item.Chunks.
func storeItem(conn *sql.DB, collectionID int64, sourceType string, item *indexItem, vecs [][]float32) error {
	metaJSON, _ := json.Marshal(item.Metadata)
	now := time.Now().UTC().Format(time.RFC3339)

	// Replace any previous version of this item.
	conn.Exec("DELETE FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, item.SourcePath)

	res, err := conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, ?, ?, ?)",
		collectionID, sourceType, item.SourcePath, now,
	)
	if err != nil {
		return fmt.Errorf("insert source: %w", err)
	}
	sourceID, _ := res.LastInsertId()

	for i, c := range item.Chunks {
		docRes, err := conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
			sourceID, collectionID, c.ChunkIndex, item.Title, c.Text, string(metaJSON),
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
