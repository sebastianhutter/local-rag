package indexer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

// IndexRSS indexes NetNewsWire RSS articles into the "rss" collection.
func IndexRSS(conn *sql.DB, cfg *config.Config, force bool, progress ProgressCallback) *IndexResult {
	result := &IndexResult{}

	basePath := expandPath(cfg.NetnewswireDBPath)

	var accountDirs []string
	if fileExists(filepath.Join(basePath, "DB.sqlite3")) {
		accountDirs = []string{basePath}
	} else {
		accountDirs = parser.FindRSSAccountDirs(basePath)
	}

	if len(accountDirs) == 0 {
		msg := fmt.Sprintf("no NetNewsWire databases found at %s", basePath)
		slog.Error(msg)
		result.Errors = 1
		result.ErrorMessages = append(result.ErrorMessages, msg)
		return result
	}

	slog.Info("found NetNewsWire accounts", "count", len(accountDirs))

	collectionID := getOrCreate(conn, "rss", "system")

	var sinceTS float64
	if !force {
		sinceTS = getRSSWatermark(conn, collectionID)
		if sinceTS > 0 {
			slog.Info("incremental index: fetching articles since", "ts", sinceTS)
		}
	}

	latestTS := sinceTS

	for _, accountDir := range accountDirs {
		slog.Info("indexing RSS account", "dir", filepath.Base(accountDir))
		acctResult, acctLatest := indexRSSAccount(conn, cfg, collectionID, accountDir, sinceTS, force, progress)
		result.Merge(acctResult)
		if acctLatest > latestTS {
			latestTS = acctLatest
		}
	}

	if latestTS > 0 {
		setRSSWatermark(conn, collectionID, latestTS)
	}

	slog.Info("RSS indexing complete", "result", result.String())
	return result
}

func indexRSSAccount(conn *sql.DB, cfg *config.Config, collectionID int64, accountDir string, sinceTS float64, force bool, progress ProgressCallback) (*IndexResult, float64) {
	result := &IndexResult{}
	latestTS := 0.0

	articles, parseErr := parseRSSWithRetry(accountDir, sinceTS)
	if parseErr != nil {
		msg := fmt.Sprintf("failed to read NetNewsWire database in %s: %v", filepath.Base(accountDir), parseErr)
		slog.Error(msg)
		result.Errors = 1
		result.ErrorMessages = append(result.ErrorMessages, msg)
		return result, latestTS
	}

	totalArticles := len(articles)
	slog.Info("found articles to process", "count", totalArticles, "account", filepath.Base(accountDir))

	// Pass 1 — decide what needs indexing. Cheap: no parsing, no network.
	todo := make([]*parser.Article, 0, len(articles))
	for _, article := range articles {
		result.TotalFound++

		// Advance watermark for all articles we've seen, not just indexed ones.
		if article.DatePublishedTS > latestTS {
			latestTS = article.DatePublishedTS
		}

		if !force && isSourceExists(conn, collectionID, article.ArticleID) {
			result.Skipped++
			continue
		}
		todo = append(todo, article)
	}

	if len(todo) == 0 {
		return result, latestTS
	}
	slog.Info("articles to index", "count", len(todo), "account", filepath.Base(accountDir))

	// Pass 2 — chunk, embed in batches, write.
	indexRSSArticles(conn, cfg, collectionID, todo, result, progress)

	return result, latestTS
}

// rssBatch groups several articles so their chunks travel to Ollama in a single
// request. An RSS article yields only a handful of chunks, so embedding one
// article at a time spends a network round trip per article — the dominant cost
// when Ollama runs on another host, and the reason a full reindex crawls.
type rssBatch struct {
	articles []*parser.Article
	chunks   [][]chunker.Chunk // per article, parallel to articles
	texts    []string          // all chunk texts, flattened
	vecs     [][]float32       // filled in by the embedding worker
	err      error
}

// indexRSSArticles chunks, embeds and writes a set of articles.
//
// Embedding requests for several batches run concurrently — a single request
// leaves a GPU host idle between round trips — while all database writes happen
// on this goroutine, since SQLite does not take concurrent writers.
func indexRSSArticles(
	conn *sql.DB,
	cfg *config.Config,
	collectionID int64,
	articles []*parser.Article,
	result *IndexResult,
	progress ProgressCallback,
) {
	batches := buildRSSBatches(articles, cfg)
	if len(batches) == 0 {
		return
	}

	workers := cfg.EmbeddingWorkers
	if workers < 1 {
		workers = 1
	}
	slog.Info("embedding articles", "batches", len(batches), "workers", workers,
		"batch_size", cfg.EmbeddingBatchSize)

	done := 0
	for start := 0; start < len(batches); start += workers {
		end := start + workers
		if end > len(batches) {
			end = len(batches)
		}
		wave := batches[start:end]

		var wg sync.WaitGroup
		for _, b := range wave {
			wg.Add(1)
			go func(b *rssBatch) {
				defer wg.Done()
				b.vecs, b.err = embed(b.texts, cfg)
			}(b)
		}
		wg.Wait()

		for _, b := range wave {
			writeRSSBatch(conn, collectionID, b, result)

			done += len(b.articles)
			if progress != nil {
				title := b.articles[len(b.articles)-1].Title
				if title == "" {
					title = "(no title)"
				}
				progress(done, len(articles), title)
			}
		}
	}
}

// buildRSSBatches chunks each article and groups them so that each batch holds
// roughly embedding_batch_size chunks. An article's chunks are never split
// across batches, so a batch may slightly exceed the target.
func buildRSSBatches(articles []*parser.Article, cfg *config.Config) []*rssBatch {
	target := cfg.EmbeddingBatchSize
	if target < 1 {
		target = 1
	}

	var batches []*rssBatch
	cur := &rssBatch{}

	for _, article := range articles {
		// Reuse the email chunker — RSS articles have a similar structure.
		chunks := chunker.ChunkEmail(article.Title, article.BodyText,
			cfg.ChunkSizeTokens, cfg.ChunkOverlapTokens)
		if !hasContent(chunks) {
			// An article with neither title nor body chunks to a single empty
			// string; embedding that wastes a slot and stores a blank document.
			continue
		}

		cur.articles = append(cur.articles, article)
		cur.chunks = append(cur.chunks, chunks)
		for _, c := range chunks {
			cur.texts = append(cur.texts, c.Text)
		}

		if len(cur.texts) >= target {
			batches = append(batches, cur)
			cur = &rssBatch{}
		}
	}

	if len(cur.articles) > 0 {
		batches = append(batches, cur)
	}
	return batches
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

// writeRSSBatch stores an embedded batch, attributing failures per article so
// one bad article does not sink the rest.
func writeRSSBatch(conn *sql.DB, collectionID int64, b *rssBatch, result *IndexResult) {
	if b.err != nil {
		for range b.articles {
			result.Errors++
		}
		if result.Errors <= 10 {
			msg := fmt.Sprintf("error embedding batch of %d articles: %v", len(b.articles), b.err)
			slog.Warn(msg)
			result.ErrorMessages = append(result.ErrorMessages, msg)
		}
		return
	}

	if len(b.vecs) != len(b.texts) {
		msg := fmt.Sprintf("embedding count mismatch: got %d vectors for %d chunks", len(b.vecs), len(b.texts))
		slog.Error(msg)
		result.Errors += len(b.articles)
		result.ErrorMessages = append(result.ErrorMessages, msg)
		return
	}

	offset := 0
	for i, article := range b.articles {
		chunks := b.chunks[i]
		vecs := b.vecs[offset : offset+len(chunks)]
		offset += len(chunks)

		if err := storeArticle(conn, collectionID, article, chunks, vecs); err != nil {
			result.Errors++
			if result.Errors <= 10 {
				msg := fmt.Sprintf("error indexing article %s: %v", article.ArticleID, err)
				slog.Warn(msg)
				result.ErrorMessages = append(result.ErrorMessages, msg)
			}
			continue
		}

		result.Indexed++
		slog.Debug("indexed article", "title", truncate(article.Title, 60), "chunks", len(chunks))
	}
}

func parseRSSWithRetry(accountDir string, sinceTS float64) ([]*parser.Article, error) {
	for attempt := 1; attempt <= maxLockRetries; attempt++ {
		articles, err := parser.ParseArticles(accountDir, sinceTS)
		if err == nil {
			return articles, nil
		}
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "locked") || strings.Contains(errStr, "busy") {
			if attempt < maxLockRetries {
				slog.Warn("NetNewsWire database is locked, retrying", "attempt", attempt, "max", maxLockRetries)
				time.Sleep(lockRetryDelay)
				continue
			}
			return nil, fmt.Errorf("database locked after %d retries", maxLockRetries)
		}
		return nil, err
	}
	return nil, fmt.Errorf("exhausted retries")
}

// storeArticle writes one article's chunks and their embeddings. Embedding has
// already happened in batch, so vecs is parallel to chunks.
func storeArticle(
	conn *sql.DB,
	collectionID int64,
	article *parser.Article,
	chunks []chunker.Chunk,
	vecs [][]float32,
) error {
	metadata := map[string]any{
		"url":       article.URL,
		"feed_name": article.FeedName,
		"date":      article.DatePublished,
	}
	if article.FeedCategory != "" {
		metadata["feed_category"] = article.FeedCategory
	}
	if len(article.Authors) > 0 {
		metadata["authors"] = article.Authors
	}
	metaJSON, _ := json.Marshal(metadata)

	now := time.Now().UTC().Format(time.RFC3339)

	conn.Exec("DELETE FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, article.ArticleID)

	res, err := conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'rss', ?, ?)",
		collectionID, article.ArticleID, now,
	)
	if err != nil {
		return fmt.Errorf("insert source: %w", err)
	}
	sourceID, _ := res.LastInsertId()

	for i, c := range chunks {
		title := article.Title
		if title == "" {
			title = "(no title)"
		}
		docRes, err := conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
			sourceID, collectionID, c.ChunkIndex, title, c.Text, string(metaJSON),
		)
		if err != nil {
			return fmt.Errorf("insert document: %w", err)
		}
		docID, _ := docRes.LastInsertId()
		vecBytes := embeddings.SerializeFloat32(vecs[i])
		_ = db.InsertEmbedding(conn, docID, vecBytes)
	}

	return nil
}

func getRSSWatermark(conn *sql.DB, collectionID int64) float64 {
	var latest sql.NullString
	conn.QueryRow(
		"SELECT MAX(json_extract(d.metadata, '$.date')) FROM documents d WHERE d.collection_id = ?",
		collectionID,
	).Scan(&latest)
	if latest.Valid && latest.String != "" {
		t, err := time.Parse(time.RFC3339, latest.String)
		if err == nil {
			return float64(t.Unix())
		}
	}
	return 0
}

func setRSSWatermark(conn *sql.DB, collectionID int64, ts float64) {
	t := time.Unix(int64(ts), 0).UTC()
	dateStr := t.Format(time.RFC3339)
	conn.Exec("UPDATE collections SET description = ? WHERE id = ?",
		fmt.Sprintf("RSS articles from NetNewsWire (indexed through %s)", dateStr), collectionID)
}
