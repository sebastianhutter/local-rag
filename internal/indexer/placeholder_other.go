//go:build !darwin

package indexer

import "os"

// isCloudPlaceholder reports whether a file is a dataless cloud placeholder.
// Placeholder detection is macOS-specific (SF_DATALESS via the File Provider
// API); elsewhere every file is treated as locally present.
func isCloudPlaceholder(os.FileInfo) bool { return false }
