package parser

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
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
	OCR        bool // true if text was obtained via OCR
}

// OCROptions controls optional OCR fallback for scanned/image-only PDFs.
// Pass nil to disable OCR entirely (original behavior).
type OCROptions struct {
	Enabled       bool
	Languages     []string
	MaxPages      int
	MaxFileSizeMB int
	MinWordCount  int
}

// ParsePDF extracts text from a PDF file page by page using PDFium (WASM).
// If ocr is non-nil and enabled, pages with fewer words than MinWordCount
// are rendered and passed through tesseract OCR.
func ParsePDF(path string, ocr *OCROptions) []PageText {
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

	// Determine if OCR is usable for this file.
	ocrEnabled := ocr != nil && ocr.Enabled && tesseractAvailable()
	if ocrEnabled && ocr.MaxFileSizeMB > 0 && len(pdfBytes) > ocr.MaxFileSizeMB*1024*1024 {
		slog.Info("PDF exceeds OCR file size limit, skipping OCR", "path", path,
			"size_mb", len(pdfBytes)/(1024*1024), "limit_mb", ocr.MaxFileSizeMB)
		ocrEnabled = false
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

	if ocrEnabled && ocr.MaxPages > 0 && numPages > ocr.MaxPages {
		slog.Info("PDF exceeds OCR page limit, skipping OCR", "path", path,
			"pages", numPages, "limit", ocr.MaxPages)
		ocrEnabled = false
	}

	minWordCount := 10
	if ocr != nil && ocr.MinWordCount > 0 {
		minWordCount = ocr.MinWordCount
	}

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
		wordCount := len(strings.Fields(text))

		if wordCount >= minWordCount {
			// Enough embedded text — use as-is.
			pages = append(pages, PageText{PageNumber: i + 1, Text: text})
			continue
		}

		// Sparse or no text — try OCR if available.
		if ocrEnabled {
			ocrText := renderAndOCR(instance, doc.Document, i, path, ocr.Languages)
			if ocrText != "" {
				slog.Debug("OCR produced text for page", "page", i+1, "path", path, "words", len(strings.Fields(ocrText)))
				pages = append(pages, PageText{PageNumber: i + 1, Text: ocrText, OCR: true})
				continue
			}
		}

		// Fall through: use whatever embedded text exists (may be empty).
		if text != "" {
			pages = append(pages, PageText{PageNumber: i + 1, Text: text})
		}
	}

	if len(pages) == 0 {
		if ocr != nil && ocr.Enabled && !tesseractAvailable() {
			slog.Warn("no extractable text in PDF and tesseract not installed", "path", path)
		} else if ocrEnabled {
			slog.Warn("no extractable text in PDF (OCR also produced no text)", "path", path)
		} else {
			slog.Warn("no extractable text found in PDF (OCR disabled)", "path", path)
		}
	}

	return pages
}

// renderAndOCR renders a single PDF page to an image and runs tesseract on it.
func renderAndOCR(instance pdfium.Pdfium, document references.FPDF_DOCUMENT, pageIndex int, path string, languages []string) string {
	renderResp, err := instance.RenderPageInDPI(&requests.RenderPageInDPI{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: document,
				Index:    pageIndex,
			},
		},
		DPI: 300,
	})
	if err != nil {
		slog.Warn("failed to render PDF page for OCR", "page", pageIndex+1, "path", path, "err", err)
		return ""
	}
	defer renderResp.Cleanup()

	text, err := ocrImage(renderResp.Result.Image, languages)
	if err != nil {
		slog.Warn("OCR failed for page", "page", pageIndex+1, "path", path, "err", err)
		return ""
	}
	return text
}
