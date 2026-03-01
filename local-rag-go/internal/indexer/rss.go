package indexer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
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

	for _, article := range articles {
		result.TotalFound++
		if progress != nil {
			title := article.Title
			if title == "" {
				title = "(no title)"
			}
			progress(result.TotalFound, totalArticles, title)
		}

		// Advance watermark for all articles we've seen, not just indexed ones.
		if article.DatePublishedTS > latestTS {
			latestTS = article.DatePublishedTS
		}

		if !force && isSourceExists(conn, collectionID, article.ArticleID) {
			result.Skipped++
			continue
		}

		count, err := indexSingleArticle(conn, cfg, collectionID, article)
		if err != nil {
			result.Errors++
			if result.Errors <= 10 {
				msg := fmt.Sprintf("error indexing article %s: %v", article.ArticleID, err)
				slog.Warn(msg)
				result.ErrorMessages = append(result.ErrorMessages, msg)
			}
			continue
		}

		result.Indexed++
		slog.Info("indexed article", "title", truncate(article.Title, 60), "chunks", count)
	}

	return result, latestTS
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

func indexSingleArticle(conn *sql.DB, cfg *config.Config, collectionID int64, article *parser.Article) (int, error) {
	// Reuse the email chunker — RSS articles have a similar structure
	chunks := chunker.ChunkEmail(article.Title, article.BodyText, cfg.ChunkSizeTokens, cfg.ChunkOverlapTokens)
	if len(chunks) == 0 {
		return 0, nil
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	vecs, err := embed(texts, cfg)
	if err != nil {
		return 0, fmt.Errorf("embeddings: %w", err)
	}

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
		return 0, fmt.Errorf("insert source: %w", err)
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
			return 0, fmt.Errorf("insert document: %w", err)
		}
		docID, _ := docRes.LastInsertId()
		vecBytes := embeddings.SerializeFloat32(vecs[i])
		conn.Exec("INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)", vecBytes, docID)
	}

	return len(chunks), nil
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
