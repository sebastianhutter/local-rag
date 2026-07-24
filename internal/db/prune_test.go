package db

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
)

func ser(vec []float32) []byte {
	buf := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// TestPruneSources verifies that PruneSources removes a source's rows from
// documents and BOTH vector tables in one shot, leaving other sources intact.
func TestPruneSources(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rag.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := InitSchema(conn, 8); err != nil {
		t.Fatal(err)
	}

	conn.Exec(`INSERT INTO collections (id, name, collection_type) VALUES (1, 'c', 'code')`)
	conn.Exec(`INSERT INTO sources (id, collection_id, source_type, source_path) VALUES
		(1, 1, 'code', '/a'), (2, 1, 'code', '/b')`)

	vec := ser(make([]float32, 8))
	docID := int64(0)
	insert := func(sourceID int64) {
		docID++
		if _, err := conn.Exec(
			`INSERT INTO documents (id, source_id, collection_id, chunk_index, content) VALUES (?, ?, 1, ?, 'x')`,
			docID, sourceID, docID,
		); err != nil {
			t.Fatal(err)
		}
		if err := InsertEmbedding(conn, docID, vec); err != nil {
			t.Fatal(err)
		}
	}
	insert(1)
	insert(1) // source 1 -> 2 docs
	insert(2) // source 2 -> 1 doc

	if err := PruneSources(conn, []int64{1}); err != nil {
		t.Fatal(err)
	}

	count := func(q string) int {
		var n int
		conn.QueryRow(q).Scan(&n)
		return n
	}
	if got := count(`SELECT COUNT(*) FROM sources`); got != 1 {
		t.Errorf("sources = %d, want 1", got)
	}
	if got := count(`SELECT COUNT(*) FROM documents`); got != 1 {
		t.Errorf("documents = %d, want 1 (only source 2's)", got)
	}
	if got := count(`SELECT COUNT(*) FROM vec_documents`); got != 1 {
		t.Errorf("vec_documents = %d, want 1", got)
	}
	if got := count(`SELECT COUNT(*) FROM vec_documents_bin`); got != 1 {
		t.Errorf("vec_documents_bin = %d, want 1", got)
	}
	// Empty input is a no-op.
	if err := PruneSources(conn, nil); err != nil {
		t.Errorf("PruneSources(nil) = %v, want nil", err)
	}
}
