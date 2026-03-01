package indexer

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

// Directories to skip when walking an Obsidian vault.
var obsidianSkipDirs = map[string]bool{
	".obsidian": true,
	".trash":    true,
	".git":      true,
}

// IndexObsidian indexes all supported files in Obsidian vaults.
func IndexObsidian(conn *sql.DB, cfg *config.Config, force bool, progress ProgressCallback) *IndexResult {
	collectionID := getOrCreate(conn, "obsidian", "system")

	excludeFolders := make(map[string]bool)
	for _, f := range cfg.ObsidianExcludeFolders {
		excludeFolders[f] = true
	}

	var allFiles []string
	for _, vault := range cfg.ObsidianVaults {
		vault = expandPath(vault)
		info, err := os.Stat(vault)
		if err != nil || !info.IsDir() {
			slog.Warn("vault path does not exist or is not a directory", "path", vault)
			continue
		}
		slog.Info("indexing Obsidian vault", "path", vault)
		files := walkVault(vault, excludeFolders)
		slog.Info("found supported files in vault", "count", len(files), "vault", vault)
		allFiles = append(allFiles, files...)
	}

	result := &IndexResult{TotalFound: len(allFiles)}

	for i, filePath := range allFiles {
		if progress != nil {
			progress(i+1, len(allFiles), filepath.Base(filePath))
		}
		indexed, err := indexSingleFile(conn, cfg, filePath, collectionID, force)
		if err != nil {
			slog.Error("error indexing", "path", filePath, "err", err)
			result.Errors++
			continue
		}
		if indexed {
			result.Indexed++
		} else {
			result.Skipped++
		}
	}

	slog.Info("obsidian indexing complete", "result", result.String())
	return result
}

func walkVault(vaultPath string, excludeFolders map[string]bool) []string {
	var results []string
	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if obsidianSkipDirs[name] || excludeFolders[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		if parser.SourceTypeForPath(path) != "" {
			results = append(results, path)
		}
		return nil
	})
	return results
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
