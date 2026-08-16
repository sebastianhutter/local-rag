package indexer

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

var preferredFormats = []string{"EPUB", "PDF"}

// IndexCalibre indexes ebooks from Calibre libraries into the "calibre" collection.
func IndexCalibre(conn *sql.DB, cfg *config.Config, force bool, progress ProgressCallback) *IndexResult {
	collectionID, err := getOrCreate(conn, "calibre", "system")
	if err != nil {
		return failedResult(err)
	}

	type bookEntry struct {
		libraryPath string
		book        *parser.CalibreBook
	}

	var allBooks []bookEntry
	for _, lib := range cfg.CalibreLibraries {
		lib = expandPath(lib)
		info, err := os.Stat(lib)
		if err != nil || !info.IsDir() {
			slog.Warn("calibre library path does not exist", "path", lib)
			continue
		}
		slog.Info("indexing Calibre library", "path", lib)
		books, err := parser.ParseCalibreLibrary(lib)
		if err != nil {
			slog.Error("failed to parse Calibre library", "path", lib, "err", err)
			continue
		}
		slog.Info("found books", "count", len(books), "library", lib)
		for _, b := range books {
			allBooks = append(allBooks, bookEntry{libraryPath: lib, book: b})
		}
	}

	result := &IndexResult{TotalFound: len(allBooks)}

	// A rebuild replaces everything, so clear once instead of purging per batch.
	cleared := clearForRebuild(conn, collectionID, force)

	indexItemsBatched(conn, cfg, collectionID, "calibre", len(allBooks),
		func(i int) *indexItem {
			return bookToItem(conn, cfg, collectionID, allBooks[i].libraryPath, allBooks[i].book, force)
		},
		result, progress, cleared)

	slog.Info("calibre indexing complete", "result", result.String())
	return result
}

func buildBookMetadata(book *parser.CalibreBook, libraryPath, format string) map[string]any {
	meta := map[string]any{}
	if len(book.Authors) > 0 {
		meta["authors"] = book.Authors
	}
	if len(book.Tags) > 0 {
		meta["tags"] = book.Tags
	}
	if book.Series != "" {
		meta["series"] = book.Series
	}
	if book.SeriesIndex != 0 {
		meta["series_index"] = book.SeriesIndex
	}
	if book.Publisher != "" {
		meta["publisher"] = book.Publisher
	}
	if book.Pubdate != "" {
		meta["pubdate"] = book.Pubdate
	}
	if book.Rating != 0 {
		meta["rating"] = book.Rating
	}
	if len(book.Languages) > 0 {
		meta["languages"] = book.Languages
	}
	if len(book.Identifiers) > 0 {
		meta["identifiers"] = book.Identifiers
	}
	meta["calibre_id"] = book.BookID
	if format != "" {
		meta["format"] = format
	}
	meta["library"] = libraryPath
	return meta
}

// bookToItem prepares one book for indexing, or returns nil if it should be
// skipped — unchanged, or no extractable content. EPUB/PDF extraction only runs
// for books that will actually be re-embedded.
func bookToItem(conn *sql.DB, cfg *config.Config, collectionID int64, libraryPath string, book *parser.CalibreBook, force bool) *indexItem {
	filePath, format := parser.GetBookFilePath(libraryPath, book, preferredFormats)

	var sourcePath, contentHash, sourceType string
	if filePath != "" {
		sourcePath = filePath
		h, err := fileHash(filePath)
		if err != nil {
			slog.Warn("cannot hash book file, skipping", "title", book.Title, "err", err)
			return nil
		}
		contentHash = h
		sourceType = format
	} else {
		if book.Description == "" {
			slog.Warn("book has no EPUB/PDF and no description, skipping", "title", book.Title)
			return nil
		}
		sourcePath = fmt.Sprintf("calibre://%s/%s", libraryPath, book.RelativePath)
		h := sha256.Sum256([]byte(book.Description))
		contentHash = fmt.Sprintf("%x", h)
		sourceType = "calibre-description"
		format = ""
	}

	if !force && isSourceUnchanged(conn, collectionID, sourcePath, contentHash) {
		return nil
	}

	bookMeta := buildBookMetadata(book, libraryPath, format)
	chunks := extractAndChunkBook(book, filePath, format, cfg, bookMeta)
	if len(chunks) == 0 {
		slog.Warn("no content extracted from book, skipping", "title", book.Title)
		return nil
	}

	return &indexItem{
		SourcePath: sourcePath,
		SourceType: sourceType,
		Title:      book.Title,
		Chunks:     chunks,
		FileHash:   contentHash,
		Mtime:      book.LastModified,
	}
}

func extractAndChunkBook(book *parser.CalibreBook, filePath, format string, cfg *config.Config, bookMeta map[string]any) []chunker.Chunk {
	chunkSize := cfg.ChunkSizeTokens
	overlap := cfg.ChunkOverlapTokens
	var chunks []chunker.Chunk
	chunkIdx := 0

	if filePath != "" {
		var sectionLabel string
		switch format {
		case "epub":
			sectionLabel = "chapter"
			chapters := parser.ParseEPUB(filePath)
			for _, ch := range chapters {
				sectionTitle := fmt.Sprintf("%s (%s %d)", book.Title, sectionLabel, ch.ChapterNumber)
				sectionChunks := chunker.ChunkPlain(ch.Text, sectionTitle, chunkSize, overlap)
				for j := range sectionChunks {
					sectionChunks[j].ChunkIndex = chunkIdx
					meta := copyMeta(bookMeta)
					meta[sectionLabel+"_number"] = ch.ChapterNumber
					sectionChunks[j].Metadata = meta
					chunks = append(chunks, sectionChunks[j])
					chunkIdx++
				}
			}
		case "pdf":
			sectionLabel = "page"
			var ocrOpts *parser.OCROptions
			if cfg.OCR.Enabled {
				ocrOpts = &parser.OCROptions{
					Enabled:       true,
					Languages:     cfg.OCR.Languages,
					MaxPages:      cfg.OCR.MaxPages,
					MaxFileSizeMB: cfg.OCR.MaxFileSizeMB,
					MinWordCount:  cfg.OCR.MinWordCount,
				}
			}
			pages := parser.ParsePDF(filePath, ocrOpts)
			for _, pg := range pages {
				sectionTitle := fmt.Sprintf("%s (%s %d)", book.Title, sectionLabel, pg.PageNumber)
				sectionChunks := chunker.ChunkPlain(pg.Text, sectionTitle, chunkSize, overlap)
				for j := range sectionChunks {
					sectionChunks[j].ChunkIndex = chunkIdx
					meta := copyMeta(bookMeta)
					meta[sectionLabel+"_number"] = pg.PageNumber
					if pg.OCR {
						meta["ocr"] = true
					}
					sectionChunks[j].Metadata = meta
					chunks = append(chunks, sectionChunks[j])
					chunkIdx++
				}
			}
		}
	}

	if book.Description != "" {
		descTitle := fmt.Sprintf("%s (description)", book.Title)
		descChunks := chunker.ChunkPlain(book.Description, descTitle, chunkSize, overlap)
		for j := range descChunks {
			descChunks[j].ChunkIndex = chunkIdx
			meta := copyMeta(bookMeta)
			meta["chunk_type"] = "description"
			descChunks[j].Metadata = meta
			chunks = append(chunks, descChunks[j])
			chunkIdx++
		}
	}

	return chunks
}

func copyMeta(m map[string]any) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
