package mcp

import (
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/config"
)

func TestCreateServer(t *testing.T) {
	s := CreateServer()
	if s == nil {
		t.Fatal("CreateServer returned nil")
	}
}

func TestBuildObsidianURI(t *testing.T) {
	uri := buildObsidianURI("/Users/me/Documents/MyVault/notes/test.md", "/Users/me/Documents/MyVault")
	if uri == "" {
		t.Fatal("expected non-empty URI")
	}
	if !contains(uri, "obsidian://open") {
		t.Errorf("expected obsidian:// URI, got %q", uri)
	}
	if !contains(uri, "vault=MyVault") {
		t.Errorf("expected vault name in URI, got %q", uri)
	}
	if !contains(uri, "file=notes") {
		t.Errorf("expected relative path in URI, got %q", uri)
	}
}

func TestBuildSourceURI(t *testing.T) {
	cfg := &dummyCfg{}

	// RSS → URL from metadata
	uri := buildSourceURI("some-id", "rss", "rss", map[string]any{"url": "https://example.com/article"}, cfg)
	if uri != "https://example.com/article" {
		t.Errorf("expected RSS URL, got %v", uri)
	}

	// Email → nil
	uri = buildSourceURI("msg-id", "email", "email", nil, cfg)
	if uri != nil {
		t.Errorf("expected nil for email, got %v", uri)
	}

	// Commit → nil
	uri = buildSourceURI("git://repo#sha", "commit", "code", nil, cfg)
	if uri != nil {
		t.Errorf("expected nil for commit, got %v", uri)
	}

	// Regular file → file:// URI
	uri = buildSourceURI("/path/to/doc.pdf", "pdf", "project", nil, cfg)
	if uri != "file:///path/to/doc.pdf" {
		t.Errorf("expected file:// URI, got %v", uri)
	}

	// Code → vscode URI
	uri = buildSourceURI("/path/to/main.go", "code", "mygroup", map[string]any{"start_line": float64(42)}, cfg)
	expected := "vscode://file/path/to/main.go:42"
	if uri != expected {
		t.Errorf("expected %q, got %v", expected, uri)
	}

	// Markdown with URL metadata → Atlassian URL
	uri = buildSourceURI("/path/to/jira-issue.md", "markdown", "atlassian", map[string]any{"url": "https://copebit.atlassian.net/browse/CB-123"}, cfg)
	if uri != "https://copebit.atlassian.net/browse/CB-123" {
		t.Errorf("expected Atlassian URL for markdown with url metadata, got %v", uri)
	}

	// Markdown without URL metadata → file:// URI
	uri = buildSourceURI("/path/to/note.md", "markdown", "obsidian", map[string]any{}, cfg)
	if uri != "file:///path/to/note.md" {
		t.Errorf("expected file:// URI for markdown without url metadata, got %v", uri)
	}

	// Calibre virtual path → nil
	uri = buildSourceURI("calibre:///lib/book", "calibre-description", "calibre", nil, cfg)
	if uri != nil {
		t.Errorf("expected nil for calibre, got %v", uri)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// dummyCfg satisfies the interface needed by buildSourceURI
type dummyCfg = config.Config
