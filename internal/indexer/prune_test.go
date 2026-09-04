package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/config"
)

func TestPruneFileSources(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "obsidian", "system")

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

	result := pruneFileSources(conn, collID, nil)

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
	collID := mustGetOrCreate(t, conn, "test", "project")

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
	collID := mustGetOrCreate(t, conn, "calibre", "system")

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

	result := pruneFileSources(conn, collID, nil)

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
	collID := mustGetOrCreate(t, conn, "test", "project")

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
	collID := mustGetOrCreate(t, conn, "code-group", "code")
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

func TestVaultExcludesPath(t *testing.T) {
	vaults := []string{"/vault"}
	excl := map[string]bool{"_Inbox": true, "_Claude Sessions": true}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"ordinary note", "/vault/Notes/note.md", false},
		{"inside an excluded folder", "/vault/_Inbox/note.md", true},
		{"excluded folder with a space", "/vault/_Claude Sessions/2026-01-01_x.md", true},
		{"nested under an excluded folder", "/vault/_Inbox/deep/deeper/note.md", true},
		{"obsidian internals", "/vault/.obsidian/plugins/x.md", true},
		{"any dot-directory", "/vault/.smart-env/cache.md", true},
		{"dotfile", "/vault/Notes/.hidden.md", true},
		// A substring is not a path component: excluding "_Inbox" must not take
		// out a folder or file that merely contains the word.
		{"folder name containing an excluded name", "/vault/My _Inbox Archive/note.md", false},
		{"file name containing an excluded name", "/vault/Notes/_Inbox notes.md", false},
		// An excluded name above the vault root belongs to somebody else.
		{"excluded name above the vault root", "/vault/Notes/ok.md", false},
		// Outside every configured vault we cannot judge, so we leave it alone.
		{"outside the vault", "/elsewhere/_Inbox/note.md", false},
		{"the vault root itself", "/vault", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vaultExcludesPath(tt.path, vaults, excl); got != tt.want {
				t.Errorf("vaultExcludesPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}

	t.Run("excluded name above the root does not condemn the vault", func(t *testing.T) {
		if vaultExcludesPath("/home/_Inbox/vault/Notes/note.md", []string{"/home/_Inbox/vault"}, excl) {
			t.Error("a path component above the vault root was treated as an exclusion")
		}
	})
}

// A vault-less config must not make everything prunable.
func TestObsidianExcluderWithoutVaults(t *testing.T) {
	if got := obsidianExcluder(&config.Config{ObsidianExcludeFolders: []string{"_Inbox"}}); got != nil {
		t.Error("expected nil predicate when no vault is configured")
	}
	if got := obsidianExcluder(nil); got != nil {
		t.Error("expected nil predicate for a nil config")
	}
}

// The end the user actually sees: a file that still exists on disk but now sits
// in an excluded folder is pruned, and its neighbours are not.
func TestPruneFileSourcesExcludedFolder(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "obsidian", "system")

	vault := t.TempDir()
	keep := filepath.Join(vault, "Notes")
	drop := filepath.Join(vault, "_Claude Sessions")
	os.MkdirAll(keep, 0o755)
	os.MkdirAll(drop, 0o755)

	keptFile := filepath.Join(keep, "real.md")
	excludedFile := filepath.Join(drop, "session.md")
	os.WriteFile(keptFile, []byte("# real"), 0o644)
	os.WriteFile(excludedFile, []byte("# session"), 0o644)

	for _, p := range []string{keptFile, excludedFile} {
		if _, err := conn.Exec(
			"INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) VALUES (?, 'markdown', ?, datetime('now'))",
			collID, p,
		); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{
		ObsidianVaults:         []string{vault},
		ObsidianExcludeFolders: []string{"_Claude Sessions"},
	}
	result := pruneFileSources(conn, collID, obsidianExcluder(cfg))

	if result.Checked != 2 {
		t.Errorf("Checked = %d, want 2", result.Checked)
	}
	if result.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1", result.Pruned)
	}

	var remaining string
	if err := conn.QueryRow("SELECT source_path FROM sources WHERE collection_id = ?", collID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != keptFile {
		t.Errorf("surviving source = %q, want %q", remaining, keptFile)
	}

	// Without the predicate the excluded file is untouched -- this is the
	// pre-existing behaviour that left it stranded.
	result = pruneFileSources(conn, collID, nil)
	if result.Pruned != 0 {
		t.Errorf("Pruned = %d with no predicate, want 0", result.Pruned)
	}
}
