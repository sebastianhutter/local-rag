package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
)

// SchemaVersion is the current schema version. Bump this when adding migrations.
const SchemaVersion = 3

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

	slog.Info("database schema initialized", "version", SchemaVersion, "embedding_dim", embeddingDim)
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
