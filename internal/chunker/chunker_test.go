package chunker

import (
	"strings"
	"testing"
)

func TestWordCount(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  spaced   out  ", 2},
		{"one two three four five", 5},
	}
	for _, tt := range tests {
		got := WordCount(tt.input)
		if got != tt.want {
			t.Errorf("WordCount(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestSplitIntoWindows(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got := SplitIntoWindows("", 10, 2)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("fits in one window", func(t *testing.T) {
		got := SplitIntoWindows("a b c", 10, 2)
		if len(got) != 1 || got[0] != "a b c" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("splits with overlap", func(t *testing.T) {
		// 10 words, chunk_size=5, overlap=2 → step=3
		text := "a b c d e f g h i j"
		got := SplitIntoWindows(text, 5, 2)
		if len(got) < 2 {
			t.Fatalf("expected at least 2 windows, got %d", len(got))
		}
		// First window should have 5 words.
		if WordCount(got[0]) != 5 {
			t.Errorf("first window has %d words, want 5", WordCount(got[0]))
		}
	})
}

func TestChunkMarkdownSimple(t *testing.T) {
	text := "Just a simple paragraph."
	chunks := ChunkMarkdown(text, "test.md", 500, 50)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "Just a simple paragraph." {
		t.Errorf("unexpected text: %q", chunks[0].Text)
	}
	if chunks[0].Title != "test.md" {
		t.Errorf("title = %q, want test.md", chunks[0].Title)
	}
}

func TestChunkMarkdownWithHeadings(t *testing.T) {
	text := `# Introduction

This is the intro.

## Background

Some background info.

## Methods

### Data Collection

How we collected data.`

	chunks := ChunkMarkdown(text, "paper.md", 500, 50)

	// Should have at least 3 sections (Introduction, Background, Data Collection).
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	// Check heading path is preserved.
	found := false
	for _, c := range chunks {
		if hp, ok := c.Metadata["heading_path"].(string); ok {
			if strings.Contains(hp, "Methods") && strings.Contains(hp, "Data Collection") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected to find chunk with heading path containing Methods > Data Collection")
	}
}

func TestChunkMarkdownEmpty(t *testing.T) {
	chunks := ChunkMarkdown("", "empty.md", 500, 50)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestChunkMarkdownLargeSection(t *testing.T) {
	// Create a section with more than 10 words.
	words := make([]string, 30)
	for i := range words {
		words[i] = "word"
	}
	text := "# Heading\n\n" + strings.Join(words, " ")

	chunks := ChunkMarkdown(text, "test.md", 10, 2)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks for long section, got %d", len(chunks))
	}
	// All chunks should have the heading prefix.
	for _, c := range chunks {
		if !strings.HasPrefix(c.Text, "[Heading] ") {
			t.Errorf("chunk missing heading prefix: %q", c.Text[:50])
		}
	}
}

func TestChunkEmailShort(t *testing.T) {
	chunks := ChunkEmail("Hello", "Short body.", 500, 50)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Title != "Hello" {
		t.Errorf("title = %q, want Hello", chunks[0].Title)
	}
}

func TestChunkEmailEmpty(t *testing.T) {
	chunks := ChunkEmail("Subject", "", 500, 50)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "Subject: Subject" {
		t.Errorf("text = %q, want 'Subject: Subject'", chunks[0].Text)
	}
}

func TestChunkEmailNoSubject(t *testing.T) {
	chunks := ChunkEmail("", "", 500, 50)
	if chunks[0].Title != "(no subject)" {
		t.Errorf("title = %q, want (no subject)", chunks[0].Title)
	}
}

func TestChunkEmailParagraphs(t *testing.T) {
	// Create body with several paragraphs exceeding chunk size.
	para := strings.Repeat("word ", 20)
	body := para + "\n\n" + para + "\n\n" + para

	chunks := ChunkEmail("Long email", body, 25, 5)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestChunkPlain(t *testing.T) {
	t.Run("short text", func(t *testing.T) {
		chunks := ChunkPlain("Hello world", "test.txt", 500, 50)
		if len(chunks) != 1 {
			t.Fatalf("expected 1 chunk, got %d", len(chunks))
		}
	})

	t.Run("empty text", func(t *testing.T) {
		chunks := ChunkPlain("", "test.txt", 500, 50)
		if len(chunks) != 1 || chunks[0].Text != "" {
			t.Error("expected single empty chunk")
		}
	})

	t.Run("long text splits", func(t *testing.T) {
		words := make([]string, 30)
		for i := range words {
			words[i] = "word"
		}
		text := strings.Join(words, " ")
		chunks := ChunkPlain(text, "test.txt", 10, 2)
		if len(chunks) < 2 {
			t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
		}
	})
}

func TestChunkIndexesAreSequential(t *testing.T) {
	words := make([]string, 100)
	for i := range words {
		words[i] = "word"
	}
	text := "# Section 1\n\n" + strings.Join(words, " ") + "\n\n# Section 2\n\n" + strings.Join(words, " ")

	chunks := ChunkMarkdown(text, "test.md", 20, 5)
	for i, c := range chunks {
		if c.ChunkIndex != i {
			t.Errorf("chunk %d has ChunkIndex %d", i, c.ChunkIndex)
		}
	}
}
