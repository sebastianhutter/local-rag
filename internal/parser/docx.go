package parser

import (
	"log/slog"

	"github.com/lu4p/cat"
)

// DocxDocument is the parsed representation of a DOCX file.
type DocxDocument struct {
	Text string
}

// ParseDocx extracts text from a DOCX file.
func ParseDocx(path string) *DocxDocument {
	text, err := cat.File(path)
	if err != nil {
		slog.Error("failed to parse DOCX", "path", path, "err", err)
		return &DocxDocument{Text: ""}
	}
	return &DocxDocument{Text: text}
}
