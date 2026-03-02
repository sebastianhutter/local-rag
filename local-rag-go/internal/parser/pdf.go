package parser

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// pdfPool is a lazily-initialized singleton so the WASM runtime is reused
// across all ParsePDF calls rather than re-created per file.
var (
	pdfPool     pdfium.Pool
	pdfPoolOnce sync.Once
	pdfPoolErr  error
)

func initPDFPool() {
	pdfPool, pdfPoolErr = webassembly.Init(webassembly.Config{
		MinIdle:  1,
		MaxIdle:  1,
		MaxTotal: 1,
	})
}

// ClosePDFPool shuts down the PDFium WASM pool. Call on application exit.
func ClosePDFPool() {
	if pdfPool != nil {
		if err := pdfPool.Close(); err != nil {
			slog.Error("failed to close PDF pool", "err", err)
		}
	}
}

// PageText represents a single page of extracted PDF text.
type PageText struct {
	PageNumber int // 1-based
	Text       string
}

// ParsePDF extracts text from a PDF file page by page using PDFium (WASM).
func ParsePDF(path string) []PageText {
	pdfPoolOnce.Do(initPDFPool)
	if pdfPoolErr != nil {
		slog.Error("failed to init PDF pool", "err", pdfPoolErr)
		return nil
	}

	instance, err := pdfPool.GetInstance(30 * time.Second)
	if err != nil {
		slog.Error("failed to get PDF pool instance", "err", err)
		return nil
	}
	defer instance.Close()

	pdfBytes, err := os.ReadFile(path)
	if err != nil {
		slog.Error("failed to read PDF file", "path", path, "err", err)
		return nil
	}

	doc, err := instance.OpenDocument(&requests.OpenDocument{
		File: &pdfBytes,
	})
	if err != nil {
		slog.Error("failed to open PDF document", "path", path, "err", err)
		return nil
	}
	defer instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{
		Document: doc.Document,
	})

	pageCountResp, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: doc.Document,
	})
	if err != nil {
		slog.Error("failed to get PDF page count", "path", path, "err", err)
		return nil
	}

	numPages := pageCountResp.PageCount
	slog.Debug("parsing PDF", "path", path, "pages", numPages)

	var pages []PageText
	for i := 0; i < numPages; i++ {
		resp, err := instance.GetPageText(&requests.GetPageText{
			Page: requests.Page{
				ByIndex: &requests.PageByIndex{
					Document: doc.Document,
					Index:    i,
				},
			},
		})
		if err != nil {
			slog.Warn("failed to extract text from page", "page", i+1, "path", path, "err", err)
			continue
		}

		text := strings.TrimSpace(resp.Text)
		if text != "" {
			pages = append(pages, PageText{PageNumber: i + 1, Text: text})
		}
	}

	if len(pages) == 0 {
		slog.Warn("no extractable text found in PDF (may be OCR-only)", "path", path)
	}

	return pages
}
