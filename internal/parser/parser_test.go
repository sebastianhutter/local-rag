package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceTypeForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"notes/hello.md", "markdown"},
		{"doc.PDF", "pdf"},
		{"file.docx", "docx"},
		{"page.html", "html"},
		{"page.HTM", "html"},
		{"readme.txt", "plaintext"},
		{"data.csv", "plaintext"},
		{"book.epub", "epub"},
		{"image.png", ""},
		{"noext", ""},
	}

	for _, tt := range tests {
		got := SourceTypeForPath(tt.path)
		if got != tt.want {
			t.Errorf("SourceTypeForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple paragraph",
			input: "<p>Hello, <b>world</b>!</p>",
			want:  "Hello,\nworld\n!",
		},
		{
			name:  "strips scripts",
			input: "<p>Text</p><script>alert('x')</script><p>More</p>",
			want:  "Text\nMore",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \n  ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HTMLToText(tt.input)
			if got != tt.want {
				t.Errorf("HTMLToText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParsePlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "Hello, world!\nSecond line."
	os.WriteFile(path, []byte(content), 0644)

	got := ParsePlaintext(path)
	if got != content {
		t.Errorf("ParsePlaintext() = %q, want %q", got, content)
	}
}

func TestParsePlaintextMissing(t *testing.T) {
	got := ParsePlaintext("/nonexistent/file.txt")
	if got != "" {
		t.Errorf("ParsePlaintext(nonexistent) = %q, want empty", got)
	}
}

func TestParseMarkdown(t *testing.T) {
	input := `---
title: My Note
tags: [go, testing]
---

# Introduction

This is a [[wiki link]] to another note.

![[embedded image.png]]

Here is a #inline-tag.
`
	doc := ParseMarkdown(input, "my-note.md")

	if doc.Title != "My Note" {
		t.Errorf("Title = %q, want %q", doc.Title, "My Note")
	}

	if len(doc.Tags) < 3 {
		t.Errorf("Expected at least 3 tags (go, testing, inline-tag), got %v", doc.Tags)
	}

	if len(doc.Links) == 0 {
		t.Error("Expected at least one link from wikilink")
	}

	// Wikilinks should be converted to plain text.
	if containsString(doc.BodyText, "[[") {
		t.Error("Body should not contain raw wikilinks")
	}

	// Embeds should be removed.
	if containsString(doc.BodyText, "![[") {
		t.Error("Body should not contain embeds")
	}
}

func TestParseMarkdownNoFrontmatter(t *testing.T) {
	doc := ParseMarkdown("# Just a heading\n\nSome text.", "untitled.md")
	if doc.Title != "untitled" {
		t.Errorf("Title = %q, want %q", doc.Title, "untitled")
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.txt")
	os.WriteFile(existing, []byte("hi"), 0644)

	if !fileExists(existing) {
		t.Error("fileExists() returned false for existing file")
	}
	if fileExists(filepath.Join(dir, "nope.txt")) {
		t.Error("fileExists() returned true for nonexistent file")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
