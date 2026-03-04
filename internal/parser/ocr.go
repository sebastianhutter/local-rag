package parser

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/tiff"
)

var (
	hasTesseract     bool
	tesseractChecked sync.Once
)

// tesseractAvailable returns true if the tesseract binary is on PATH.
// The result is cached after the first call.
func tesseractAvailable() bool {
	tesseractChecked.Do(func() {
		_, err := exec.LookPath("tesseract")
		hasTesseract = err == nil
		if !hasTesseract {
			slog.Info("tesseract not found on PATH, OCR fallback disabled")
		} else {
			slog.Info("tesseract found, OCR fallback available")
		}
	})
	return hasTesseract
}

// ocrImage binarizes the rendered page and runs tesseract OCR on it.
// The binarization (grayscale + threshold) is critical for scanned documents
// with gray backgrounds that tesseract cannot handle directly.
func ocrImage(img *image.RGBA, languages []string) (string, error) {
	bw := binarize(img)
	return runTesseract(bw, languages)
}

// runTesseract encodes img to a TIFF temp file and runs tesseract on it.
func runTesseract(img image.Image, languages []string) (string, error) {
	tmpFile, err := os.CreateTemp("", "local-rag-ocr-*.tiff")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := encodeTIFF(tmpFile, img); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("encode TIFF: %w", err)
	}
	tmpFile.Close()

	// Resolve symlinks so tesseract can open the file on macOS
	// where /tmp is a symlink to /private/tmp.
	realPath, err := filepath.EvalSymlinks(tmpPath)
	if err != nil {
		realPath = tmpPath
	}

	langStr := "eng"
	if len(languages) > 0 {
		langStr = strings.Join(languages, "+")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tesseract", realPath, "stdout", "-l", langStr)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tesseract: %w (stderr: %s)", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// binarize converts an RGBA image to a grayscale image with a fixed threshold
// (black text on white background). This dramatically improves tesseract
// accuracy on scanned documents with gray backgrounds.
func binarize(src *image.RGBA) *image.Gray {
	b := src.Bounds()
	dst := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := src.At(x, y).RGBA()
			// Standard luminance formula, values are 16-bit.
			lum := (299*r + 587*g + 114*bl) / 1000
			if lum < 0x8000 {
				dst.SetGray(x, y, color.Gray{Y: 0}) // black
			} else {
				dst.SetGray(x, y, color.Gray{Y: 255}) // white
			}
		}
	}
	return dst
}

// toRGBA converts any image.Image to *image.RGBA.
func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, img, b.Min, draw.Src)
	return dst
}

// encodeTIFF writes an image as uncompressed TIFF.
func encodeTIFF(w io.Writer, img image.Image) error {
	return tiff.Encode(w, img, &tiff.Options{Compression: tiff.Uncompressed})
}
