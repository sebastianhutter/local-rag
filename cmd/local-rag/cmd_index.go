package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/indexer"
)

var forceIndex bool
var noPrune bool

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index content from various sources",
}

// --- index obsidian ---

var obsidianVaults []string

var indexObsidianCmd = &cobra.Command{
	Use:   "obsidian",
	Short: "Index Obsidian vault(s)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		if !cfg.IsCollectionEnabled("obsidian") {
			return fmt.Errorf("collection 'obsidian' is disabled in config")
		}

		vaults := obsidianVaults
		if len(vaults) == 0 {
			vaults = cfg.ObsidianVaults
		}
		if len(vaults) == 0 {
			return fmt.Errorf("no Obsidian vaults configured — use --vault or set obsidian_vaults in config")
		}

		// Temporarily override vaults in config
		origVaults := cfg.ObsidianVaults
		cfg.ObsidianVaults = vaults
		defer func() { cfg.ObsidianVaults = origVaults }()

		autoPrune(conn, cfg, "obsidian")
		result := indexer.IndexObsidian(conn, cfg, forceIndex, progressCallback("obsidian"))
		printResult("Obsidian", result)
		return nil
	},
}

// --- index email ---

var indexEmailCmd = &cobra.Command{
	Use:   "email",
	Short: "Index eM Client emails",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		if !cfg.IsCollectionEnabled("email") {
			return fmt.Errorf("collection 'email' is disabled in config")
		}

		result := indexer.IndexEmails(conn, cfg, forceIndex, progressCallback("email"))
		printResult("Email", result)
		return nil
	},
}

// --- index calibre ---

var calibreLibraries []string

var indexCalibreCmd = &cobra.Command{
	Use:   "calibre",
	Short: "Index Calibre ebook libraries",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		if !cfg.IsCollectionEnabled("calibre") {
			return fmt.Errorf("collection 'calibre' is disabled in config")
		}

		libs := calibreLibraries
		if len(libs) == 0 {
			libs = cfg.CalibreLibraries
		}
		if len(libs) == 0 {
			return fmt.Errorf("no Calibre libraries configured — use --library or set calibre_libraries in config")
		}

		origLibs := cfg.CalibreLibraries
		cfg.CalibreLibraries = libs
		defer func() { cfg.CalibreLibraries = origLibs }()

		result := indexer.IndexCalibre(conn, cfg, forceIndex, progressCallback("calibre"))
		printResult("Calibre", result)
		return nil
	},
}

// --- index rss ---

var indexRSSCmd = &cobra.Command{
	Use:   "rss",
	Short: "Index NetNewsWire RSS articles",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		if !cfg.IsCollectionEnabled("rss") {
			return fmt.Errorf("collection 'rss' is disabled in config")
		}

		result := indexer.IndexRSS(conn, cfg, forceIndex, progressCallback("rss"))
		printResult("RSS", result)
		return nil
	},
}

// --- index project ---

var indexProjectCmd = &cobra.Command{
	Use:   "project [NAME]",
	Short: "Index project document collections (from config)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		if len(cfg.Projects) == 0 {
			return fmt.Errorf("no projects configured in config")
		}

		var projectNames []string
		if len(args) > 0 {
			name := args[0]
			if _, ok := cfg.Projects[name]; !ok {
				return fmt.Errorf("project %q not found in config", name)
			}
			projectNames = []string{name}
		} else {
			for name := range cfg.Projects {
				projectNames = append(projectNames, name)
			}
		}

		for _, name := range projectNames {
			if !cfg.IsCollectionEnabled(name) {
				slog.Warn("collection is disabled, skipping", "name", name)
				continue
			}

			autoPrune(conn, cfg, name)
			paths := cfg.Projects[name]
			fmt.Printf("project: %s\n", name)
			result := indexer.IndexProject(conn, cfg, name, paths, forceIndex, progressCallback(name))
			printResult(name, result)
		}
		return nil
	},
}

// --- index code ---

var indexHistory bool

var indexCodeCmd = &cobra.Command{
	Use:   "code [NAME]",
	Short: "Index code repositories (tree-sitter + optional commit history)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		if len(cfg.Repositories) == 0 {
			return fmt.Errorf("no repositories configured in config")
		}

		var repoNames []string
		if len(args) > 0 {
			name := args[0]
			if _, ok := cfg.Repositories[name]; !ok {
				return fmt.Errorf("repository collection %q not found in config", name)
			}
			repoNames = []string{name}
		} else {
			for name := range cfg.Repositories {
				repoNames = append(repoNames, name)
			}
		}

		for _, repoName := range repoNames {
			if !cfg.IsCollectionEnabled(repoName) {
				slog.Warn("collection is disabled, skipping", "name", repoName)
				continue
			}

			autoPrune(conn, cfg, repoName)
			repos := cfg.Repositories[repoName]
			for _, repoPath := range repos {
				fmt.Printf("%s: %s\n", repoName, repoPath)
				result := indexer.IndexGitRepo(conn, cfg, repoPath, repoName, forceIndex, indexHistory, progressCallback(repoName))
				printResult(fmt.Sprintf("%s/%s", repoName, filepath.Base(repoPath)), result)
			}
		}
		return nil
	},
}

// --- index all ---

var indexAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Index all configured sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		// Auto-prune obsidian, code, and project collections before indexing
		if !noPrune {
			autoPrune(conn, cfg, "obsidian")
			for repoName := range cfg.Repositories {
				if cfg.IsCollectionEnabled(repoName) {
					autoPrune(conn, cfg, repoName)
				}
			}
			for projectName := range cfg.Projects {
				if cfg.IsCollectionEnabled(projectName) {
					autoPrune(conn, cfg, projectName)
				}
			}
		}

		type indexSource struct {
			label string
			run   func() *indexer.IndexResult
		}

		var sources []indexSource

		if cfg.IsCollectionEnabled("obsidian") && len(cfg.ObsidianVaults) > 0 {
			sources = append(sources, indexSource{
				label: "Obsidian",
				run: func() *indexer.IndexResult {
					return indexer.IndexObsidian(conn, cfg, forceIndex, progressCallback("obsidian"))
				},
			})
		}

		if cfg.IsCollectionEnabled("email") {
			sources = append(sources, indexSource{
				label: "Email",
				run: func() *indexer.IndexResult {
					return indexer.IndexEmails(conn, cfg, forceIndex, progressCallback("email"))
				},
			})
		}

		if cfg.IsCollectionEnabled("calibre") && len(cfg.CalibreLibraries) > 0 {
			sources = append(sources, indexSource{
				label: "Calibre",
				run: func() *indexer.IndexResult {
					return indexer.IndexCalibre(conn, cfg, forceIndex, progressCallback("calibre"))
				},
			})
		}

		if cfg.IsCollectionEnabled("rss") {
			sources = append(sources, indexSource{
				label: "RSS",
				run: func() *indexer.IndexResult {
					return indexer.IndexRSS(conn, cfg, forceIndex, progressCallback("rss"))
				},
			})
		}

		for repoName, repos := range cfg.Repositories {
			if !cfg.IsCollectionEnabled(repoName) {
				continue
			}
			for _, repoPath := range repos {
				rn, rp := repoName, repoPath
				label := fmt.Sprintf("%s/%s", rn, filepath.Base(rp))
				sources = append(sources, indexSource{
					label: label,
					run: func() *indexer.IndexResult {
						return indexer.IndexGitRepo(conn, cfg, rp, rn, forceIndex, true, progressCallback(rn))
					},
				})
			}
		}

		// Project collections from config
		for projectName, projectPaths := range cfg.Projects {
			if !cfg.IsCollectionEnabled(projectName) {
				continue
			}
			pn, pp := projectName, projectPaths
			sources = append(sources, indexSource{
				label: pn,
				run: func() *indexer.IndexResult {
					return indexer.IndexProject(conn, cfg, pn, pp, forceIndex, progressCallback(pn))
				},
			})
		}

		if len(sources) == 0 {
			return fmt.Errorf("no sources configured — add vaults, libraries, or repositories in config")
		}

		fmt.Println()
		fmt.Printf("%-30s %8s %8s %8s %8s\n", "Collection", "Indexed", "Skipped", "Errors", "Total")
		fmt.Println("-----------------------------------------------------------------------")

		for _, s := range sources {
			fmt.Printf("%s...\n", s.label)
			result := s.run()
			errStr := fmt.Sprintf("%d", result.Errors)
			if result.Errors > 0 {
				errStr = fmt.Sprintf("*%d*", result.Errors)
			}
			fmt.Printf("  %-28s %8d %8d %8s %8d\n", s.label, result.Indexed, result.Skipped, errStr, result.TotalFound)
		}

		fmt.Println()
		return nil
	},
}

func init() {
	indexCmd.PersistentFlags().BoolVar(&forceIndex, "force", false, "Force re-index all content")
	indexCmd.PersistentFlags().BoolVar(&noPrune, "no-prune", false, "Skip automatic pruning of stale sources before indexing")

	indexObsidianCmd.Flags().StringArrayVarP(&obsidianVaults, "vault", "V", nil, "Vault path(s)")
	indexCalibreCmd.Flags().StringArrayVarP(&calibreLibraries, "library", "l", nil, "Library path(s)")
	indexCodeCmd.Flags().BoolVar(&indexHistory, "history", false, "Also index commit history")

	indexCmd.AddCommand(indexObsidianCmd)
	indexCmd.AddCommand(indexEmailCmd)
	indexCmd.AddCommand(indexCalibreCmd)
	indexCmd.AddCommand(indexRSSCmd)
	indexCmd.AddCommand(indexProjectCmd)
	indexCmd.AddCommand(indexCodeCmd)
	indexCmd.AddCommand(indexAllCmd)

	rootCmd.AddCommand(indexCmd)
}

func openConfigAndDB() (*config.Config, *sql.DB, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	conn, err := db.Open(cfg.ExpandedDBPath())
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.InitSchema(conn, cfg.EmbeddingDimensions); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("init schema: %w", err)
	}

	return cfg, conn, nil
}

func progressCallback(label string) indexer.ProgressCallback {
	return func(current, total int, itemName string) {
		fmt.Fprintf(os.Stderr, "\r\033[K[%s] %d/%d: %s", label, current, total, truncateStr(itemName, 60))
		if current == total {
			fmt.Fprintln(os.Stderr)
		}
	}
}

func printResult(label string, result *indexer.IndexResult) {
	errStr := fmt.Sprintf("%d errors", result.Errors)
	if result.Errors > 0 {
		errStr = fmt.Sprintf("%d errors!", result.Errors)
	}
	fmt.Printf("%s indexing complete: %d indexed, %d skipped, %s (out of %d found)\n",
		label, result.Indexed, result.Skipped, errStr, result.TotalFound)
	for _, msg := range result.ErrorMessages {
		fmt.Fprintf(os.Stderr, "  error: %s\n", msg)
	}
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}


func autoPrune(conn *sql.DB, cfg *config.Config, collectionName string) {
	if noPrune {
		return
	}
	result := indexer.PruneCollection(conn, cfg, collectionName)
	if result.Pruned > 0 {
		fmt.Printf("Pruned %d stale source(s) from %s\n", result.Pruned, collectionName)
	}
}
