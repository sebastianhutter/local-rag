package indexer

import (
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

func TestArticleToItem(t *testing.T) {
	cfg := testBatchConfig(32)

	article := &parser.Article{
		ArticleID:     "feed/1/article/9",
		Title:         "A Title",
		BodyText:      "some body text",
		URL:           "https://example.com/9",
		FeedName:      "Example Feed",
		FeedCategory:  "Tech",
		Authors:       []string{"Jane Doe"},
		DatePublished: "2026-08-16T09:11:56Z",
	}

	item := articleToItem(article, cfg)

	if item.SourcePath != "feed/1/article/9" {
		t.Errorf("SourcePath = %q, want the article ID", item.SourcePath)
	}
	if item.Title != "A Title" {
		t.Errorf("Title = %q", item.Title)
	}
	if len(item.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for key, want := range map[string]any{
		"url":           "https://example.com/9",
		"feed_name":     "Example Feed",
		"date":          "2026-08-16T09:11:56Z",
		"feed_category": "Tech",
	} {
		if got := item.Metadata[key]; got != want {
			t.Errorf("metadata[%q] = %v, want %v", key, got, want)
		}
	}

	authors, ok := item.Metadata["authors"].([]string)
	if !ok || len(authors) != 1 || authors[0] != "Jane Doe" {
		t.Errorf("metadata[authors] = %v, want [Jane Doe]", item.Metadata["authors"])
	}
}

// Optional fields must be left out entirely rather than stored as empty values,
// so metadata filters do not match on blanks.
func TestArticleToItemOmitsEmptyOptionalFields(t *testing.T) {
	item := articleToItem(&parser.Article{
		ArticleID: "a1",
		Title:     "No extras",
		BodyText:  "body",
	}, testBatchConfig(32))

	if _, ok := item.Metadata["feed_category"]; ok {
		t.Error("empty feed_category should be omitted")
	}
	if _, ok := item.Metadata["authors"]; ok {
		t.Error("empty authors should be omitted")
	}
}

func TestArticleToItemUntitled(t *testing.T) {
	item := articleToItem(&parser.Article{
		ArticleID: "a1",
		BodyText:  "body with no title",
	}, testBatchConfig(32))

	if item.Title != "(no title)" {
		t.Errorf("Title = %q, want the untitled placeholder", item.Title)
	}
}

// Every item must carry a source_type: it is stored on the sources row and is
// what `--type` / the MCP source_type filter matches on. An empty one silently
// removes the whole source from those filters.
func TestArticleToItemSetsSourceType(t *testing.T) {
	item := articleToItem(&parser.Article{
		ArticleID: "a1", Title: "T", BodyText: "body",
	}, testBatchConfig(32))

	if item.SourceType != "rss" {
		t.Errorf("SourceType = %q, want rss", item.SourceType)
	}
}
