package parser

import (
	"log/slog"
	"strings"

	"github.com/dslipak/pdf"
)

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

	for i := 1; i <= numPages; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			slog.Warn("failed to extract text from page", "page", i, "path", path, "err", err)
			continue
		}

		text = strings.TrimSpace(text)
		if text != "" {
			pages = append(pages, PageText{PageNumber: i, Text: text})
		}
	}

	if len(pages) == 0 {
		slog.Warn("no extractable text found in PDF (may be OCR-only)", "path", path)
	}

	return pages
}
