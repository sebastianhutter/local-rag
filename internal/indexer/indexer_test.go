package indexer

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/db"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.InitSchema(conn, 1024); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestFileHash(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(f, []byte("hello world"), 0o644)

	h1, err := fileHash(f)
	if err != nil {
		t.Fatalf("fileHash failed: %v", err)
	}
	if h1 == "" {
		t.Fatal("expected non-empty hash")
	}

	// Same content → same hash
	f2 := filepath.Join(tmpDir, "test2.txt")
	os.WriteFile(f2, []byte("hello world"), 0o644)
	h2, _ := fileHash(f2)
	if h1 != h2 {
		t.Errorf("same content should produce same hash: %s != %s", h1, h2)
	}

	// Different content → different hash
	f3 := filepath.Join(tmpDir, "test3.txt")
	os.WriteFile(f3, []byte("goodbye world"), 0o644)
	h3, _ := fileHash(f3)
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
}

func TestIsHidden(t *testing.T) {
	tests := []struct {
		path   string
		hidden bool
	}{
		{"foo/bar/baz.txt", false},
		{".hidden/file.txt", true},
		{"foo/.hidden/file.txt", true},
		{"foo/bar/.file", true},
		{"normal/path/file.go", false},
	}
	for _, tt := range tests {
		if got := isHidden(tt.path); got != tt.hidden {
			t.Errorf("isHidden(%q) = %v, want %v", tt.path, got, tt.hidden)
		}
	}
}

func TestCollectFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some supported files
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# hello"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "doc.txt"), []byte("text"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, ".hidden.md"), []byte("hidden"), 0o644)
	os.MkdirAll(filepath.Join(tmpDir, "sub"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "sub", "note.md"), []byte("note"), 0o644)

	files := collectFiles([]string{tmpDir})
	if len(files) < 3 {
		t.Errorf("expected at least 3 files, got %d: %v", len(files), files)
	}

	// Hidden file should not be included
	for _, f := range files {
		if filepath.Base(f) == ".hidden.md" {
			t.Error("hidden file should not be included")
		}
	}
}

func TestGetOrCreate(t *testing.T) {
	conn := setupTestDB(t)

	id1 := getOrCreate(conn, "test-collection", "project")
	if id1 == 0 {
		t.Fatal("expected non-zero collection ID")
	}

	// Same name returns same ID
	id2 := getOrCreate(conn, "test-collection", "project")
	if id1 != id2 {
		t.Errorf("same name should return same id: %d != %d", id1, id2)
	}

	// Different name returns different ID
	id3 := getOrCreate(conn, "other-collection", "system")
	if id3 == id1 {
		t.Error("different name should return different ID")
	}
}

func TestUpsertSource(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "test", "project")

	// First insert
	id1, err := upsertSource(conn, collID, "/path/to/file.md", "markdown", "abc123", "2025-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("upsertSource failed: %v", err)
	}
	if id1 == 0 {
		t.Fatal("expected non-zero source ID")
	}

	// Same path → same ID, updated hash
	id2, err := upsertSource(conn, collID, "/path/to/file.md", "markdown", "def456", "2025-01-02T00:00:00Z")
	if err != nil {
		t.Fatalf("upsertSource failed on update: %v", err)
	}
	if id2 != id1 {
		t.Errorf("upsert should return same ID: %d != %d", id2, id1)
	}

	// Verify hash was updated
	var hash string
	conn.QueryRow("SELECT file_hash FROM sources WHERE id = ?", id1).Scan(&hash)
	if hash != "def456" {
		t.Errorf("expected updated hash 'def456', got %q", hash)
	}
}

func TestIsSourceUnchanged(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "test", "project")

	upsertSource(conn, collID, "/file.md", "markdown", "hash123", "")

	if !isSourceUnchanged(conn, collID, "/file.md", "hash123") {
		t.Error("source should be unchanged with same hash")
	}
	if isSourceUnchanged(conn, collID, "/file.md", "different") {
		t.Error("source should be changed with different hash")
	}
	if isSourceUnchanged(conn, collID, "/nonexistent.md", "hash123") {
		t.Error("nonexistent source should not be unchanged")
	}
}

func TestIsSourceExists(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "test", "project")

	if isSourceExists(conn, collID, "msg-id-1") {
		t.Error("source should not exist yet")
	}

	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'email', ?, datetime('now'))",
		collID, "msg-id-1",
	)

	if !isSourceExists(conn, collID, "msg-id-1") {
		t.Error("source should exist after insert")
	}
}

func TestDeleteOldDocs(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "test", "project")

	res, _ := conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'markdown', '/file.md', datetime('now'))",
		collID,
	)
	sourceID, _ := res.LastInsertId()

	// Insert some documents
	for i := 0; i < 3; i++ {
		conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content) VALUES (?, ?, ?, 'test', 'content')",
			sourceID, collID, i,
		)
	}

	var count int
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE source_id = ?", sourceID).Scan(&count)
	if count != 3 {
		t.Fatalf("expected 3 documents, got %d", count)
	}

	deleteOldDocs(conn, sourceID)

	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE source_id = ?", sourceID).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 documents after delete, got %d", count)
	}
}

func TestIndexResultMerge(t *testing.T) {
	r1 := &IndexResult{Indexed: 5, Skipped: 3, Errors: 1, TotalFound: 9}
	r2 := &IndexResult{Indexed: 2, Skipped: 1, Errors: 0, TotalFound: 3, ErrorMessages: []string{"oops"}}

	r1.Merge(r2)

	if r1.Indexed != 7 {
		t.Errorf("expected Indexed=7, got %d", r1.Indexed)
	}
	if r1.Skipped != 4 {
		t.Errorf("expected Skipped=4, got %d", r1.Skipped)
	}
	if r1.Errors != 1 {
		t.Errorf("expected Errors=1, got %d", r1.Errors)
	}
	if r1.TotalFound != 12 {
		t.Errorf("expected TotalFound=12, got %d", r1.TotalFound)
	}
	if len(r1.ErrorMessages) != 1 {
		t.Errorf("expected 1 error message, got %d", len(r1.ErrorMessages))
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input    string
		contains string
	}{
		{"~/Documents", filepath.Join(home, "Documents")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := expandPath(tt.input)
		if got != tt.contains {
			t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.contains)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("expected 'short', got %q", got)
	}
	if got := truncate("this is a long string", 10); got != "this is a " {
		t.Errorf("expected truncated string, got %q", got)
	}
}

func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "exists.txt")
	os.WriteFile(f, []byte("hi"), 0o644)

	if !fileExists(f) {
		t.Error("file should exist")
	}
	if fileExists(filepath.Join(tmpDir, "nope.txt")) {
		t.Error("file should not exist")
	}
}
