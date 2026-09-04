package search

import (
	"database/sql"
	"math"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sebastianhutter/local-rag-go/internal/config"
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

	withMeta := &Filters{MetadataFilters: map[string]string{"source": "jira"}}
	if !withMeta.hasFilters() {
		t.Error("filters with metadata should return true")
	}

	emptyMeta := &Filters{MetadataFilters: map[string]string{}}
	if emptyMeta.hasFilters() {
		t.Error("filters with empty metadata map should return false")
	}
}

func TestApplyExcludeDefaults(t *testing.T) {
	cfgWith := &config.Config{}
	cfgWith.SearchDefaults.ExcludeCollections = []string{"claude-sessions"}
	cfgWithout := &config.Config{}

	t.Run("no config", func(t *testing.T) {
		in := &Filters{}
		if got := applyExcludeDefaults(in, nil); got != in {
			t.Error("nil config should return the filters unchanged")
		}
	})

	t.Run("no exclusions configured", func(t *testing.T) {
		in := &Filters{}
		if got := applyExcludeDefaults(in, cfgWithout); got != in {
			t.Error("empty exclusion list should return the filters unchanged")
		}
	})

	t.Run("applied when no collection named", func(t *testing.T) {
		got := applyExcludeDefaults(&Filters{SourceType: "markdown"}, cfgWith)
		if len(got.ExcludeCollections) != 1 || got.ExcludeCollections[0] != "claude-sessions" {
			t.Errorf("ExcludeCollections = %v, want [claude-sessions]", got.ExcludeCollections)
		}
		if got.SourceType != "markdown" {
			t.Errorf("other filters lost: SourceType = %q", got.SourceType)
		}
	})

	t.Run("nil filters still get the defaults", func(t *testing.T) {
		got := applyExcludeDefaults(nil, cfgWith)
		if got == nil || len(got.ExcludeCollections) != 1 {
			t.Errorf("applyExcludeDefaults(nil) = %#v", got)
		}
	})

	// An explicit collection is an explicit request, and a default must not
	// override it -- otherwise an excluded collection could never be searched.
	t.Run("explicit collection wins", func(t *testing.T) {
		in := &Filters{Collection: "claude-sessions"}
		got := applyExcludeDefaults(in, cfgWith)
		if len(got.ExcludeCollections) != 0 {
			t.Errorf("exclusions applied over an explicit collection: %v", got.ExcludeCollections)
		}
	})

	t.Run("caller's filters are not mutated", func(t *testing.T) {
		in := &Filters{}
		applyExcludeDefaults(in, cfgWith)
		if len(in.ExcludeCollections) != 0 {
			t.Errorf("caller's Filters was mutated: %v", in.ExcludeCollections)
		}
	})
}

func TestPassesFiltersExcludeCollections(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE collections (id INTEGER PRIMARY KEY, name TEXT, collection_type TEXT);
		CREATE TABLE sources (id INTEGER PRIMARY KEY, source_type TEXT, source_path TEXT);
		CREATE TABLE documents (id INTEGER PRIMARY KEY, source_id INTEGER, collection_id INTEGER, metadata TEXT);
		INSERT INTO collections VALUES (1,'obsidian','system'),(2,'claude-sessions','project');
		INSERT INTO sources VALUES (1,'markdown','/vault/note.md'),(2,'markdown','/vault/session.md');
		INSERT INTO documents VALUES (10,1,1,NULL),(20,2,2,NULL);
	`); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		docID   int64
		filters *Filters
		want    bool
	}{
		{"kept when nothing excluded", 20, &Filters{}, true},
		{"excluded by name", 20, &Filters{ExcludeCollections: []string{"claude-sessions"}}, false},
		{"other collection unaffected", 10, &Filters{ExcludeCollections: []string{"claude-sessions"}}, true},
		{"excluded by type", 20, &Filters{ExcludeCollections: []string{"project"}}, false},
		{"type exclusion spares a different type", 10, &Filters{ExcludeCollections: []string{"project"}}, true},
		{"unknown name excludes nothing", 20, &Filters{ExcludeCollections: []string{"nope"}}, true},
		{"explicit collection plus its own exclusion still filters", 20,
			&Filters{Collection: "claude-sessions", ExcludeCollections: []string{"claude-sessions"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := passesFilters(db, tt.docID, tt.filters); got != tt.want {
				t.Errorf("passesFilters(%d) = %v, want %v", tt.docID, got, tt.want)
			}
		})
	}
}
