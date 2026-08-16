package indexer

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
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

	collectionID, err := getOrCreate(conn, "rss", "system")
	if err != nil {
		return failedResult(err)
	}

	var sinceTS float64
	if !force {
		sinceTS = getRSSWatermark(conn, collectionID)
		if sinceTS > 0 {
			slog.Info("incremental index: fetching articles since", "ts", sinceTS)
		}
	}

	latestTS := sinceTS

	// A rebuild replaces everything, so clear once instead of purging per batch.
	cleared := clearForRebuild(conn, collectionID, force)

	for _, accountDir := range accountDirs {
		slog.Info("indexing RSS account", "dir", filepath.Base(accountDir))
		acctResult, acctLatest := indexRSSAccount(conn, cfg, collectionID, accountDir, sinceTS, force, progress, cleared)
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

func indexRSSAccount(conn *sql.DB, cfg *config.Config, collectionID int64, accountDir string, sinceTS float64, force bool, progress ProgressCallback, cleared bool) (*IndexResult, float64) {
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
	indexItemsBatched(conn, cfg, collectionID, "rss", len(todo),
		func(i int) *indexItem { return articleToItem(todo[i], cfg) },
		result, progress, cleared)

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

// articleToItem chunks an article and collects the metadata stored with every
// one of its chunks.
func articleToItem(article *parser.Article, cfg *config.Config) *indexItem {
	title := article.Title
	if title == "" {
		title = "(no title)"
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

	return &indexItem{
		SourcePath: article.ArticleID,
		Title:      title,
		// Reuse the email chunker — RSS articles have a similar structure.
		Chunks: chunker.ChunkEmail(article.Title, article.BodyText,
			cfg.ChunkSizeTokens, cfg.ChunkOverlapTokens),
		Metadata: metadata,
	}
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
