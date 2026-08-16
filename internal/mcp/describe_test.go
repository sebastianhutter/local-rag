package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Code collections store git watermarks in the description column. Returning
// them verbatim made rag_list_collections cost tens of thousands of tokens of
// an MCP client's context for a single call.
func TestDescribeCollection(t *testing.T) {
	watermarks := map[string]string{
		"/repos/alpha":         "abc123",
		"/repos/alpha:history": "abc123",
		"/repos/beta":          "def456",
		"/repos/gamma":         "ghi789",
	}
	raw, _ := json.Marshal(watermarks)

	text, repos := describeCollection(string(raw))
	if text != "" {
		t.Errorf("watermarks should not be returned as text, got %q", text)
	}
	if repos != 3 {
		t.Errorf("got %d repositories, want 3 (the :history key is the same repo)", repos)
	}
}

func TestDescribeCollectionHumanText(t *testing.T) {
	const human = "RSS articles from NetNewsWire (indexed through 2026-08-16T09:11:56Z)"
	text, repos := describeCollection(human)
	if text != human {
		t.Errorf("human description should pass through, got %q", text)
	}
	if repos != 0 {
		t.Errorf("got %d repositories, want 0", repos)
	}
}

func TestDescribeCollectionEmpty(t *testing.T) {
	text, repos := describeCollection("")
	if text != "" || repos != 0 {
		t.Errorf("got (%q, %d), want empty", text, repos)
	}
}

// A description that merely starts with '{' but is not a watermark map must not
// be silently swallowed.
func TestDescribeCollectionNonWatermarkJSON(t *testing.T) {
	const odd = "{not really json"
	text, _ := describeCollection(odd)
	if text != odd {
		t.Errorf("unparseable description should pass through, got %q", text)
	}
}

// Guard the size problem itself.
func TestDescribeCollectionShrinksLargePayload(t *testing.T) {
	watermarks := map[string]string{}
	for i := 0; i < 200; i++ {
		watermarks[strings.Repeat("/some/long/repository/path", 4)+string(rune('a'+i%26))+string(rune('a'+i/26))] = strings.Repeat("f", 40)
	}
	raw, _ := json.Marshal(watermarks)
	if len(raw) < 20000 {
		t.Fatalf("test payload too small: %d", len(raw))
	}
	text, repos := describeCollection(string(raw))
	if len(text) != 0 {
		t.Errorf("large watermark payload leaked %d chars into the result", len(text))
	}
	if repos == 0 {
		t.Error("expected a repository count")
	}
}
