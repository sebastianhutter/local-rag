package parser

import (
	"log/slog"
	"strings"
	"time"

	"github.com/dslipak/pdf"
)

// pdfPageTimeout is the maximum time to spend extracting text from a single page.
const pdfPageTimeout = 30 * time.Second

// PageText represents a single page of extracted PDF text.
type PageText struct {
	PageNumber int // 1-based
	Text       string
}

// ParsePDF extracts text from a PDF file page by page.
func ParsePDF(path string) []PageText {
	reader, err := pdf.Open(path)
	if err != nil {
		slog.Error("failed to open PDF", "path", path, "err", err)
		return nil
	}

	var pages []PageText
	numPages := reader.NumPage()
	slog.Debug("parsing PDF", "path", path, "pages", numPages)

	for i := 1; i <= numPages; i++ {
		text := extractPageText(reader, i, path)
		if text != "" {
			pages = append(pages, PageText{PageNumber: i, Text: text})
		}
	}

	if len(pages) == 0 {
		slog.Warn("no extractable text found in PDF (may be OCR-only)", "path", path)
	}

	return pages
}

// extractPageText extracts text from a single page with a timeout to avoid
// hanging on complex or malformed PDF pages.
func extractPageText(reader *pdf.Reader, pageNum int, path string) string {
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("panic extracting PDF page", "page", pageNum, "path", path, "err", r)
				ch <- result{err: nil}
			}
		}()
		page := reader.Page(pageNum)
		if page.V.IsNull() {
			ch <- result{}
			return
		}
		text, err := page.GetPlainText(nil)
		ch <- result{text: strings.TrimSpace(text), err: err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			slog.Warn("failed to extract text from page", "page", pageNum, "path", path, "err", r.err)
			return ""
		}
		return r.text
	case <-time.After(pdfPageTimeout):
		slog.Warn("timeout extracting PDF page, skipping", "page", pageNum, "path", path, "timeout", pdfPageTimeout)
		return ""
	}
}
