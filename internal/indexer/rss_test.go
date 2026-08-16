package indexer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

func testRSSConfig(batchSize int) *config.Config {
	return &config.Config{
		EmbeddingBatchSize: batchSize,
		ChunkSizeTokens:    500,
		ChunkOverlapTokens: 50,
	}
}

func makeArticles(n int, body string) []*parser.Article {
	articles := make([]*parser.Article, n)
	for i := range articles {
		articles[i] = &parser.Article{
			ArticleID: fmt.Sprintf("a%d", i),
			Title:     fmt.Sprintf("Article %d", i),
			BodyText:  body,
		}
	}
	return articles
}

// Every batch must be internally consistent: texts flattened in chunk order,
// and one text per chunk. writeRSSBatch slices the returned vectors by these
// offsets, so a mismatch would attach the wrong embedding to a document.
func assertBatchesConsistent(t *testing.T, batches []*rssBatch, wantArticles int) {
	t.Helper()

	seen := 0
	for bi, b := range batches {
		if len(b.articles) != len(b.chunks) {
			t.Fatalf("batch %d: %d articles but %d chunk sets", bi, len(b.articles), len(b.chunks))
		}
		total := 0
		for _, chunks := range b.chunks {
			total += len(chunks)
		}
		if total != len(b.texts) {
			t.Fatalf("batch %d: %d chunks but %d texts", bi, total, len(b.texts))
		}

		// texts must be the chunk texts, in order.
		pos := 0
		for _, chunks := range b.chunks {
			for _, c := range chunks {
				if b.texts[pos] != c.Text {
					t.Fatalf("batch %d: text at %d does not match its chunk", bi, pos)
				}
				pos++
			}
		}
		seen += len(b.articles)
	}

	if seen != wantArticles {
		t.Errorf("batches cover %d articles, want %d", seen, wantArticles)
	}
}

func TestBuildRSSBatches(t *testing.T) {
	articles := makeArticles(10, "short body text")
	batches := buildRSSBatches(articles, testRSSConfig(4))

	assertBatchesConsistent(t, batches, 10)

	// Short articles chunk to one text each, so a target of 4 gives 4/4/2.
	if len(batches) != 3 {
		t.Fatalf("got %d batches, want 3", len(batches))
	}
	for i, want := range []int{4, 4, 2} {
		if got := len(batches[i].texts); got != want {
			t.Errorf("batch %d has %d texts, want %d", i, got, want)
		}
	}
}

// An article's chunks must never be split across batches, even when the article
// alone exceeds the batch target — writeRSSBatch assumes one article's vectors
// are contiguous within a single batch.
func TestBuildRSSBatchesKeepsArticleChunksTogether(t *testing.T) {
	long := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit ", 500)
	articles := makeArticles(3, long)

	batches := buildRSSBatches(articles, testRSSConfig(2))
	assertBatchesConsistent(t, batches, 3)

	multi := false
	for _, b := range batches {
		for _, chunks := range b.chunks {
			if len(chunks) > 1 {
				multi = true
			}
		}
	}
	if !multi {
		t.Skip("chunker produced one chunk per article; nothing to verify")
	}
}

func TestBuildRSSBatchesSkipsEmptyArticles(t *testing.T) {
	articles := []*parser.Article{
		{ArticleID: "a1", Title: "Real", BodyText: "has content"},
		{ArticleID: "a2", Title: "", BodyText: ""},
		{ArticleID: "a3", Title: "Also real", BodyText: "more content"},
	}

	batches := buildRSSBatches(articles, testRSSConfig(32))

	total := 0
	for _, b := range batches {
		total += len(b.articles)
		for _, a := range b.articles {
			if a.ArticleID == "a2" {
				t.Error("article with no chunks should be dropped")
			}
		}
	}
	if total != 2 {
		t.Errorf("got %d articles batched, want 2", total)
	}
}

func TestBuildRSSBatchesEmptyInput(t *testing.T) {
	if got := buildRSSBatches(nil, testRSSConfig(32)); len(got) != 0 {
		t.Errorf("got %d batches for no articles, want 0", len(got))
	}
}

// A failed embedding call must be charged to every article in the batch, so the
// run's totals still add up.
func TestWriteRSSBatchEmbedError(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "rss", "system")

	b := &rssBatch{
		articles: makeArticles(3, "body"),
		err:      fmt.Errorf("ollama unreachable"),
	}
	result := &IndexResult{}
	writeRSSBatch(conn, collID, b, result)

	if result.Errors != 3 {
		t.Errorf("got %d errors, want 3 (one per article)", result.Errors)
	}
	if result.Indexed != 0 {
		t.Errorf("got %d indexed, want 0", result.Indexed)
	}
}

// If Ollama returns a different number of vectors than we sent texts, the
// offsets are meaningless — the batch must be rejected rather than store
// mismatched embeddings.
func TestWriteRSSBatchVectorCountMismatch(t *testing.T) {
	conn := setupTestDB(t)
	collID := getOrCreate(conn, "rss", "system")

	articles := makeArticles(2, "body")
	b := buildRSSBatches(articles, testRSSConfig(32))[0]
	b.vecs = [][]float32{{1, 2, 3}} // fewer vectors than texts

	result := &IndexResult{}
	writeRSSBatch(conn, collID, b, result)

	if result.Indexed != 0 {
		t.Errorf("got %d indexed, want 0 on a count mismatch", result.Indexed)
	}
	if result.Errors == 0 {
		t.Error("count mismatch should be reported as an error")
	}
}
