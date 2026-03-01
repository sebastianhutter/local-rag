package indexer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
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

	collectionID := getOrCreate(conn, "email", "system")

	// Determine watermark for incremental indexing
	var sinceDate string
	if !force {
		sinceDate = getEmailWatermark(conn, collectionID)
		if sinceDate != "" {
			slog.Info("incremental index: fetching emails since", "date", sinceDate)
		}
	}

	latestDate := sinceDate

	for _, accountDir := range accountDirs {
		slog.Info("indexing email account", "dir", filepath.Base(accountDir))
		acctResult, acctLatest := indexEmailAccount(conn, cfg, collectionID, accountDir, sinceDate, force, progress)
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

func indexEmailAccount(conn *sql.DB, cfg *config.Config, collectionID int64, accountDir, sinceDate string, force bool, progress ProgressCallback) (*IndexResult, string) {
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

	for _, email := range emails {
		result.TotalFound++
		if progress != nil {
			subj := email.Subject
			if subj == "" {
				subj = "(no subject)"
			}
			progress(result.TotalFound, totalEmails, subj)
		}

		// Advance watermark for all emails we've seen, not just indexed ones.
		if email.Date > latestDate {
			latestDate = email.Date
		}

		if !force && isSourceExists(conn, collectionID, email.MessageID) {
			result.Skipped++
			continue
		}

		count, err := indexSingleEmail(conn, cfg, collectionID, email)
		if err != nil {
			result.Errors++
			if result.Errors <= 10 {
				msg := fmt.Sprintf("error indexing email %s: %v", email.MessageID, err)
				slog.Warn(msg)
				result.ErrorMessages = append(result.ErrorMessages, msg)
			}
			continue
		}

		result.Indexed++
		slog.Info("indexed email", "subject", truncate(email.Subject, 60), "chunks", count)
	}

	return result, latestDate
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

func indexSingleEmail(conn *sql.DB, cfg *config.Config, collectionID int64, email *parser.EmailMessage) (int, error) {
	chunks := chunker.ChunkEmail(email.Subject, email.BodyText, cfg.ChunkSizeTokens, cfg.ChunkOverlapTokens)
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
		"sender":     email.Sender,
		"recipients": email.Recipients,
		"date":       email.Date,
		"folder":     email.Folder,
	}
	metaJSON, _ := json.Marshal(metadata)

	now := time.Now().UTC().Format(time.RFC3339)

	// Delete existing source if re-indexing
	conn.Exec("DELETE FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, email.MessageID)

	res, err := conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'email', ?, ?)",
		collectionID, email.MessageID, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert source: %w", err)
	}
	sourceID, _ := res.LastInsertId()

	for i, c := range chunks {
		title := email.Subject
		if title == "" {
			title = "(no subject)"
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

func getEmailWatermark(conn *sql.DB, collectionID int64) string {
	var latest sql.NullString
	conn.QueryRow(
		"SELECT MAX(json_extract(d.metadata, '$.date')) FROM documents d WHERE d.collection_id = ?",
		collectionID,
	).Scan(&latest)
	if latest.Valid {
		return latest.String
	}
	return ""
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
