// Package db handles SQLite connection management with sqlite-vec and FTS5 support.
package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

// Open creates a new SQLite connection with WAL mode, foreign keys, and sqlite-vec loaded.
func Open(dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Verify connection works and sqlite-vec is loaded.
	var vecVersion string
	if err := db.QueryRow("SELECT vec_version()").Scan(&vecVersion); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite-vec not available: %w", err)
	}
	slog.Debug("sqlite-vec loaded", "version", vecVersion)

	return db, nil
}
