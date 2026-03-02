package search

import (
	"math"
	"testing"
)

func TestEscapeFTSQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", `"hello"`},
		{"hello world", `"hello" "world"`},
		{"kubernetes deployment strategy", `"kubernetes" "deployment" "strategy"`},
	}
	for _, tt := range tests {
		got := escapeFTSQuery(tt.input)
		if got != tt.want {
			t.Errorf("escapeFTSQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRRFMerge(t *testing.T) {
	vecResults := []rankedResult{
		{docID: 1, score: 0.1},
		{docID: 2, score: 0.2},
		{docID: 3, score: 0.3},
	}
	ftsResults := []rankedResult{
		{docID: 2, score: -5.0},
		{docID: 4, score: -3.0},
		{docID: 1, score: -1.0},
	}

	merged := RRFMerge(vecResults, ftsResults, 60, 0.7, 0.3)

	if len(merged) != 4 {
		t.Fatalf("expected 4 merged results, got %d", len(merged))
	}

	// Doc 1 and 2 should have highest scores (they appear in both lists).
	topTwo := map[int64]bool{}
	for _, r := range merged[:2] {
		topTwo[r.docID] = true
	}
	if !topTwo[1] || !topTwo[2] {
		t.Errorf("expected docs 1 and 2 in top 2, got %v", merged[:2])
	}

	// Verify scores are descending.
	for i := 1; i < len(merged); i++ {
		if merged[i].score > merged[i-1].score {
			t.Errorf("scores not descending at position %d", i)
		}
	}
}

func TestRRFMergeScoreFormula(t *testing.T) {
	// Verify exact RRF formula: weight / (k + rank + 1)
	vecResults := []rankedResult{{docID: 1, score: 0.0}}
	ftsResults := []rankedResult{{docID: 1, score: 0.0}}

	k := 60
	vw := 0.7
	fw := 0.3

	merged := RRFMerge(vecResults, ftsResults, k, vw, fw)

	expected := vw/float64(k+0+1) + fw/float64(k+0+1)
	if math.Abs(merged[0].score-expected) > 1e-10 {
		t.Errorf("score = %f, want %f", merged[0].score, expected)
	}
}

func TestRRFMergeEmpty(t *testing.T) {
	merged := RRFMerge(nil, nil, 60, 0.7, 0.3)
	if len(merged) != 0 {
		t.Errorf("expected empty, got %d", len(merged))
	}
}

func TestFiltersHasFilters(t *testing.T) {
	var nilFilters *Filters
	if nilFilters.hasFilters() {
		t.Error("nil filters should return false")
	}

	empty := &Filters{}
	if empty.hasFilters() {
		t.Error("empty filters should return false")
	}

	withCollection := &Filters{Collection: "email"}
	if !withCollection.hasFilters() {
		t.Error("filters with collection should return true")
	}

	withSender := &Filters{Sender: "alice@example.com"}
	if !withSender.hasFilters() {
		t.Error("filters with sender should return true")
	}
}
