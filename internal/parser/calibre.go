package parser

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// CalibreBook is a book entry from a Calibre library.
type CalibreBook struct {
	BookID       int64
	Title        string
	Authors      []string
	Tags         []string
	Series       string
	SeriesIndex  float64
	Publisher    string
	Pubdate      string
	Rating       int
	Languages    []string
	Identifiers  map[string]string
	Description  string // plain text
	Formats      map[string]string // format -> filename_without_ext
	RelativePath string // books.path
	LastModified string
}

// ParseCalibreLibrary loads all books from a Calibre library (read-only).
func ParseCalibreLibrary(libraryPath string) ([]*CalibreBook, error) {
	dbPath := filepath.Join(libraryPath, "metadata.db")
	uri := fmt.Sprintf("file:%s?mode=ro", dbPath)

	conn, err := sql.Open("sqlite3", uri)
	if err != nil {
		return nil, fmt.Errorf("open calibre db: %w", err)
	}
	defer conn.Close()

	return loadAllBooks(conn)
}

// GetBookFilePath resolves the absolute path for the best available format.
func GetBookFilePath(libraryPath string, book *CalibreBook, preferredFormats []string) (string, string) {
	if len(preferredFormats) == 0 {
		preferredFormats = []string{"EPUB", "PDF"}
	}

	for _, fmt := range preferredFormats {
		filenameBase, ok := book.Formats[fmt]
		if !ok {
			continue
		}
		filePath := filepath.Join(libraryPath, book.RelativePath, filenameBase+"."+strings.ToLower(fmt))
		if fileExists(filePath) {
			return filePath, strings.ToLower(fmt)
		}
	}
	return "", ""
}

func loadAllBooks(conn *sql.DB) ([]*CalibreBook, error) {
	rows, err := conn.Query("SELECT id, title, path, pubdate, last_modified FROM books")
	if err != nil {
		return nil, fmt.Errorf("query books: %w", err)
	}
	defer rows.Close()

	type bookRow struct {
		id           int64
		title        string
		path         string
		pubdate      string
		lastModified string
	}
	var bookRows []bookRow
	for rows.Next() {
		var b bookRow
		var pubdate, lastMod sql.NullString
		if err := rows.Scan(&b.id, &b.title, &b.path, &pubdate, &lastMod); err != nil {
			continue
		}
		if pubdate.Valid {
			b.pubdate = pubdate.String
		}
		if lastMod.Valid {
			b.lastModified = lastMod.String
		}
		bookRows = append(bookRows, b)
	}

	if len(bookRows) == 0 {
		return nil, nil
	}

	authorsMap := loadBookAuthors(conn)
	tagsMap := loadBookTags(conn)
	seriesMap := loadBookSeries(conn)
	publishersMap := loadBookPublishers(conn)
	ratingsMap := loadBookRatings(conn)
	languagesMap := loadBookLanguages(conn)
	identifiersMap := loadBookIdentifiers(conn)
	commentsMap := loadBookComments(conn)
	formatsMap := loadBookFormats(conn)

	books := make([]*CalibreBook, 0, len(bookRows))
	for _, r := range bookRows {
		description := ""
		if html, ok := commentsMap[r.id]; ok {
			description = HTMLToText(html)
		}

		series := ""
		seriesIndex := 0.0
		if si, ok := seriesMap[r.id]; ok {
			series = si.name
			seriesIndex = si.index
		}

		books = append(books, &CalibreBook{
			BookID:       r.id,
			Title:        r.title,
			Authors:      authorsMap[r.id],
			Tags:         tagsMap[r.id],
			Series:       series,
			SeriesIndex:  seriesIndex,
			Publisher:    publishersMap[r.id],
			Pubdate:      r.pubdate,
			Rating:       ratingsMap[r.id],
			Languages:    languagesMap[r.id],
			Identifiers:  identifiersMap[r.id],
			Description:  description,
			Formats:      formatsMap[r.id],
			RelativePath: r.path,
			LastModified: r.lastModified,
		})
	}

	slog.Info("loaded books from Calibre library", "count", len(books))
	return books, nil
}

func loadBookAuthors(conn *sql.DB) map[int64][]string {
	result := make(map[int64][]string)
	rows, err := conn.Query(
		"SELECT bal.book, a.name FROM books_authors_link bal JOIN authors a ON bal.author = a.id ORDER BY bal.book, bal.id",
	)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var name string
		if rows.Scan(&bookID, &name) == nil {
			result[bookID] = append(result[bookID], name)
		}
	}
	return result
}

func loadBookTags(conn *sql.DB) map[int64][]string {
	result := make(map[int64][]string)
	rows, err := conn.Query(
		"SELECT btl.book, t.name FROM books_tags_link btl JOIN tags t ON btl.tag = t.id",
	)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var name string
		if rows.Scan(&bookID, &name) == nil {
			result[bookID] = append(result[bookID], name)
		}
	}
	return result
}

type seriesInfo struct {
	name  string
	index float64
}

func loadBookSeries(conn *sql.DB) map[int64]seriesInfo {
	result := make(map[int64]seriesInfo)
	rows, err := conn.Query(
		"SELECT bsl.book, s.name, b.series_index FROM books_series_link bsl JOIN series s ON bsl.series = s.id JOIN books b ON bsl.book = b.id",
	)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var name string
		var idx sql.NullFloat64
		if rows.Scan(&bookID, &name, &idx) == nil {
			si := seriesInfo{name: name}
			if idx.Valid {
				si.index = idx.Float64
			}
			result[bookID] = si
		}
	}
	return result
}

func loadBookPublishers(conn *sql.DB) map[int64]string {
	result := make(map[int64]string)
	rows, err := conn.Query(
		"SELECT bpl.book, p.name FROM books_publishers_link bpl JOIN publishers p ON bpl.publisher = p.id",
	)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var name string
		if rows.Scan(&bookID, &name) == nil {
			result[bookID] = name
		}
	}
	return result
}

func loadBookRatings(conn *sql.DB) map[int64]int {
	result := make(map[int64]int)
	rows, err := conn.Query(
		"SELECT brl.book, r.rating FROM books_ratings_link brl JOIN ratings r ON brl.rating = r.id",
	)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var rating int
		if rows.Scan(&bookID, &rating) == nil {
			result[bookID] = rating
		}
	}
	return result
}

func loadBookLanguages(conn *sql.DB) map[int64][]string {
	result := make(map[int64][]string)
	rows, err := conn.Query(
		"SELECT bll.book, l.lang_code FROM books_languages_link bll JOIN languages l ON bll.lang_code = l.id",
	)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var lang string
		if rows.Scan(&bookID, &lang) == nil {
			result[bookID] = append(result[bookID], lang)
		}
	}
	return result
}

func loadBookIdentifiers(conn *sql.DB) map[int64]map[string]string {
	result := make(map[int64]map[string]string)
	rows, err := conn.Query("SELECT book, type, val FROM identifiers")
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var idType, val string
		if rows.Scan(&bookID, &idType, &val) == nil {
			if result[bookID] == nil {
				result[bookID] = make(map[string]string)
			}
			result[bookID][idType] = val
		}
	}
	return result
}

func loadBookComments(conn *sql.DB) map[int64]string {
	result := make(map[int64]string)
	rows, err := conn.Query("SELECT book, text FROM comments")
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var text string
		if rows.Scan(&bookID, &text) == nil {
			result[bookID] = text
		}
	}
	return result
}

func loadBookFormats(conn *sql.DB) map[int64]map[string]string {
	result := make(map[int64]map[string]string)
	rows, err := conn.Query("SELECT book, format, name FROM data")
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var format, name string
		if rows.Scan(&bookID, &format, &name) == nil {
			if result[bookID] == nil {
				result[bookID] = make(map[string]string)
			}
			result[bookID][format] = name
		}
	}
	return result
}
