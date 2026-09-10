package indexer

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

const (
	maxLockRetries = 3
	lockRetryDelay = 2 * time.Second
)

// IndexEmails indexes eM Client emails into the "email" collection.
func IndexEmails(conn *sql.DB, cfg *config.Config, force bool, progress ProgressCallback) *IndexResult {
	result := &IndexResult{}

	basePath := expandPath(cfg.EmclientDBPath)

	// Check if base path is itself an account dir
	var accountDirs []string
	if fileExists(filepath.Join(basePath, "mail_index.dat")) {
		accountDirs = []string{basePath}
	} else {
		accountDirs = parser.FindEmailAccountDirs(basePath)
	}

	if len(accountDirs) == 0 {
		msg := fmt.Sprintf("no eM Client mail databases found at %s", basePath)
		slog.Error(msg)
		result.Errors = 1
		result.ErrorMessages = append(result.ErrorMessages, msg)
		return result
	}

	slog.Info("found eM Client accounts", "count", len(accountDirs))

	collectionID, err := getOrCreate(conn, "email", "system")
	if err != nil {
		return failedResult(err)
	}

	// Determine watermark for incremental indexing
	var sinceDate string
	if !force {
		sinceDate = getEmailWatermark(conn, collectionID)
		if sinceDate != "" {
			slog.Info("incremental index: fetching emails since", "date", sinceDate)
		}
	}

	latestDate := sinceDate

	// A rebuild replaces everything, so clear once instead of purging per batch.
	cleared := clearForRebuild(conn, collectionID, force)

	for _, accountDir := range accountDirs {
		slog.Info("indexing email account", "dir", filepath.Base(accountDir))
		acctResult, acctLatest := indexEmailAccount(conn, cfg, collectionID, accountDir, sinceDate, force, progress, cleared)
		result.Merge(acctResult)
		if acctLatest > latestDate {
			latestDate = acctLatest
		}
	}

	if latestDate != "" {
		setEmailWatermark(conn, collectionID, latestDate)
	}

	slog.Info("email indexing complete", "result", result.String())
	return result
}

func indexEmailAccount(conn *sql.DB, cfg *config.Config, collectionID int64, accountDir, sinceDate string, force bool, progress ProgressCallback, cleared bool) (*IndexResult, string) {
	result := &IndexResult{}
	latestDate := ""

	emails, parseErr := parseEmailsWithRetry(accountDir, sinceDate)
	if parseErr != nil {
		msg := fmt.Sprintf("failed to read eM Client database in %s: %v", filepath.Base(accountDir), parseErr)
		slog.Error(msg)
		result.Errors = 1
		result.ErrorMessages = append(result.ErrorMessages, msg)
		return result, latestDate
	}

	totalEmails := len(emails)
	slog.Info("found emails to process", "count", totalEmails, "account", filepath.Base(accountDir))

	// Pass 1 — decide what needs indexing. Cheap: no chunking, no network.
	now := nowISO()
	todo := make([]*parser.EmailMessage, 0, len(emails))
	for _, email := range emails {
		result.TotalFound++

		// Advance watermark for all emails we've seen, not just indexed ones.
		// A scheduled message dated in the future is indexed but must not move
		// the watermark past now, or mail arriving before its send date would
		// fall below the watermark and never be picked up.
		if email.Date > latestDate && email.Date <= now {
			latestDate = email.Date
		}

		if !force && isSourceExists(conn, collectionID, email.MessageID) {
			result.Skipped++
			continue
		}
		todo = append(todo, email)
	}

	if len(todo) == 0 {
		return result, latestDate
	}
	slog.Info("emails to index", "count", len(todo), "account", filepath.Base(accountDir))

	// Pass 2 — chunk, embed in batches, write.
	indexItemsBatched(conn, cfg, collectionID, "email", len(todo),
		func(i int) *indexItem { return emailToItem(todo[i], cfg) },
		result, progress, cleared)

	return result, latestDate
}

// emailToItem chunks an email and collects the metadata stored with every one
// of its chunks.
func emailToItem(email *parser.EmailMessage, cfg *config.Config) *indexItem {
	title := email.Subject
	if title == "" {
		title = "(no subject)"
	}

	return &indexItem{
		SourcePath: email.MessageID,
		SourceType: "email",
		Title:      title,
		Chunks: chunker.ChunkEmail(email.Subject, email.BodyText,
			cfg.ChunkSizeTokens, cfg.ChunkOverlapTokens),
		Metadata: map[string]any{
			"sender":     email.Sender,
			"recipients": email.Recipients,
			"date":       email.Date,
			"folder":     email.Folder,
		},
	}
}

func parseEmailsWithRetry(accountDir, sinceDate string) ([]*parser.EmailMessage, error) {
	for attempt := 1; attempt <= maxLockRetries; attempt++ {
		emails, err := parser.ParseEmails(accountDir, sinceDate)
		if err == nil {
			return emails, nil
		}
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "locked") || strings.Contains(errStr, "busy") {
			if attempt < maxLockRetries {
				slog.Warn("eM Client database is locked, retrying", "attempt", attempt, "max", maxLockRetries)
				time.Sleep(lockRetryDelay)
				continue
			}
			return nil, fmt.Errorf("database locked after %d retries", maxLockRetries)
		}
		return nil, err
	}
	return nil, fmt.Errorf("exhausted retries")
}

// getEmailWatermark returns the newest indexed message date, ignoring dates in
// the future. A scheduled message is stored with its send date, so without the
// cut-off one queued mail would push the watermark days ahead and every message
// arriving before then would be filtered out by ParseEmails and never indexed.
func getEmailWatermark(conn *sql.DB, collectionID int64) string {
	var latest sql.NullString
	conn.QueryRow(
		"SELECT MAX(json_extract(d.metadata, '$.date')) FROM documents d "+
			"WHERE d.collection_id = ? AND json_extract(d.metadata, '$.date') <= ?",
		collectionID, nowISO(),
	).Scan(&latest)
	if latest.Valid {
		return latest.String
	}
	return ""
}

// nowISO renders the current time the way parser.ParseEmails renders a message
// date (RFC3339, UTC), so the two compare correctly as strings.
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func setEmailWatermark(conn *sql.DB, collectionID int64, date string) {
	conn.Exec("UPDATE collections SET description = ? WHERE id = ?",
		fmt.Sprintf("Emails from eM Client (indexed through %s)", date), collectionID)
}

func isSourceExists(conn *sql.DB, collectionID int64, sourcePath string) bool {
	var id int64
	err := conn.QueryRow(
		"SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, sourcePath,
	).Scan(&id)
	return err == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
