// Package indexer provides the indexing orchestration for local-rag.
//
// Each indexer reads content from a specific source, chunks it, embeds
// the chunks via Ollama, and stores everything in the SQLite database.
package indexer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

// getOrCreate wraps db.GetOrCreateCollection for convenience.
//
// The error is returned rather than swallowed: a zero ID would otherwise be
// used as a collection_id and quietly file documents under a collection that
// does not exist.
func getOrCreate(conn *sql.DB, name, collType string) (int64, error) {
	id, err := db.GetOrCreateCollection(conn, name, collType, nil, nil)
	if err != nil {
		slog.Error("failed to get/create collection", "name", name, "err", err)
		return 0, err
	}
	return id, nil
}

// failedResult builds a one-error result for a run that could not start.
func failedResult(err error) *IndexResult {
	return &IndexResult{Errors: 1, ErrorMessages: []string{err.Error()}}
}

// CheckNameConflict fails if this collection name is claimed by more than one
// config section. Collection names are unique, so indexing under an ambiguous
// name would merge two unrelated corpora into one collection.
//
// Only the ambiguous collection is refused — everything else still indexes.
func CheckNameConflict(cfg *config.Config, name string) error {
	for _, c := range cfg.CollectionNameConflicts() {
		if c.Name == name {
			return fmt.Errorf(
				"collection name conflict: %s — rename one of them in config, then index it under the new name", c)
		}
	}
	return nil
}

// embed wraps embeddings.GetEmbeddings for convenience.
func embed(texts []string, cfg *config.Config) ([][]float32, error) {
	return embeddings.GetEmbeddings(context.Background(), texts, cfg.EmbeddingModel)
}

// ProgressCallback is called per item with (current, total, itemName).
type ProgressCallback func(current, total int, itemName string)

// IndexResult summarises an indexing run.
type IndexResult struct {
	Indexed       int
	Skipped       int
	Errors        int
	TotalFound    int
	ErrorMessages []string
}

func (r *IndexResult) String() string {
	return fmt.Sprintf("Indexed: %d, Skipped: %d, Errors: %d, Total found: %d",
		r.Indexed, r.Skipped, r.Errors, r.TotalFound)
}

// Merge adds another result into this one.
func (r *IndexResult) Merge(other *IndexResult) {
	r.Indexed += other.Indexed
	r.Skipped += other.Skipped
	r.Errors += other.Errors
	r.TotalFound += other.TotalFound
	r.ErrorMessages = append(r.ErrorMessages, other.ErrorMessages...)
}

// fileHash computes SHA256 of a file.
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// isHidden checks if any path component starts with a dot.
func isHidden(path string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// collectFiles walks directories recursively and collects files with supported
// extensions. When skipPlaceholders is true, dataless cloud files are left out:
// reading one would make macOS download it from the provider first, so a folder
// that is mostly "online-only" would otherwise turn an index run into a
// multi-gigabyte download.
func collectFiles(paths []string, skipPlaceholders bool) []string {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			slog.Warn("path does not exist", "path", p)
			continue
		}
		if !info.IsDir() {
			if !isHidden(p) && parser.SourceTypeForPath(p) != "" {
				if skipPlaceholders && isCloudPlaceholder(info) {
					slog.Warn("skipping cloud-only file (not downloaded)", "path", p)
					continue
				}
				files = append(files, p)
			}
			continue
		}
		var placeholders int
		filepath.Walk(p, func(fp string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				return nil
			}
			if isHidden(fp) {
				return nil
			}
			if parser.SourceTypeForPath(fp) == "" {
				return nil
			}
			if skipPlaceholders && isCloudPlaceholder(fi) {
				placeholders++
				slog.Debug("skipping cloud-only file (not downloaded)", "path", fp)
				return nil
			}
			files = append(files, fp)
			return nil
		})
		if placeholders > 0 {
			slog.Warn("skipped cloud-only files; download them locally to index them "+
				"(Finder: Always Keep on This Device), or set skip_cloud_placeholders=false",
				"count", placeholders, "path", p)
		}
	}
	return files
}

// parseAndChunk dispatches a file to the right parser and returns chunks.
func parseAndChunk(path, sourceType string, cfg *config.Config) []chunker.Chunk {
	chunkSize := cfg.ChunkSizeTokens
	overlap := cfg.ChunkOverlapTokens
	title := filepath.Base(path)

	switch sourceType {
	case "markdown":
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Error("failed to read markdown file", "path", path, "err", err)
			return nil
		}
		doc := parser.ParseMarkdown(string(data), filepath.Base(path))
		chunks := chunker.ChunkMarkdown(doc.BodyText, doc.Title, chunkSize, overlap)
		for i := range chunks {
			// Copy frontmatter fields into chunk metadata.
			for k, v := range doc.Frontmatter {
				if k == "tags" {
					continue // handled separately below
				}
				chunks[i].Metadata[k] = v
			}
			if len(doc.Tags) > 0 {
				chunks[i].Metadata["tags"] = doc.Tags
			}
			if len(doc.Links) > 0 {
				chunks[i].Metadata["links"] = doc.Links
			}
		}
		return chunks

	case "pdf":
		var ocrOpts *parser.OCROptions
		if cfg.OCR.Enabled {
			ocrOpts = &parser.OCROptions{
				Enabled:       true,
				Languages:     cfg.OCR.Languages,
				MaxPages:      cfg.OCR.MaxPages,
				MaxFileSizeMB: cfg.OCR.MaxFileSizeMB,
				MinWordCount:  cfg.OCR.MinWordCount,
			}
		}
		pages := parser.ParsePDF(path, ocrOpts)
		if len(pages) == 0 {
			return nil
		}
		var chunks []chunker.Chunk
		chunkIdx := 0
		for _, page := range pages {
			pageTitle := fmt.Sprintf("%s (page %d)", title, page.PageNumber)
			pageChunks := chunker.ChunkPlain(page.Text, pageTitle, chunkSize, overlap)
			for i := range pageChunks {
				pageChunks[i].ChunkIndex = chunkIdx
				pageChunks[i].Metadata["page_number"] = page.PageNumber
				if page.OCR {
					pageChunks[i].Metadata["ocr"] = true
				}
				chunks = append(chunks, pageChunks[i])
				chunkIdx++
			}
		}
		return chunks

	case "docx":
		doc := parser.ParseDocx(path)
		if doc.Text == "" {
			return nil
		}
		return chunker.ChunkPlain(doc.Text, title, chunkSize, overlap)

	case "html":
		text := parser.ParseHTML(path)
		if text == "" {
			return nil
		}
		return chunker.ChunkPlain(text, title, chunkSize, overlap)

	case "plaintext":
		text := parser.ParsePlaintext(path)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return chunker.ChunkPlain(text, title, chunkSize, overlap)
	}

	slog.Warn("unknown source type", "type", sourceType, "path", path)
	return nil
}

// upsertSource inserts or updates a source row and deletes old documents/vectors.
// Returns the source ID.
//
// Callers replacing many sources at once should purge first with
// purgeSourceDocuments and then pass purged=true: the per-source vector delete
// below scans the whole vec0 table, which is ruinous in a loop.
func upsertSource(conn *sql.DB, collectionID int64, sourcePath, sourceType, fileH, mtime string) (int64, error) {
	return upsertSourceRow(conn, collectionID, sourcePath, sourceType, fileH, mtime, false)
}

func upsertSourceRow(conn *sql.DB, collectionID int64, sourcePath, sourceType, fileH, mtime string, purged bool) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var existingID sql.NullInt64
	err := conn.QueryRow(
		"SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, sourcePath,
	).Scan(&existingID)

	if err == nil && existingID.Valid {
		sourceID := existingID.Int64
		if !purged {
			// Delete old documents and vectors
			deleteOldDocs(conn, sourceID)
		}
		conn.Exec(
			"UPDATE sources SET file_hash = ?, file_modified_at = ?, last_indexed_at = ?, source_type = ? WHERE id = ?",
			fileH, mtime, now, sourceType, sourceID,
		)
		return sourceID, nil
	}

	res, err := conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, file_hash, file_modified_at, last_indexed_at) VALUES (?, ?, ?, ?, ?, ?)",
		collectionID, sourceType, sourcePath, fileH, mtime, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert source: %w", err)
	}
	return res.LastInsertId()
}

// deleteOldDocs removes documents and their vector entries for a source.
func deleteOldDocs(conn *sql.DB, sourceID int64) {
	rows, err := conn.Query("SELECT id FROM documents WHERE source_id = ?", sourceID)
	if err != nil {
		return
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}

	if len(ids) > 0 {
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		_ = db.DeleteEmbeddings(conn, args)
	}
	conn.Exec("DELETE FROM documents WHERE source_id = ?", sourceID)
}

// insertChunks embeds chunks and inserts them into documents + vec_documents.
func insertChunks(conn *sql.DB, sourceID, collectionID int64, chunks []chunker.Chunk, cfg *config.Config) error {
	if len(chunks) == 0 {
		return nil
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	vecs, err := embed(texts, cfg)
	if err != nil {
		return fmt.Errorf("embeddings: %w", err)
	}

	for i, c := range chunks {
		metaJSON := ""
		if len(c.Metadata) > 0 {
			b, _ := json.Marshal(c.Metadata)
			metaJSON = string(b)
		}

		res, err := conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
			sourceID, collectionID, c.ChunkIndex, c.Title, c.Text, metaJSON,
		)
		if err != nil {
			return fmt.Errorf("insert document: %w", err)
		}
		docID, _ := res.LastInsertId()

		vecBytes := embeddings.SerializeFloat32(vecs[i])
		if err := db.InsertEmbedding(conn, docID, vecBytes); err != nil {
			return err
		}
	}

	return nil
}

// isSourceCurrent reports whether a source is already indexed at the given
// modification time.
//
// This is a cheap pre-check that runs before hashing. Hashing reads the whole
// file, which on a cloud-backed folder means downloading it from the provider —
// so without this, "incremental" indexing still paid full I/O for every
// unchanged file on every run. mtime alone can be fooled by a deliberately
// restored timestamp; --force re-reads everything.
func isSourceCurrent(conn *sql.DB, collectionID int64, sourcePath, mtime string) bool {
	if mtime == "" {
		return false
	}
	var storedMtime sql.NullString
	err := conn.QueryRow(
		"SELECT file_modified_at FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, sourcePath,
	).Scan(&storedMtime)
	if err != nil {
		return false
	}
	return storedMtime.Valid && storedMtime.String == mtime
}

// isSourceUnchanged checks if a source's file hash matches. Returns true if unchanged.
func isSourceUnchanged(conn *sql.DB, collectionID int64, sourcePath, currentHash string) bool {
	var storedHash sql.NullString
	err := conn.QueryRow(
		"SELECT file_hash FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, sourcePath,
	).Scan(&storedHash)
	if err != nil {
		return false
	}
	return storedHash.Valid && storedHash.String == currentHash
}

// fileToItem prepares one file for indexing, or returns nil if it should be
// skipped — unchanged since the last run, or nothing extractable.
//
// The cheap checks run first and in order: stat, then hash, then parse. Parsing
// is the expensive step (PDF text extraction, OCR, tree-sitter), so it only
// happens for files that are actually going to be re-embedded.
func fileToItem(conn *sql.DB, cfg *config.Config, filePath string, collectionID int64, force bool) *indexItem {
	absPath, _ := filepath.Abs(filePath)

	info, statErr := os.Stat(filePath)
	mtime := ""
	if statErr == nil {
		mtime = info.ModTime().UTC().Format(time.RFC3339)
	}

	// Stat-level check first: an unchanged file is skipped without ever being
	// opened, which matters most for cloud-backed files where a read is a
	// download.
	if !force && isSourceCurrent(conn, collectionID, absPath, mtime) {
		return nil
	}

	fh, err := fileHash(filePath)
	if err != nil {
		slog.Warn("cannot hash file, skipping", "path", filePath, "err", err)
		return nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	sourceType := parser.ExtensionMap[ext]
	if sourceType == "" {
		sourceType = "plaintext"
	}

	// mtime moved but the content may still be identical (touch, re-sync,
	// metadata-only change) — the hash decides whether we re-embed.
	if !force && isSourceUnchanged(conn, collectionID, absPath, fh) {
		// Record the new mtime so the cheap check succeeds next run.
		conn.Exec(
			"UPDATE sources SET file_modified_at = ?, last_indexed_at = ? WHERE collection_id = ? AND source_path = ?",
			mtime, time.Now().UTC().Format(time.RFC3339), collectionID, absPath,
		)
		return nil
	}

	slog.Debug("parsing file", "path", filepath.Base(filePath), "type", sourceType)
	chunks := parseAndChunk(filePath, sourceType, cfg)
	if len(chunks) == 0 {
		slog.Warn("no content extracted, skipping", "path", filePath)
		return nil
	}

	return &indexItem{
		SourcePath: absPath,
		SourceType: sourceType,
		Chunks:     chunks,
		FileHash:   fh,
		Mtime:      mtime,
	}
}

// PruneResult summarises a pruning run.
type PruneResult struct {
	Pruned        int
	Checked       int
	Errors        int
	ErrorMessages []string
}

func (r *PruneResult) String() string {
	return fmt.Sprintf("Pruned: %d, Checked: %d, Errors: %d", r.Pruned, r.Checked, r.Errors)
}

// Merge adds another result into this one.
func (r *PruneResult) Merge(other *PruneResult) {
	r.Pruned += other.Pruned
	r.Checked += other.Checked
	r.Errors += other.Errors
	r.ErrorMessages = append(r.ErrorMessages, other.ErrorMessages...)
}

// sourceInfo holds basic info about an indexed source row.
type sourceInfo struct {
	ID         int64
	SourcePath string
	SourceType string
}

// sourcesForCollection returns all sources for a collection.
func sourcesForCollection(conn *sql.DB, collectionID int64) ([]sourceInfo, error) {
	rows, err := conn.Query(
		"SELECT id, source_path, source_type FROM sources WHERE collection_id = ?",
		collectionID,
	)
	if err != nil {
		return nil, fmt.Errorf("query sources: %w", err)
	}
	defer rows.Close()

	var sources []sourceInfo
	for rows.Next() {
		var s sourceInfo
		if err := rows.Scan(&s.ID, &s.SourcePath, &s.SourceType); err != nil {
			continue
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// deleteSourceByID deletes a source and all its documents and vectors by source ID.
func deleteSourceByID(conn *sql.DB, sourceID int64) {
	deleteOldDocs(conn, sourceID)
	conn.Exec("DELETE FROM sources WHERE id = ?", sourceID)
}

// deleteSource deletes a source by collection ID and source path.
func deleteSource(conn *sql.DB, collectionID int64, sourcePath string) {
	var sourceID sql.NullInt64
	err := conn.QueryRow(
		"SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, sourcePath,
	).Scan(&sourceID)
	if err != nil || !sourceID.Valid {
		return
	}
	deleteSourceByID(conn, sourceID.Int64)
}

// IndexProject indexes documents from file paths into a named project collection.
func IndexProject(conn *sql.DB, cfg *config.Config, collectionName string, paths []string, force bool, progress ProgressCallback) *IndexResult {
	if err := CheckNameConflict(cfg, collectionName); err != nil {
		slog.Error("refusing to index", "name", collectionName, "err", err)
		return failedResult(err)
	}

	collectionID, err := db.GetOrCreateCollection(conn, collectionName, "project", nil, nil)
	if err != nil {
		slog.Error("failed to get/create collection", "name", collectionName, "err", err)
		return failedResult(err)
	}

	files := collectFiles(paths, cfg.SkipCloudPlaceholders)
	result := &IndexResult{TotalFound: len(files)}

	slog.Info("project indexer: found files", "count", len(files), "collection", collectionName)

	indexItemsBatched(conn, cfg, collectionID, collectionName, len(files),
		func(i int) *indexItem { return fileToItem(conn, cfg, files[i], collectionID, force) },
		result, progress)

	slog.Info("project indexer done", "result", result.String())
	return result
}
