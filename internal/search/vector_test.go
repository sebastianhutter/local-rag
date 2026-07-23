package search

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
)

func TestSquaredL2(t *testing.T) {
	tests := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{0, 0}, []float32{0, 0}, 0},
		{[]float32{1, 0}, []float32{0, 0}, 1},
		{[]float32{3, 4}, []float32{0, 0}, 25},
		{[]float32{1, 2, 3}, []float32{1, 2, 3}, 0},
	}
	for _, tt := range tests {
		if got := squaredL2(tt.a, tt.b); got != tt.want {
			t.Errorf("squaredL2(%v,%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
	if got := squaredL2([]float32{1, 2}, []float32{1}); !math.IsInf(got, 1) {
		t.Errorf("mismatched lengths = %v, want +Inf", got)
	}
}

// TestVectorSearchRerank verifies the binary-quantize → rerank path returns the
// nearest documents (by exact L2) in order, across a dimension divisible by 8.
func TestVectorSearchRerank(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rag.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.InitSchema(conn, 8); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(
		`INSERT INTO collections (id, name, collection_type) VALUES (1, 'test', 'project')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(
		`INSERT INTO sources (id, collection_id, source_type, source_path) VALUES (1, 1, 'txt', '/x')`,
	); err != nil {
		t.Fatal(err)
	}

	// Four documents with distinct 8-d embeddings.
	docs := map[int64][]float32{
		10: {1, 1, 0, 0, 0, 0, 0, 0},
		11: {0, 1, 1, 0, 0, 0, 0, 0},
		12: {0, 0, 0, 0, 1, 1, 1, 1},
		13: {-1, -1, -1, 0, 0, 0, 0, 0},
	}
	for docID, vec := range docs {
		if _, err := conn.Exec(
			`INSERT INTO documents (id, source_id, collection_id, chunk_index, content) VALUES (?, 1, 1, ?, 'c')`,
			docID, docID,
		); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertEmbedding(conn, docID, embeddings.SerializeFloat32(vec)); err != nil {
			t.Fatal(err)
		}
	}

	// Query closest to doc 10.
	query := []float32{1, 1, 0, 0, 0, 0, 0, 0}
	results, err := vectorSearch(conn, query, 3, &Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no vector results")
	}
	if results[0].docID != 10 {
		t.Errorf("nearest = doc %d, want 10", results[0].docID)
	}
	// Results must be sorted ascending by distance.
	for i := 1; i < len(results); i++ {
		if results[i].score < results[i-1].score {
			t.Errorf("results not sorted by distance: %v", results)
		}
	}
}
