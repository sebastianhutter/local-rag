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
func getOrCreate(conn *sql.DB, name, collType string) int64 {
	id, err := db.GetOrCreateCollection(conn, name, collType, nil, nil)
	if err != nil {
		slog.Error("failed to get/create collection", "name", name, "err", err)
		return 0
	}
	return id
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

// collectFiles walks directories recursively and collects files with supported extensions.
func collectFiles(paths []string) []string {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			slog.Warn("path does not exist", "path", p)
			continue
		}
		if !info.IsDir() {
			if !isHidden(p) && parser.SourceTypeForPath(p) != "" {
				files = append(files, p)
			}
			continue
		}
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
			if parser.SourceTypeForPath(fp) != "" {
				files = append(files, fp)
			}
			return nil
		})
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
			if len(doc.Tags) > 0 {
				chunks[i].Metadata["tags"] = doc.Tags
			}
			if len(doc.Links) > 0 {
				chunks[i].Metadata["links"] = doc.Links
			}
		}
		return chunks

	case "pdf":
		pages := parser.ParsePDF(path)
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
func upsertSource(conn *sql.DB, collectionID int64, sourcePath, sourceType, fileH, mtime string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var existingID sql.NullInt64
	err := conn.QueryRow(
		"SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
		collectionID, sourcePath,
	).Scan(&existingID)

	if err == nil && existingID.Valid {
		sourceID := existingID.Int64
		// Delete old documents and vectors
		deleteOldDocs(conn, sourceID)
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
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		conn.Exec("DELETE FROM vec_documents WHERE document_id IN ("+placeholders+")", args...)
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
		if _, err := conn.Exec(
			"INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)",
			vecBytes, docID,
		); err != nil {
			return fmt.Errorf("insert vec: %w", err)
		}
	}

	return nil
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

// indexSingleFile indexes one file into a collection. Returns true if indexed, false if skipped.
func indexSingleFile(conn *sql.DB, cfg *config.Config, filePath string, collectionID int64, force bool) (bool, error) {
	absPath, _ := filepath.Abs(filePath)
	fh, err := fileHash(filePath)
	if err != nil {
		return false, fmt.Errorf("hash %s: %w", filePath, err)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	sourceType := parser.ExtensionMap[ext]
	if sourceType == "" {
		sourceType = "plaintext"
	}

	if !force && isSourceUnchanged(conn, collectionID, absPath, fh) {
		return false, nil
	}

	slog.Debug("parsing file", "path", filepath.Base(filePath), "type", sourceType)
	chunks := parseAndChunk(filePath, sourceType, cfg)
	if len(chunks) == 0 {
		slog.Warn("no content extracted, skipping", "path", filePath)
		return false, nil
	}

	slog.Debug("embedding chunks", "path", filepath.Base(filePath), "chunks", len(chunks))

	info, _ := os.Stat(filePath)
	mtime := ""
	if info != nil {
		mtime = info.ModTime().UTC().Format(time.RFC3339)
	}

	sourceID, err := upsertSource(conn, collectionID, absPath, sourceType, fh, mtime)
	if err != nil {
		return false, err
	}

	if err := insertChunks(conn, sourceID, collectionID, chunks, cfg); err != nil {
		return false, err
	}

	slog.Info("indexed file", "path", filepath.Base(filePath), "type", sourceType, "chunks", len(chunks))
	return true, nil
}

// IndexProject indexes documents from file paths into a named project collection.
func IndexProject(conn *sql.DB, cfg *config.Config, collectionName string, paths []string, force bool, progress ProgressCallback) *IndexResult {
	collectionID := getOrCreate(conn, collectionName, "project")

	files := collectFiles(paths)
	result := &IndexResult{TotalFound: len(files)}

	slog.Info("project indexer: found files", "count", len(files), "collection", collectionName)

	for i, f := range files {
		if progress != nil {
			progress(i+1, len(files), filepath.Base(f))
		}
		indexed, err := indexSingleFile(conn, cfg, f, collectionID, force)
		if err != nil {
			slog.Error("error indexing", "path", f, "err", err)
			result.Errors++
			result.ErrorMessages = append(result.ErrorMessages, err.Error())
			continue
		}
		if indexed {
			result.Indexed++
		} else {
			result.Skipped++
		}
	}

	slog.Info("project indexer done", "result", result.String())
	return result
}
