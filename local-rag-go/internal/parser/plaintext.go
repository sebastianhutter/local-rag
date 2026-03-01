package parser

import (
	"log/slog"
	"os"
)

// ParsePlaintext reads a plain text file and returns its content.
func ParsePlaintext(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Error("failed to read file", "path", path, "err", err)
		return ""
	}
	return string(data)
}
