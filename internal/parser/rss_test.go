package parser

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newRSSTestDB creates a NetNewsWire-shaped DB.sqlite3 in a temp account dir.
// withInlineAuthors selects the modern schema (articles.authors JSON column)
// versus the legacy authors/authorsLookup tables.
func newRSSTestDB(t *testing.T, withInlineAuthors bool) string {
	t.Helper()

	dir := t.TempDir()
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "DB.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()

	authorsCol := ""
	if withInlineAuthors {
		authorsCol = ", authors TEXT"
	}
	_, err = conn.Exec(`CREATE TABLE articles (
		articleID TEXT NOT NULL PRIMARY KEY, feedID TEXT NOT NULL, uniqueID TEXT NOT NULL,
		title TEXT, contentHTML TEXT, contentText TEXT, url TEXT, externalURL TEXT,
		summary TEXT, imageURL TEXT, bannerImageURL TEXT, datePublished DATE, dateModified DATE` + authorsCol + `)`)
	if err != nil {
		t.Fatalf("create articles: %v", err)
	}

	if withInlineAuthors {
		_, err = conn.Exec(
			`INSERT INTO articles (articleID, feedID, uniqueID, title, contentText, datePublished, authors)
			 VALUES ('a1', 'f1', 'u1', 'Hello', 'Body text', 1786871516, ?)`,
			`[{"name":"Jane Doe","authorID":"x1"},{"name":"","authorID":"x2"}]`)
		if err != nil {
			t.Fatalf("insert article: %v", err)
		}
	} else {
		_, err = conn.Exec(
			`INSERT INTO articles (articleID, feedID, uniqueID, title, contentText, datePublished)
			 VALUES ('a1', 'f1', 'u1', 'Hello', 'Body text', 1786871516)`)
		if err != nil {
			t.Fatalf("insert article: %v", err)
		}
		for _, stmt := range []string{
			`CREATE TABLE authors (authorID TEXT NOT NULL PRIMARY KEY, name TEXT, url TEXT, avatarURL TEXT, emailAddress TEXT)`,
			`CREATE TABLE authorsLookup (authorID TEXT NOT NULL, articleID TEXT NOT NULL, PRIMARY KEY (authorID, articleID))`,
			`INSERT INTO authors (authorID, name) VALUES ('x1', 'Jane Doe')`,
			`INSERT INTO authorsLookup (authorID, articleID) VALUES ('x1', 'a1')`,
		} {
			if _, err := conn.Exec(stmt); err != nil {
				t.Fatalf("legacy authors setup: %v", err)
			}
		}
	}

	return dir
}

func TestParseArticlesAuthors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inline bool
	}{
		{"inline authors column", true},
		{"legacy lookup tables", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			articles, err := ParseArticles(newRSSTestDB(t, tc.inline), 0)
			if err != nil {
				t.Fatalf("ParseArticles: %v", err)
			}
			if len(articles) != 1 {
				t.Fatalf("got %d articles, want 1", len(articles))
			}
			got := articles[0].Authors
			if len(got) != 1 || got[0] != "Jane Doe" {
				t.Errorf("authors = %v, want [Jane Doe]", got)
			}
		})
	}
}

// The watermark filter must include articles published in the same second as
// the watermark, otherwise siblings arriving later are lost forever.
func TestParseArticlesSinceIsInclusive(t *testing.T) {
	dir := newRSSTestDB(t, true)

	articles, err := ParseArticles(dir, 1786871516)
	if err != nil {
		t.Fatalf("ParseArticles: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("got %d articles at the watermark second, want 1", len(articles))
	}

	articles, err = ParseArticles(dir, 1786871517)
	if err != nil {
		t.Fatalf("ParseArticles: %v", err)
	}
	if len(articles) != 0 {
		t.Fatalf("got %d articles past the watermark, want 0", len(articles))
	}
}

func TestParseInlineAuthors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"malformed", "not json", nil},
		{"empty array", "[]", nil},
		{"names only", `[{"name":"A"},{"name":"B"}]`, []string{"A", "B"}},
		{"skips blank names", `[{"name":"  "},{"name":"B"}]`, []string{"B"}},
		{"ignores extra fields", `[{"name":"A","authorID":"z","url":"http://x"}]`, []string{"A"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseInlineAuthors(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
