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
	collectionID, err := getOrCreate(conn, "obsidian", "system")
	if err != nil {
		return failedResult(err)
	}

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
		files := walkVault(vault, excludeFolders, cfg.SkipCloudPlaceholders)
		slog.Info("found supported files in vault", "count", len(files), "vault", vault)
		allFiles = append(allFiles, files...)
	}

	result := &IndexResult{TotalFound: len(allFiles)}

	// A rebuild replaces everything, so clear once instead of purging per batch.
	cleared := clearForRebuild(conn, collectionID, force)

	indexItemsBatched(conn, cfg, collectionID, "obsidian", len(allFiles),
		func(i int) *indexItem { return fileToItem(conn, cfg, allFiles[i], collectionID, force) },
		result, progress, cleared)

	slog.Info("obsidian indexing complete", "result", result.String())
	return result
}

func walkVault(vaultPath string, excludeFolders map[string]bool, skipPlaceholders bool) []string {
	var results []string
	var placeholders int
	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if vaultSkipsDir(info.Name(), excludeFolders) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		if parser.SourceTypeForPath(path) == "" {
			return nil
		}
		if skipPlaceholders && isCloudPlaceholder(info) {
			placeholders++
			slog.Debug("skipping cloud-only file (not downloaded)", "path", path)
			return nil
		}
		results = append(results, path)
		return nil
	})
	if placeholders > 0 {
		slog.Warn("skipped cloud-only files; download them locally to index them "+
			"(Finder: Always Keep on This Device), or set skip_cloud_placeholders=false",
			"count", placeholders, "vault", vaultPath)
	}
	return results
}

// vaultSkipsDir reports whether the vault walk descends into a directory with
// this name. Kept separate from walkVault so pruning can ask the same question
// the walk asks — if the two drift, a folder added to the exclude list gets
// skipped by indexing but survives in the database forever, invisible to both.
func vaultSkipsDir(name string, excludeFolders map[string]bool) bool {
	return obsidianSkipDirs[name] || excludeFolders[name] || strings.HasPrefix(name, ".")
}

// vaultExcludesPath reports whether an already-indexed file now sits somewhere
// the vault walk will not go: inside an excluded folder, an Obsidian internal
// directory or a dot-directory, or hidden behind a leading dot itself.
//
// Only the components below a vault root are examined. A path under none of the
// configured vaults cannot be judged — the vault may simply have been removed
// from the config — and is reported as not excluded, which is also what makes
// this safe when no vaults are configured at all. Restricting to the part below
// the root matters: a home directory that happens to contain a folder called
// "_Templates" must not condemn every vault beneath it.
//
// Deliberately narrower than the walk in two places. A cloud placeholder is not
// excluded, because it still exists and skipping it is temporary. An
// unsupported extension is not excluded either, since that would turn a change
// to the parser's extension map into silent data loss.
func vaultExcludesPath(path string, vaults []string, excludeFolders map[string]bool) bool {
	for _, vault := range vaults {
		rel, err := filepath.Rel(expandPath(vault), path)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		for _, dir := range parts[:len(parts)-1] {
			if vaultSkipsDir(dir, excludeFolders) {
				return true
			}
		}
		if strings.HasPrefix(parts[len(parts)-1], ".") {
			return true
		}
	}
	return false
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
