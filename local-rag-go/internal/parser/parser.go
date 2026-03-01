// Package parser provides file format parsers for local-rag.
//
// Each parser reads a specific file format and returns extracted text.
// The Dispatch function selects the appropriate parser based on file extension.
package parser

import (
	"path/filepath"
	"strings"
)

// ExtensionMap maps file extensions to source type names.
var ExtensionMap = map[string]string{
	".md":    "markdown",
	".pdf":   "pdf",
	".docx":  "docx",
	".html":  "html",
	".htm":   "html",
	".txt":   "plaintext",
	".csv":   "plaintext",
	".json":  "plaintext",
	".yaml":  "plaintext",
	".yml":   "plaintext",
	".epub":  "epub",
}

// SourceTypeForPath returns the source type for a file path based on its extension.
// Returns empty string if the extension is not recognized.
func SourceTypeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return ExtensionMap[ext]
}
