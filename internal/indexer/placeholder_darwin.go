//go:build darwin

package indexer

import (
	"os"
	"syscall"
)

// sfDataless is macOS's SF_DATALESS stat flag (sys/stat.h). The File Provider
// API sets it on cloud-storage placeholders — OneDrive "Files On-Demand",
// iCloud Drive "Optimise Mac Storage", Google Drive and Synology Drive
// on-demand files. The directory entry carries the real name, size and mtime,
// but no file data is present locally.
const sfDataless = 0x40000000

// isCloudPlaceholder reports whether a file is a dataless cloud placeholder.
//
// Reading such a file is not a disk read: macOS synchronously downloads it from
// the provider first, which can take seconds per file and materialises it on
// local disk. Stat-ing it, as here, does not trigger that.
func isCloudPlaceholder(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return st.Flags&sfDataless != 0
}
