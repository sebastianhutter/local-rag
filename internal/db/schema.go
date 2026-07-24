package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// SchemaVersion is the current schema version. Bump this when adding migrations.
const SchemaVersion = 4

// InitSchema creates all tables, virtual tables, and triggers if they don't exist.
func InitSchema(db *sql.DB, embeddingDim int) error {
	schema := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS collections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			collection_type TEXT NOT NULL DEFAULT 'project',
			description TEXT,
			paths TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
			source_type TEXT NOT NULL,
			source_path TEXT NOT NULL,
			file_hash TEXT,
			file_modified_at TEXT,
			last_indexed_at TEXT,
			UNIQUE(collection_id, source_path)
		);

		CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
			chunk_index INTEGER NOT NULL,
			title TEXT,
			content TEXT NOT NULL,
			metadata TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(source_id, chunk_index)
		);

		CREATE VIRTUAL TABLE IF NOT EXISTS vec_documents USING vec0(
			embedding float[%d],
			document_id INTEGER
		);

		-- Binary-quantized mirror of vec_documents for fast candidate retrieval.
		-- Each row shares the rowid of its vec_documents counterpart, so exact
		-- float vectors can be fetched by rowid for reranking. See search package.
		CREATE VIRTUAL TABLE IF NOT EXISTS vec_documents_bin USING vec0(
			embedding bit[%[1]d],
			document_id INTEGER
		);

		CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
			title,
			content,
			content='documents',
			content_rowid='id'
		);

		CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
			INSERT INTO documents_fts(rowid, title, content)
			VALUES (new.id, new.title, new.content);
		END;

		CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, content)
			VALUES('delete', old.id, old.title, old.content);
		END;

		CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, content)
			VALUES('delete', old.id, old.title, old.content);
			INSERT INTO documents_fts(rowid, title, content)
			VALUES (new.id, new.title, new.content);
		END;

		CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT
		);

		-- Speeds up per-collection COUNT/aggregation (collections list/info) and
		-- collection-scoped deletes. Without it, those queries full-scan documents.
		CREATE INDEX IF NOT EXISTS idx_documents_collection_id ON documents(collection_id);
	`, embeddingDim)

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	// Set schema version if not already set.
	var existing sql.NullString
	err := db.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&existing)
	if err == sql.ErrNoRows {
		if _, err := db.Exec(
			"INSERT INTO meta (key, value) VALUES ('schema_version', ?)",
			fmt.Sprintf("%d", SchemaVersion),
		); err != nil {
			return fmt.Errorf("set schema version: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}

	if err := backfillBinaryVectors(db); err != nil {
		return fmt.Errorf("backfill binary vectors: %w", err)
	}

	slog.Info("database schema initialized", "version", SchemaVersion, "embedding_dim", embeddingDim)
	return nil
}

// binaryBackfillDoneKey marks that vec_documents_bin has been fully populated
// from the existing vec_documents rows. Once set, InitSchema skips the check
// on subsequent opens, keeping the search hot path cheap.
const binaryBackfillDoneKey = "binary_backfill_done"

// backfillBinaryVectors populates vec_documents_bin with binary-quantized copies
// of every vec_documents row that does not yet have one. It runs once (guarded
// by a meta flag) to migrate databases created before binary quantization was
// added; new inserts keep the two tables in sync via InsertEmbedding.
func backfillBinaryVectors(db *sql.DB) error {
	var done sql.NullString
	err := db.QueryRow("SELECT value FROM meta WHERE key = ?", binaryBackfillDoneKey).Scan(&done)
	if err == nil && done.Valid && done.String == "1" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("check backfill flag: %w", err)
	}

	// Compare row counts (cheap — reads the rowid shadow tables, not vectors).
	var floatCount, binCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM vec_documents").Scan(&floatCount); err != nil {
		return fmt.Errorf("count vectors: %w", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM vec_documents_bin").Scan(&binCount); err != nil {
		return fmt.Errorf("count binary vectors: %w", err)
	}

	if binCount != floatCount {
		// Rebuild from scratch. We clear any partial rows and re-insert everything
		// rather than filtering with NOT IN: a WHERE subquery on the vec0 source
		// makes sqlite-vec mishandle the vector column type during INSERT...SELECT.
		if binCount > 0 {
			if _, err := db.Exec("DELETE FROM vec_documents_bin"); err != nil {
				return fmt.Errorf("clear partial binary vectors: %w", err)
			}
		}
		if floatCount > 0 {
			slog.Info("backfilling binary-quantized vectors (one-time)", "count", floatCount)
			if _, err := db.Exec(`
				INSERT INTO vec_documents_bin(rowid, embedding, document_id)
				SELECT rowid, vec_quantize_binary(embedding), document_id
				FROM vec_documents
			`); err != nil {
				return fmt.Errorf("insert binary vectors: %w", err)
			}
			slog.Info("binary vector backfill complete", "count", floatCount)
		}
	}

	if _, err := db.Exec(
		"INSERT OR REPLACE INTO meta (key, value) VALUES (?, '1')",
		binaryBackfillDoneKey,
	); err != nil {
		return fmt.Errorf("set backfill flag: %w", err)
	}
	return nil
}

// InsertEmbedding inserts an embedding for a document into both the float
// vector table and its binary-quantized mirror, keeping their rowids aligned.
// The binary mirror is used for fast candidate retrieval during search; the
// float table holds the exact vectors used for reranking.
func InsertEmbedding(conn *sql.DB, documentID int64, vecBytes []byte) error {
	res, err := conn.Exec(
		"INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)",
		vecBytes, documentID,
	)
	if err != nil {
		return fmt.Errorf("insert vec: %w", err)
	}
	rowid, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("vec rowid: %w", err)
	}
	if _, err := conn.Exec(
		"INSERT INTO vec_documents_bin (rowid, embedding, document_id) VALUES (?, vec_quantize_binary(?), ?)",
		rowid, vecBytes, documentID,
	); err != nil {
		return fmt.Errorf("insert binary vec: %w", err)
	}
	return nil
}

// PruneSources deletes the given sources together with all their documents and
// embeddings (float + binary mirror) in a single transaction.
//
// It is the bulk equivalent of deleting sources one at a time. Deleting from
// the vec0 tables filters on the `document_id` metadata column, which forces a
// full scan of the vector table per DELETE — so doing it once per source is
// O(sources × table size) and painfully slow when pruning many files. Batching
// every stale source into one pair of DELETEs (via a document-id subquery)
// makes it a single scan of each vector table regardless of how many sources
// are pruned.
func PruneSources(conn *sql.DB, sourceIDs []int64) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	inList := intList(sourceIDs)
	docSubquery := "SELECT id FROM documents WHERE source_id IN (" + inList + ")"

	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("begin prune tx: %w", err)
	}
	// Order matters: delete vector rows (which resolve document ids from the
	// documents table) before the documents themselves.
	stmts := []string{
		"DELETE FROM vec_documents_bin WHERE document_id IN (" + docSubquery + ")",
		"DELETE FROM vec_documents WHERE document_id IN (" + docSubquery + ")",
		"DELETE FROM documents WHERE source_id IN (" + inList + ")",
		"DELETE FROM sources WHERE id IN (" + inList + ")",
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("prune delete: %w", err)
		}
	}
	return tx.Commit()
}

// intList renders int64 IDs as a comma-separated SQL literal list. The IDs are
// internal primary keys, so inlining them is safe from injection.
func intList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// DeleteEmbeddings removes embeddings for the given document IDs from both the
// float vector table and its binary-quantized mirror.
func DeleteEmbeddings(conn *sql.DB, documentIDs []any) error {
	if len(documentIDs) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(documentIDs)), ",")
	if _, err := conn.Exec(
		"DELETE FROM vec_documents_bin WHERE document_id IN ("+placeholders+")", documentIDs...,
	); err != nil {
		return fmt.Errorf("delete binary vecs: %w", err)
	}
	if _, err := conn.Exec(
		"DELETE FROM vec_documents WHERE document_id IN ("+placeholders+")", documentIDs...,
	); err != nil {
		return fmt.Errorf("delete vecs: %w", err)
	}
	return nil
}

// GetOrCreateCollection returns the ID of an existing collection or creates a new one.
func GetOrCreateCollection(db *sql.DB, name, collectionType string, description *string, paths []string) (int64, error) {
	var pathsJSON *string
	if len(paths) > 0 {
		b, err := json.Marshal(paths)
		if err != nil {
			return 0, fmt.Errorf("marshal paths: %w", err)
		}
		s := string(b)
		pathsJSON = &s
	}

	var id int64
	err := db.QueryRow("SELECT id FROM collections WHERE name = ?", name).Scan(&id)
	if err == nil {
		// Collection exists — update paths if provided.
		if pathsJSON != nil {
			if _, err := db.Exec("UPDATE collections SET paths = ? WHERE id = ?", *pathsJSON, id); err != nil {
				return 0, fmt.Errorf("update collection paths: %w", err)
			}
		}
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("query collection: %w", err)
	}

	// Create new collection.
	result, err := db.Exec(
		"INSERT INTO collections (name, collection_type, description, paths) VALUES (?, ?, ?, ?)",
		name, collectionType, description, pathsJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("insert collection: %w", err)
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get collection id: %w", err)
	}

	slog.Info("created collection", "name", name, "type", collectionType, "id", id)
	return id, nil
}

// GetCollectionPaths returns the stored paths for a collection.
func GetCollectionPaths(db *sql.DB, name string) ([]string, error) {
	var pathsJSON sql.NullString
	err := db.QueryRow("SELECT paths FROM collections WHERE name = ?", name).Scan(&pathsJSON)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("collection %q not found", name)
	}
	if err != nil {
		return nil, fmt.Errorf("query collection paths: %w", err)
	}
	if !pathsJSON.Valid || pathsJSON.String == "" {
		return nil, nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON.String), &paths); err != nil {
		return nil, fmt.Errorf("unmarshal paths: %w", err)
	}
	return paths, nil
}

// SetCollectionPaths stores paths for an existing collection.
func SetCollectionPaths(db *sql.DB, name string, paths []string) error {
	var pathsJSON *string
	if len(paths) > 0 {
		b, err := json.Marshal(paths)
		if err != nil {
			return fmt.Errorf("marshal paths: %w", err)
		}
		s := string(b)
		pathsJSON = &s
	}

	result, err := db.Exec("UPDATE collections SET paths = ? WHERE name = ?", pathsJSON, name)
	if err != nil {
		return fmt.Errorf("update collection paths: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	return nil
}

// RewriteSourcePaths replaces oldPrefix with newPrefix in source_path for all
// sources belonging to the named collection. Returns the number of rows updated.
func RewriteSourcePaths(d *sql.DB, collectionName, oldPrefix, newPrefix string) (int64, error) {
	var collID int64
	err := d.QueryRow("SELECT id FROM collections WHERE name = ?", collectionName).Scan(&collID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("collection %q not found", collectionName)
	}
	if err != nil {
		return 0, fmt.Errorf("lookup collection: %w", err)
	}

	result, err := d.Exec(`
		UPDATE sources
		SET source_path = ? || SUBSTR(source_path, ? + 1)
		WHERE collection_id = ?
		  AND source_path LIKE ? || '%'
	`, newPrefix, len(oldPrefix), collID, oldPrefix)
	if err != nil {
		return 0, fmt.Errorf("rewrite source paths: %w", err)
	}
	return result.RowsAffected()
}
