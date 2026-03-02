package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAndVecVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var version string
	if err := db.QueryRow("SELECT vec_version()").Scan(&version); err != nil {
		t.Fatalf("vec_version: %v", err)
	}
	if version == "" {
		t.Error("expected non-empty vec_version")
	}
}

func TestOpenCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("expected directory to be created")
	}
}

func TestInitSchema(t *testing.T) {
	db := testDB(t)

	if err := InitSchema(db, 1024); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Verify tables exist.
	tables := []string{"collections", "sources", "documents", "meta"}
	for _, table := range tables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}

	// Verify schema version.
	var version string
	if err := db.QueryRow("SELECT value FROM meta WHERE key='schema_version'").Scan(&version); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if version != "3" {
		t.Errorf("schema_version = %q, want 3", version)
	}
}

func TestInitSchemaIdempotent(t *testing.T) {
	db := testDB(t)

	// Running InitSchema twice should not error.
	if err := InitSchema(db, 1024); err != nil {
		t.Fatalf("first InitSchema: %v", err)
	}
	if err := InitSchema(db, 1024); err != nil {
		t.Fatalf("second InitSchema: %v", err)
	}
}

func TestGetOrCreateCollection(t *testing.T) {
	db := testDB(t)
	if err := InitSchema(db, 1024); err != nil {
		t.Fatal(err)
	}

	// Create new collection.
	id1, err := GetOrCreateCollection(db, "test-collection", "project", nil, nil)
	if err != nil {
		t.Fatalf("GetOrCreateCollection: %v", err)
	}
	if id1 == 0 {
		t.Error("expected non-zero ID")
	}

	// Get existing collection.
	id2, err := GetOrCreateCollection(db, "test-collection", "project", nil, nil)
	if err != nil {
		t.Fatalf("GetOrCreateCollection (existing): %v", err)
	}
	if id1 != id2 {
		t.Errorf("IDs differ: %d != %d", id1, id2)
	}

	// Create with different name.
	id3, err := GetOrCreateCollection(db, "other-collection", "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id3 == id1 {
		t.Error("expected different ID for different collection")
	}
}

func TestGetOrCreateCollectionWithPaths(t *testing.T) {
	db := testDB(t)
	if err := InitSchema(db, 1024); err != nil {
		t.Fatal(err)
	}

	paths := []string{"/path/a", "/path/b"}
	id, err := GetOrCreateCollection(db, "project-x", "project", nil, paths)
	if err != nil {
		t.Fatal(err)
	}

	// Verify paths stored.
	var pathsJSON sql.NullString
	if err := db.QueryRow("SELECT paths FROM collections WHERE id = ?", id).Scan(&pathsJSON); err != nil {
		t.Fatal(err)
	}
	if !pathsJSON.Valid || pathsJSON.String != `["/path/a","/path/b"]` {
		t.Errorf("paths = %v, want JSON array", pathsJSON)
	}

	// Update paths on existing collection.
	newPaths := []string{"/path/c"}
	id2, err := GetOrCreateCollection(db, "project-x", "project", nil, newPaths)
	if err != nil {
		t.Fatal(err)
	}
	if id != id2 {
		t.Error("expected same ID")
	}

	if err := db.QueryRow("SELECT paths FROM collections WHERE id = ?", id).Scan(&pathsJSON); err != nil {
		t.Fatal(err)
	}
	if pathsJSON.String != `["/path/c"]` {
		t.Errorf("paths not updated: %v", pathsJSON.String)
	}
}
