package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func setupVecDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := InitSchema(conn, 8); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// Orphaned embeddings accumulate whenever documents are removed without their
// vectors — the vec0 tables have no foreign keys, so a cascade leaves them.
func TestOrphanedVectorLifecycle(t *testing.T) {
	conn := setupVecDB(t)

	conn.Exec("INSERT INTO collections (name, collection_type) VALUES ('c','project')")
	conn.Exec("INSERT INTO sources (collection_id, source_type, source_path) VALUES (1,'md','/a')")

	vec := make([]byte, 8*4)
	var keep, drop []int64
	for i := 0; i < 6; i++ {
		res, err := conn.Exec("INSERT INTO documents (source_id, collection_id, chunk_index, content) VALUES (1,1,?, 'x')", i)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if err := InsertEmbedding(conn, id, vec); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			keep = append(keep, id)
		} else {
			drop = append(drop, id)
		}
	}

	stats, err := CountOrphanedVectors(conn)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Orphaned != 0 {
		t.Fatalf("clean database reports %d orphans", stats.Orphaned)
	}

	// Delete documents WITHOUT touching vectors — the leak this cleans up.
	for _, id := range drop {
		if _, err := conn.Exec("DELETE FROM documents WHERE id = ?", id); err != nil {
			t.Fatal(err)
		}
	}

	stats, _ = CountOrphanedVectors(conn)
	if stats.Orphaned != len(drop) {
		t.Errorf("got %d orphans, want %d", stats.Orphaned, len(drop))
	}

	deleted, err := DeleteOrphanedVectors(conn)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != len(drop) {
		t.Errorf("deleted %d, want %d", deleted, len(drop))
	}

	// The surviving documents must keep their embeddings, in both tables.
	var vecs, bins int
	conn.QueryRow("SELECT COUNT(*) FROM vec_documents").Scan(&vecs)
	conn.QueryRow("SELECT COUNT(*) FROM vec_documents_bin").Scan(&bins)
	if vecs != len(keep) || bins != len(keep) {
		t.Errorf("got %d float / %d binary vectors, want %d each", vecs, bins, len(keep))
	}
	for _, id := range keep {
		var n int
		conn.QueryRow("SELECT COUNT(*) FROM vec_documents WHERE document_id = ?", id).Scan(&n)
		if n != 1 {
			t.Errorf("surviving document %d has %d vectors, want 1", id, n)
		}
	}

	// Idempotent.
	if again, _ := DeleteOrphanedVectors(conn); again != 0 {
		t.Errorf("second pass deleted %d, want 0", again)
	}
}
