package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/config"
)

func TestPruneFileSources(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "obsidian", "system")

	tmpDir := t.TempDir()

	// Create a real file
	existingFile := filepath.Join(tmpDir, "existing.md")
	os.WriteFile(existingFile, []byte("# Hello"), 0o644)

	// Insert source for existing file
	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'markdown', ?, datetime('now'))",
		collID, existingFile,
	)

	// Insert source for non-existent file
	missingFile := filepath.Join(tmpDir, "deleted.md")
	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'markdown', ?, datetime('now'))",
		collID, missingFile,
	)

	result := pruneFileSources(conn, collID)

	if result.Checked != 2 {
		t.Errorf("expected 2 checked, got %d", result.Checked)
	}
	if result.Pruned != 1 {
		t.Errorf("expected 1 pruned, got %d", result.Pruned)
	}

	// Verify existing source still exists
	var count int
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ? AND source_path = ?", collID, existingFile).Scan(&count)
	if count != 1 {
		t.Error("existing source should not be pruned")
	}

	// Verify missing source was deleted
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ? AND source_path = ?", collID, missingFile).Scan(&count)
	if count != 0 {
		t.Error("missing source should be pruned")
	}
}

func TestDeleteSourceByID(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "test", "project")

	// Insert source
	res, _ := conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'markdown', '/file.md', datetime('now'))",
		collID,
	)
	sourceID, _ := res.LastInsertId()

	// Insert documents for that source
	for i := 0; i < 3; i++ {
		conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content) VALUES (?, ?, ?, 'test', 'content')",
			sourceID, collID, i,
		)
	}

	var count int
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE id = ?", sourceID).Scan(&count)
	if count != 1 {
		t.Fatal("expected source to exist")
	}
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE source_id = ?", sourceID).Scan(&count)
	if count != 3 {
		t.Fatalf("expected 3 documents, got %d", count)
	}

	deleteSourceByID(conn, sourceID)

	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE id = ?", sourceID).Scan(&count)
	if count != 0 {
		t.Error("source should be deleted")
	}
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE source_id = ?", sourceID).Scan(&count)
	if count != 0 {
		t.Error("documents should be cascade-deleted")
	}
}

func TestPruneSkipsURISources(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "calibre", "system")

	// Insert a calibre:// URI source
	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'calibre-description', ?, datetime('now'))",
		collID, "calibre:///Library/books/some-book",
	)

	// Insert a git:// URI source
	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'commit', ?, datetime('now'))",
		collID, "git:///repo#abc123",
	)

	result := pruneFileSources(conn, collID)

	// URI sources should be skipped entirely, not checked
	if result.Checked != 0 {
		t.Errorf("expected 0 checked (URIs skipped), got %d", result.Checked)
	}
	if result.Pruned != 0 {
		t.Errorf("expected 0 pruned, got %d", result.Pruned)
	}

	// Verify sources still exist
	var count int
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ?", collID).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 sources still present, got %d", count)
	}
}

func TestSourcesForCollection(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "test", "project")

	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'markdown', '/a.md', datetime('now'))",
		collID,
	)
	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'pdf', '/b.pdf', datetime('now'))",
		collID,
	)

	sources, err := sourcesForCollection(conn, collID)
	if err != nil {
		t.Fatalf("sourcesForCollection failed: %v", err)
	}
	if len(sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(sources))
	}
}

func TestPruneResultMerge(t *testing.T) {
	r1 := &PruneResult{Pruned: 3, Checked: 10, Errors: 1, ErrorMessages: []string{"err1"}}
	r2 := &PruneResult{Pruned: 2, Checked: 5, Errors: 0}

	r1.Merge(r2)

	if r1.Pruned != 5 {
		t.Errorf("expected Pruned=5, got %d", r1.Pruned)
	}
	if r1.Checked != 15 {
		t.Errorf("expected Checked=15, got %d", r1.Checked)
	}
	if r1.Errors != 1 {
		t.Errorf("expected Errors=1, got %d", r1.Errors)
	}
	if len(r1.ErrorMessages) != 1 {
		t.Errorf("expected 1 error message, got %d", len(r1.ErrorMessages))
	}
}

func TestPruneCollectionNonexistent(t *testing.T) {
	conn := setupTestDB(t)
	cfg := &config.Config{}

	result := PruneCollection(conn, cfg, "nonexistent")
	if result.Pruned != 0 || result.Checked != 0 || result.Errors != 0 {
		t.Errorf("expected empty result for nonexistent collection, got %+v", result)
	}
}

func TestPruneCodeSkipsCommits(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "code-group", "code")
	cfg := &config.Config{GitHistoryInMonths: 6}

	// Insert a commit source (git:// URI)
	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'commit', 'git:///repo#abc123', datetime('now'))",
		collID,
	)

	// Insert a code file source that doesn't exist on disk
	conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'code', '/nonexistent/file.go', datetime('now'))",
		collID,
	)

	result := pruneCodeSources(conn, cfg, collID)

	// Only the file source should be checked, not the commit
	if result.Checked != 1 {
		t.Errorf("expected 1 checked (file only), got %d", result.Checked)
	}
	if result.Pruned != 1 {
		t.Errorf("expected 1 pruned (missing file), got %d", result.Pruned)
	}

	// Commit source should still exist
	var count int
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ? AND source_path = 'git:///repo#abc123'", collID).Scan(&count)
	if count != 1 {
		t.Error("commit source should never be pruned")
	}
}
