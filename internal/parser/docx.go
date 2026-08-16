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

	part := findMainDocumentPart(zr)

	var docXML io.ReadCloser
	for _, zf := range zr.File {
		if zf.Name == part {
			docXML, err = zf.Open()
			if err != nil {
				return "", fmt.Errorf("open %s: %w", part, err)
			}
			break
		}
	}
	if docXML == nil {
		return "", fmt.Errorf("main document part %q not found in archive", part)
	}
	defer docXML.Close()

	return parseWordXML(docXML)
}

// mainPartRelType is the package relationship type whose target is the main
// document part.
const mainPartRelType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"

// packageRels is the root relationship file, _rels/.rels.
type packageRels struct {
	Relationships []struct {
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

// findMainDocumentPart resolves which zip entry holds the document body.
//
// It is not always word/document.xml: the part is addressed through the package
// relationships, and Word writes word/document2.xml (or document22.xml, …) for
// files it has repaired or converted. Assuming the conventional name silently
// drops those documents, so the relationship is read first and the convention
// is only a fallback for packages without a usable _rels/.rels.
func findMainDocumentPart(zr *zip.Reader) string {
	const fallback = "word/document.xml"

	f, err := zr.Open("_rels/.rels")
	if err != nil {
		return fallback
	}
	defer f.Close()

	var rels packageRels
	if err := xml.NewDecoder(f).Decode(&rels); err != nil {
		slog.Debug("cannot parse _rels/.rels, assuming conventional part name", "err", err)
		return fallback
	}

	for _, rel := range rels.Relationships {
		if rel.Type != mainPartRelType {
			continue
		}
		// Targets are relative to the package root and may be written either
		// as "word/document2.xml" or "/word/document2.xml".
		target := strings.TrimPrefix(strings.TrimPrefix(rel.Target, "/"), "./")
		if target != "" {
			return target
		}
	}

	return fallback
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
