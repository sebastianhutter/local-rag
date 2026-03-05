package indexer

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

// PruneAll prunes stale sources from all collections.
func PruneAll(conn *sql.DB, cfg *config.Config) *PruneResult {
	result := &PruneResult{}

	rows, err := conn.Query("SELECT id, name, collection_type FROM collections")
	if err != nil {
		slog.Error("prune: failed to list collections", "err", err)
		result.Errors++
		result.ErrorMessages = append(result.ErrorMessages, err.Error())
		return result
	}
	defer rows.Close()

	type collInfo struct {
		id     int64
		name   string
		ctype  string
	}
	var collections []collInfo
	for rows.Next() {
		var c collInfo
		if rows.Scan(&c.id, &c.name, &c.ctype) == nil {
			collections = append(collections, c)
		}
	}

	for _, c := range collections {
		r := pruneCollectionByType(conn, cfg, c.id, c.name, c.ctype)
		result.Merge(r)
	}

	if result.Pruned > 0 {
		slog.Info("prune all complete", "result", result.String())
	}
	return result
}

// PruneCollection prunes stale sources from a single named collection.
func PruneCollection(conn *sql.DB, cfg *config.Config, collectionName string) *PruneResult {
	var id int64
	var ctype string
	err := conn.QueryRow(
		"SELECT id, collection_type FROM collections WHERE name = ?", collectionName,
	).Scan(&id, &ctype)
	if err != nil {
		return &PruneResult{} // collection doesn't exist yet, nothing to prune
	}

	r := pruneCollectionByType(conn, cfg, id, collectionName, ctype)
	if r.Pruned > 0 {
		slog.Info("prune complete", "collection", collectionName, "result", r.String())
	}
	return r
}

func pruneCollectionByType(conn *sql.DB, cfg *config.Config, collectionID int64, name, ctype string) *PruneResult {
	switch name {
	case "obsidian":
		return pruneFileSources(conn, collectionID)
	case "email":
		return pruneEmailSources(conn, cfg, collectionID)
	case "rss":
		return pruneRSSSources(conn, cfg, collectionID)
	case "calibre":
		return pruneCalibreSources(conn, cfg, collectionID)
	default:
		switch ctype {
		case "project":
			return pruneFileSources(conn, collectionID)
		case "code":
			return pruneCodeSources(conn, cfg, collectionID)
		default:
			return &PruneResult{}
		}
	}
}

// pruneFileSources removes sources whose file paths no longer exist on disk.
// Skips sources with URI-style paths (calibre://, git://).
func pruneFileSources(conn *sql.DB, collectionID int64) *PruneResult {
	result := &PruneResult{}

	sources, err := sourcesForCollection(conn, collectionID)
	if err != nil {
		result.Errors++
		result.ErrorMessages = append(result.ErrorMessages, err.Error())
		return result
	}

	for _, s := range sources {
		// Skip URI-style sources
		if strings.Contains(s.SourcePath, "://") {
			continue
		}
		result.Checked++
		if _, err := os.Stat(s.SourcePath); os.IsNotExist(err) {
			slog.Info("pruning stale source", "path", s.SourcePath)
			deleteSourceByID(conn, s.ID)
			result.Pruned++
		}
	}

	return result
}

// pruneEmailSources removes indexed emails that no longer exist in eM Client.
func pruneEmailSources(conn *sql.DB, cfg *config.Config, collectionID int64) *PruneResult {
	result := &PruneResult{}

	basePath := expandPath(cfg.EmclientDBPath)

	var accountDirs []string
	if fileExists(filepath.Join(basePath, "mail_index.dat")) {
		accountDirs = []string{basePath}
	} else {
		accountDirs = parser.FindEmailAccountDirs(basePath)
	}

	if len(accountDirs) == 0 {
		slog.Warn("prune email: no eM Client accounts found, skipping prune")
		return result
	}

	// Collect all current message IDs from eM Client
	currentIDs := make(map[string]bool)
	for _, dir := range accountDirs {
		emails, err := parser.ParseEmails(dir, "")
		if err != nil {
			slog.Warn("prune email: cannot read eM Client DB, skipping prune", "dir", dir, "err", err)
			return result // Safety: don't prune if we can't read the source
		}
		for _, email := range emails {
			currentIDs[email.MessageID] = true
		}
	}

	sources, err := sourcesForCollection(conn, collectionID)
	if err != nil {
		result.Errors++
		result.ErrorMessages = append(result.ErrorMessages, err.Error())
		return result
	}

	for _, s := range sources {
		result.Checked++
		if !currentIDs[s.SourcePath] {
			slog.Info("pruning stale email", "messageID", s.SourcePath)
			deleteSourceByID(conn, s.ID)
			result.Pruned++
		}
	}

	return result
}

// pruneRSSSources removes indexed articles that no longer exist in NetNewsWire.
func pruneRSSSources(conn *sql.DB, cfg *config.Config, collectionID int64) *PruneResult {
	result := &PruneResult{}

	basePath := expandPath(cfg.NetnewswireDBPath)

	var accountDirs []string
	if fileExists(filepath.Join(basePath, "DB.sqlite3")) {
		accountDirs = []string{basePath}
	} else {
		accountDirs = parser.FindRSSAccountDirs(basePath)
	}

	if len(accountDirs) == 0 {
		slog.Warn("prune rss: no NetNewsWire accounts found, skipping prune")
		return result
	}

	// Collect all current article IDs
	currentIDs := make(map[string]bool)
	for _, dir := range accountDirs {
		ids, err := parser.CollectArticleIDs(dir)
		if err != nil {
			slog.Warn("prune rss: cannot read NetNewsWire DB, skipping prune", "dir", dir, "err", err)
			return result // Safety: don't prune if we can't read the source
		}
		for _, id := range ids {
			currentIDs[id] = true
		}
	}

	sources, err := sourcesForCollection(conn, collectionID)
	if err != nil {
		result.Errors++
		result.ErrorMessages = append(result.ErrorMessages, err.Error())
		return result
	}

	for _, s := range sources {
		result.Checked++
		if !currentIDs[s.SourcePath] {
			slog.Info("pruning stale article", "articleID", s.SourcePath)
			deleteSourceByID(conn, s.ID)
			result.Pruned++
		}
	}

	return result
}

// pruneCalibreSources removes indexed books that no longer exist in Calibre.
func pruneCalibreSources(conn *sql.DB, cfg *config.Config, collectionID int64) *PruneResult {
	result := &PruneResult{}

	// Build a set of all current Calibre source paths
	type bookKey struct {
		libraryPath  string
		relativePath string
	}
	currentBooks := make(map[bookKey]bool)

	for _, lib := range cfg.CalibreLibraries {
		lib = expandPath(lib)
		books, err := parser.ParseCalibreLibrary(lib)
		if err != nil {
			slog.Warn("prune calibre: cannot read Calibre library, skipping prune", "path", lib, "err", err)
			return result // Safety: don't prune if we can't read the source
		}
		for _, b := range books {
			currentBooks[bookKey{lib, b.RelativePath}] = true
		}
	}

	sources, err := sourcesForCollection(conn, collectionID)
	if err != nil {
		result.Errors++
		result.ErrorMessages = append(result.ErrorMessages, err.Error())
		return result
	}

	for _, s := range sources {
		result.Checked++

		if strings.HasPrefix(s.SourcePath, "calibre://") {
			// calibre://libraryPath/relativePath
			rest := s.SourcePath[len("calibre://"):]
			// Find the book by checking all libraries
			found := false
			for _, lib := range cfg.CalibreLibraries {
				lib = expandPath(lib)
				if strings.HasPrefix(rest, lib+"/") {
					relPath := rest[len(lib)+1:]
					if currentBooks[bookKey{lib, relPath}] {
						found = true
						break
					}
				}
			}
			if !found {
				slog.Info("pruning stale calibre source", "path", s.SourcePath)
				deleteSourceByID(conn, s.ID)
				result.Pruned++
			}
		} else {
			// File-based source — check file existence
			if _, err := os.Stat(s.SourcePath); os.IsNotExist(err) {
				slog.Info("pruning stale calibre file", "path", s.SourcePath)
				deleteSourceByID(conn, s.ID)
				result.Pruned++
			}
		}
	}

	return result
}

// pruneCodeSources removes stale code files and expired commits.
func pruneCodeSources(conn *sql.DB, cfg *config.Config, collectionID int64) *PruneResult {
	result := &PruneResult{}

	sources, err := sourcesForCollection(conn, collectionID)
	if err != nil {
		result.Errors++
		result.ErrorMessages = append(result.ErrorMessages, err.Error())
		return result
	}

	historyMonths := cfg.GitHistoryInMonths
	if historyMonths <= 0 {
		historyMonths = 6
	}
	cutoff := time.Now().AddDate(0, -historyMonths, 0)

	for _, s := range sources {
		result.Checked++

		if strings.HasPrefix(s.SourcePath, "git://") {
			// Commit source — check if date is within the configured history window
			if shouldPruneCommit(conn, s.ID, cutoff) {
				slog.Info("pruning expired commit", "path", s.SourcePath)
				deleteSourceByID(conn, s.ID)
				result.Pruned++
			}
		} else {
			// File source — check file existence
			if _, err := os.Stat(s.SourcePath); os.IsNotExist(err) {
				slog.Info("pruning stale code file", "path", s.SourcePath)
				deleteSourceByID(conn, s.ID)
				result.Pruned++
			}
		}
	}

	return result
}

// shouldPruneCommit checks if a commit source is older than the cutoff date.
func shouldPruneCommit(conn *sql.DB, sourceID int64, cutoff time.Time) bool {
	var metaJSON sql.NullString
	err := conn.QueryRow(
		"SELECT metadata FROM documents WHERE source_id = ? LIMIT 1", sourceID,
	).Scan(&metaJSON)
	if err != nil || !metaJSON.Valid {
		return false // Can't determine date, keep it
	}

	var meta map[string]any
	if json.Unmarshal([]byte(metaJSON.String), &meta) != nil {
		return false
	}

	dateStr, ok := meta["author_date"].(string)
	if !ok || dateStr == "" {
		return false
	}

	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		// Try ISO format without timezone
		t, err = time.Parse("2006-01-02T15:04:05-07:00", dateStr)
		if err != nil {
			return false
		}
	}

	return t.Before(cutoff)
}
