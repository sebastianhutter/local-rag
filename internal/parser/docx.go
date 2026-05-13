package parser

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// DocxDocument is the parsed representation of a DOCX file.
type DocxDocument struct {
	Text string
}

// ParseDocx extracts text from a DOCX file by parsing the XML directly.
func ParseDocx(path string) *DocxDocument {
	text, err := extractDocxText(path)
	if err != nil {
		slog.Error("failed to parse DOCX", "path", path, "err", err)
		return &DocxDocument{Text: ""}
	}
	return &DocxDocument{Text: text}
}

func extractDocxText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}

	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}

	var docXML io.ReadCloser
	for _, zf := range zr.File {
		if zf.Name == "word/document.xml" {
			docXML, err = zf.Open()
			if err != nil {
				return "", fmt.Errorf("open word/document.xml: %w", err)
			}
			break
		}
	}
	if docXML == nil {
		return "", fmt.Errorf("word/document.xml not found in archive")
	}
	defer docXML.Close()

	return parseWordXML(docXML)
}

// parseWordXML walks the XML token stream and extracts text from <w:t> elements,
// inserting newlines at paragraph boundaries (<w:p>).
func parseWordXML(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var buf strings.Builder
	inText := false
	paragraphHasText := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				if t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
					if paragraphHasText {
						buf.WriteByte('\n')
					}
					paragraphHasText = false
				}
			case "t":
				if t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
					inText = true
				}
			case "tab":
				if t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
					buf.WriteByte('\t')
					paragraphHasText = true
				}
			case "br":
				if t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
					buf.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if t.Name.Local == "t" && t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
				inText = false
			}
		case xml.CharData:
			if inText {
				buf.Write(t)
				paragraphHasText = true
			}
		}
	}

	return strings.TrimSpace(buf.String()), nil
}
