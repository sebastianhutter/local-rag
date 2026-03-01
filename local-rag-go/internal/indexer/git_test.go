package indexer

import (
	"testing"
)

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		path    string
		exclude bool
	}{
		{"main.go", false},
		{"src/app.py", false},
		{".DS_Store", true},
		{"go.sum", true},
		{"package-lock.json", true},
		{"node_modules/foo/bar.js", true},
		{"__pycache__/mod.pyc", true},
		{".terraform/providers/aws.tf", true},
		{"vendor/lib/file.go", true},
		{"src/main.rs", false},
		{".terraform.lock.hcl", true},
		{"uv.lock", true},
	}
	for _, tt := range tests {
		if got := shouldExclude(tt.path); got != tt.exclude {
			t.Errorf("shouldExclude(%q) = %v, want %v", tt.path, got, tt.exclude)
		}
	}
}

func TestParseWatermarks(t *testing.T) {
	// Empty string
	wm := parseWatermarks("")
	if len(wm) != 0 {
		t.Errorf("expected empty map, got %v", wm)
	}

	// JSON format
	wm = parseWatermarks(`{"/path/repo":"abc123","/path/repo:history":"def456"}`)
	if wm["/path/repo"] != "abc123" {
		t.Errorf("expected 'abc123', got %q", wm["/path/repo"])
	}
	if wm["/path/repo:history"] != "def456" {
		t.Errorf("expected 'def456', got %q", wm["/path/repo:history"])
	}

	// Legacy format
	wm = parseWatermarks("git:/path/repo:abc123")
	if wm["/path/repo"] != "abc123" {
		t.Errorf("expected 'abc123', got %q", wm["/path/repo"])
	}

	// Invalid
	wm = parseWatermarks("random text")
	if len(wm) != 0 {
		t.Errorf("expected empty map for invalid input, got %v", wm)
	}
}

func TestMakeWatermarks(t *testing.T) {
	wm := map[string]string{
		"/path/repo": "abc123",
	}
	result := makeWatermarks(wm)
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Parse it back
	parsed := parseWatermarks(result)
	if parsed["/path/repo"] != "abc123" {
		t.Errorf("round-trip failed: got %q", parsed["/path/repo"])
	}
}

func TestShouldIndexFile(t *testing.T) {
	tests := []struct {
		path  string
		index bool
	}{
		{"main.go", true},
		{"app.py", true},
		{"script.js", true},
		{"infra.tf", true},
		{"README.md", true},   // .md is in CodeExtensionMap (markdown via tree-sitter)
		{"go.sum", false},     // excluded
		{".DS_Store", false},  // excluded
		{"Makefile", true},
	}
	for _, tt := range tests {
		if got := shouldIndexFile(tt.path); got != tt.index {
			t.Errorf("shouldIndexFile(%q) = %v, want %v", tt.path, got, tt.index)
		}
	}
}

func TestIsGitRepo(t *testing.T) {
	// Current repo should be a git repo (we're inside local-rag-go)
	if !isGitRepo(".") {
		t.Error("expected current directory to be a git repo")
	}

	tmpDir := t.TempDir()
	if isGitRepo(tmpDir) {
		t.Error("temp dir should not be a git repo")
	}
}
