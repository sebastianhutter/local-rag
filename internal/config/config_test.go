package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Save() overlays a fixed list of keys, so a new option that is not added there
// loads correctly but is silently dropped the first time the GUI saves settings.
func TestNewOptionsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// Absent from the file: defaults apply. skip_cloud_placeholders defaults to
	// true, which a plain zero value would get wrong.
	os.WriteFile(path, []byte(`{"embedding_model":"bge-m3"}`), 0o644)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SkipCloudPlaceholders {
		t.Error("skip_cloud_placeholders should default to true when absent")
	}
	if cfg.EmbeddingWorkers != 4 {
		t.Errorf("embedding_workers = %d, want 4", cfg.EmbeddingWorkers)
	}

	// An explicit false must not be overwritten by the default.
	os.WriteFile(path, []byte(`{"skip_cloud_placeholders":false,"embedding_workers":8}`), 0o644)
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkipCloudPlaceholders {
		t.Error("explicit false should be honoured")
	}

	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, key := range []string{"skip_cloud_placeholders", "embedding_workers"} {
		if !strings.Contains(string(data), key) {
			t.Errorf("Save() did not write %q:\n%s", key, data)
		}
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SkipCloudPlaceholders {
		t.Error("skip_cloud_placeholders did not survive save/load")
	}
	if reloaded.EmbeddingWorkers != 8 {
		t.Errorf("embedding_workers = %d after round trip, want 8", reloaded.EmbeddingWorkers)
	}
}

func TestDefaults(t *testing.T) {
	cfg := defaults()

	if cfg.EmbeddingModel != "bge-m3" {
		t.Errorf("EmbeddingModel = %q, want bge-m3", cfg.EmbeddingModel)
	}
	if cfg.EmbeddingDimensions != 1024 {
		t.Errorf("EmbeddingDimensions = %d, want 1024", cfg.EmbeddingDimensions)
	}
	if cfg.ChunkSizeTokens != 500 {
		t.Errorf("ChunkSizeTokens = %d, want 500", cfg.ChunkSizeTokens)
	}
	if cfg.ChunkOverlapTokens != 50 {
		t.Errorf("ChunkOverlapTokens = %d, want 50", cfg.ChunkOverlapTokens)
	}
	if cfg.SearchDefaults.TopK != 10 {
		t.Errorf("TopK = %d, want 10", cfg.SearchDefaults.TopK)
	}
	if cfg.SearchDefaults.RRFK != 60 {
		t.Errorf("RRFK = %d, want 60", cfg.SearchDefaults.RRFK)
	}
	if cfg.SearchDefaults.VectorWeight != 0.7 {
		t.Errorf("VectorWeight = %f, want 0.7", cfg.SearchDefaults.VectorWeight)
	}
	if cfg.GUI.MCPPort != 31123 {
		t.Errorf("MCPPort = %d, want 31123", cfg.GUI.MCPPort)
	}
	if cfg.GitHistoryInMonths != 6 {
		t.Errorf("GitHistoryInMonths = %d, want 6", cfg.GitHistoryInMonths)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EmbeddingModel != "bge-m3" {
		t.Errorf("expected defaults when file missing")
	}
}

func TestLoadAndSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write a config with some custom values.
	original := defaults()
	original.EmbeddingModel = "mxbai-embed-large"
	original.ChunkSizeTokens = 300
	original.ObsidianVaults = []string{"/tmp/vault1"}
	original.Repositories = map[string][]string{
		"myorg": {"/tmp/repo1", "/tmp/repo2"},
	}
	original.DisabledCollections = []string{"rss"}
	original.SearchDefaults.VectorWeight = 0.6

	if err := Save(original, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load it back.
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.EmbeddingModel != "mxbai-embed-large" {
		t.Errorf("EmbeddingModel = %q, want mxbai-embed-large", loaded.EmbeddingModel)
	}
	if loaded.ChunkSizeTokens != 300 {
		t.Errorf("ChunkSizeTokens = %d, want 300", loaded.ChunkSizeTokens)
	}
	if len(loaded.ObsidianVaults) != 1 || loaded.ObsidianVaults[0] != "/tmp/vault1" {
		t.Errorf("ObsidianVaults = %v, want [/tmp/vault1]", loaded.ObsidianVaults)
	}
	if len(loaded.Repositories["myorg"]) != 2 {
		t.Errorf("Repositories[myorg] = %v, want 2 items", loaded.Repositories["myorg"])
	}
	if loaded.SearchDefaults.VectorWeight != 0.6 {
		t.Errorf("VectorWeight = %f, want 0.6", loaded.SearchDefaults.VectorWeight)
	}
}

func TestSavePreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write a JSON file with an unknown key.
	initial := map[string]any{
		"custom_field":      "preserve_me",
		"embedding_model":   "old-model",
		"chunk_size_tokens": 100,
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	cfg.EmbeddingModel = "new-model"
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}

	// Read raw JSON and check custom_field is preserved.
	raw, _ := os.ReadFile(path)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)

	if result["custom_field"] != "preserve_me" {
		t.Error("unknown key custom_field was not preserved")
	}
	if result["embedding_model"] != "new-model" {
		t.Error("embedding_model was not updated")
	}
}

func TestExpandPath(t *testing.T) {
	home := homeDir()
	tests := []struct {
		input string
		want  string
	}{
		{"~/Documents", filepath.Join(home, "Documents")},
		{"~", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}
	for _, tt := range tests {
		got := expandPath(tt.input)
		if got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsCollectionEnabled(t *testing.T) {
	cfg := defaults()
	cfg.DisabledCollections = []string{"rss", "email"}

	if !cfg.IsCollectionEnabled("obsidian") {
		t.Error("obsidian should be enabled")
	}
	if cfg.IsCollectionEnabled("rss") {
		t.Error("rss should be disabled")
	}
	if cfg.IsCollectionEnabled("email") {
		t.Error("email should be disabled")
	}
}
