package parser

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"howett.net/plist"
)

// Article is a parsed RSS article from NetNewsWire.
type Article struct {
	ArticleID      string
	Title          string
	BodyText       string
	URL            string
	FeedName       string
	FeedCategory   string
	Authors        []string
	DatePublished  string  // ISO datetime
	DatePublishedTS float64 // Unix timestamp
}

// FindRSSAccountDirs finds NetNewsWire account directories containing DB.sqlite3.
func FindRSSAccountDirs(basePath string) []string {
	var dirs []string
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return dirs
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(basePath, entry.Name())
		if fileExists(filepath.Join(dir, "DB.sqlite3")) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// ParseArticles parses RSS articles from a NetNewsWire account directory.
func ParseArticles(accountDir string, sinceTS float64) ([]*Article, error) {
	dbPath := filepath.Join(accountDir, "DB.sqlite3")
	if !fileExists(dbPath) {
		return nil, fmt.Errorf("DB.sqlite3 not found in %s", accountDir)
	}

	conn, err := openReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	feedIDMap := loadFeedIDMap(accountDir)
	authorsMap := loadRSSAuthors(conn)

	slog.Info("loaded RSS data",
		"feeds", len(feedIDMap),
		"authorSets", len(authorsMap),
	)

	query := "SELECT articleID, feedID, title, contentHTML, contentText, url, externalURL, summary, datePublished FROM articles"
	var args []any
	if sinceTS > 0 {
		// datePublished may be stored as datetime strings or as numeric timestamps.
		// Use datetime() to normalize both formats for comparison.
		sinceTime := time.Unix(int64(sinceTS), 0).UTC().Format("2006-01-02 15:04:05")
		query += " WHERE datetime(datePublished) > datetime(?)"
		args = append(args, sinceTime)
	}
	query += " ORDER BY datePublished ASC"

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query articles: %w", err)
	}
	defer rows.Close()

	articles := make([]*Article, 0)
	var errorCount int

	for rows.Next() {
		var articleID, feedID string
		var title, contentHTML, contentText, articleURL, externalURL, summary sql.NullString
		var datePublishedRaw any

		if err := rows.Scan(&articleID, &feedID, &title, &contentHTML, &contentText,
			&articleURL, &externalURL, &summary, &datePublishedRaw); err != nil {
			errorCount++
			if errorCount <= 10 {
				slog.Warn("error scanning article row", "err", err)
			}
			continue
		}

		datePublished := parseDatePublished(datePublishedRaw)

		article := rssRowToArticle(articleID, feedID, title, contentHTML, contentText,
			articleURL, externalURL, summary, datePublished, feedIDMap, authorsMap)
		if article != nil {
			articles = append(articles, article)
		}
	}

	slog.Info("parsed articles", "count", len(articles), "errors", errorCount)
	return articles, rows.Err()
}

// parseDatePublished converts the datePublished column value to a Unix timestamp.
// go-sqlite3 may return time.Time, float64, int64, or string depending on the stored format.
func parseDatePublished(raw any) float64 {
	if raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case time.Time:
		return float64(v.Unix())
	case float64:
		return v
	case int64:
		return float64(v)
	case string:
		if v == "" {
			return 0
		}
		// Try RFC3339 first, then common SQLite datetime formats.
		for _, layout := range []string{
			time.RFC3339,
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02",
		} {
			if t, err := time.Parse(layout, v); err == nil {
				return float64(t.Unix())
			}
		}
		slog.Warn("unparseable datePublished string", "value", v)
		return 0
	default:
		slog.Warn("unexpected datePublished type", "type", fmt.Sprintf("%T", raw), "value", raw)
		return 0
	}
}

func tsToISO(ts float64) string {
	if ts == 0 {
		return ""
	}
	dt := time.Unix(int64(ts), 0).UTC()
	return dt.Format(time.RFC3339)
}

type feedInfo struct {
	name     string
	category string
}

func loadFeedIDMap(accountDir string) map[string]feedInfo {
	plistPath := filepath.Join(accountDir, "FeedMetadata.plist")
	result := make(map[string]feedInfo)

	data, err := os.ReadFile(plistPath)
	if err != nil {
		slog.Info("FeedMetadata.plist not found", "dir", accountDir)
		return result
	}

	var plistData map[string]map[string]any
	if _, err := plist.Unmarshal(data, &plistData); err != nil {
		slog.Warn("cannot read FeedMetadata.plist", "err", err)
		return result
	}

	opmlNames := loadOPMLNames(accountDir)

	for xmlURL, entry := range plistData {
		feedID, _ := entry["feedID"].(string)
		if feedID == "" {
			continue
		}

		category := ""
		if folderRel, ok := entry["folderRelationship"].(map[string]any); ok {
			for labelKey := range folderRel {
				if idx := strings.Index(labelKey, "/label/"); idx >= 0 {
					category = labelKey[idx+7:]
					break
				}
			}
		}

		feedName := opmlNames[xmlURL]
		if feedName == "" {
			if parsed, err := url.Parse(xmlURL); err == nil {
				feedName = parsed.Host
			} else {
				feedName = xmlURL
			}
		}

		result[feedID] = feedInfo{name: feedName, category: category}
	}

	return result
}

// OPML structures
type opml struct {
	Body opmlBody `xml:"body"`
}

type opmlBody struct {
	Outlines []opmlOutline `xml:"outline"`
}

type opmlOutline struct {
	Text     string         `xml:"text,attr"`
	Title    string         `xml:"title,attr"`
	XMLURL   string         `xml:"xmlUrl,attr"`
	Children []opmlOutline  `xml:"outline"`
}

func loadOPMLNames(accountDir string) map[string]string {
	opmlPath := filepath.Join(accountDir, "Subscriptions.opml")
	data, err := os.ReadFile(opmlPath)
	if err != nil {
		return map[string]string{}
	}

	var o opml
	if err := xml.Unmarshal(data, &o); err != nil {
		slog.Warn("cannot parse OPML", "err", err)
		return map[string]string{}
	}

	names := make(map[string]string)
	for _, outline := range o.Body.Outlines {
		if outline.XMLURL != "" {
			name := outline.Text
			if name == "" {
				name = outline.Title
			}
			names[outline.XMLURL] = name
		}
		for _, child := range outline.Children {
			if child.XMLURL != "" {
				name := child.Text
				if name == "" {
					name = child.Title
				}
				names[child.XMLURL] = name
			}
		}
	}
	return names
}

func loadRSSAuthors(conn *sql.DB) map[string][]string {
	result := make(map[string][]string)
	rows, err := conn.Query(
		"SELECT al.articleID, a.name FROM authorsLookup al JOIN authors a ON al.authorID = a.authorID WHERE a.name IS NOT NULL AND a.name != ''",
	)
	if err != nil {
		slog.Warn("cannot read authors", "err", err)
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var articleID, name string
		if rows.Scan(&articleID, &name) == nil {
			result[articleID] = append(result[articleID], name)
		}
	}
	return result
}

func rssRowToArticle(
	articleID, feedID string,
	title, contentHTML, contentText, articleURL, externalURL, summary sql.NullString,
	datePublished float64,
	feedIDMap map[string]feedInfo,
	authorsMap map[string][]string,
) *Article {
	titleStr := ""
	if title.Valid {
		titleStr = title.String
	}

	urlStr := ""
	if articleURL.Valid && articleURL.String != "" {
		urlStr = articleURL.String
	} else if externalURL.Valid {
		urlStr = externalURL.String
	}

	ts := datePublished

	// Body: prefer contentText, fallback to contentHTML, then summary.
	body := ""
	if contentText.Valid && strings.TrimSpace(contentText.String) != "" {
		body = contentText.String
	} else if contentHTML.Valid && strings.TrimSpace(contentHTML.String) != "" {
		body = HTMLToText(contentHTML.String)
	}
	if strings.TrimSpace(body) == "" && summary.Valid && strings.TrimSpace(summary.String) != "" {
		s := summary.String
		if strings.Contains(s, "<") {
			body = HTMLToText(s)
		} else {
			body = s
		}
	}

	if strings.TrimSpace(body) == "" && titleStr == "" {
		return nil
	}

	fi := feedIDMap[feedID]
	feedName := fi.name
	if feedName == "" {
		feedName = feedID
	}

	return &Article{
		ArticleID:       articleID,
		Title:           titleStr,
		BodyText:        strings.TrimSpace(body),
		URL:             urlStr,
		FeedName:        feedName,
		FeedCategory:    fi.category,
		Authors:         authorsMap[articleID],
		DatePublished:   tsToISO(ts),
		DatePublishedTS: ts,
	}
}
