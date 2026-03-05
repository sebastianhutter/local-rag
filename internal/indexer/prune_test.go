package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestShouldPruneCommit(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "code-group", "code")

	// Insert a commit source with old metadata
	res, _ := conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'commit', 'git:///repo#old', datetime('now'))",
		collID,
	)
	oldSourceID, _ := res.LastInsertId()

	oldDate := time.Now().AddDate(0, -12, 0).Format(time.RFC3339) // 12 months ago
	oldMeta, _ := json.Marshal(map[string]any{"author_date": oldDate})
	conn.Exec(
		"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, 0, 'commit', 'content', ?)",
		oldSourceID, collID, string(oldMeta),
	)

	// Insert a commit source with recent metadata
	res, _ = conn.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'commit', 'git:///repo#new', datetime('now'))",
		collID,
	)
	newSourceID, _ := res.LastInsertId()

	newDate := time.Now().AddDate(0, -1, 0).Format(time.RFC3339) // 1 month ago
	newMeta, _ := json.Marshal(map[string]any{"author_date": newDate})
	conn.Exec(
		"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, 0, 'commit', 'content', ?)",
		newSourceID, collID, string(newMeta),
	)

	cutoff := time.Now().AddDate(0, -6, 0) // 6 months ago

	if !shouldPruneCommit(conn, oldSourceID, cutoff) {
		t.Error("old commit (12 months ago) should be pruned with 6-month cutoff")
	}
	if shouldPruneCommit(conn, newSourceID, cutoff) {
		t.Error("recent commit (1 month ago) should not be pruned with 6-month cutoff")
	}
}
