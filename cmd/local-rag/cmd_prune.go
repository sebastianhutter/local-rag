package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/indexer"
)

var pruneVectors bool
var pruneYes bool

var pruneCmd = &cobra.Command{
	Use:   "prune [COLLECTION]",
	Short: "Remove stale sources whose originals no longer exist",
	Long: `Prune removes indexed entries for files that have been deleted or moved,
emails removed from eM Client, RSS articles purged from NetNewsWire,
books removed from Calibre, or code files deleted from repositories.

Without arguments, prunes all collections. With a collection name argument,
prunes only that collection.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if pruneVectors {
			return pruneOrphanedVectors()
		}

		cfg, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		if len(args) > 0 {
			name := args[0]
			result := indexer.PruneCollection(conn, cfg, name)
			printPruneResult(name, result)
		} else {
			result := indexer.PruneAll(conn, cfg)
			printPruneResult("all", result)
		}

		return nil
	},
}

func init() {
	pruneCmd.Flags().BoolVar(&pruneVectors, "vectors", false,
		"Remove orphaned embeddings (vectors whose document no longer exists)")
	pruneCmd.Flags().BoolVarP(&pruneYes, "yes", "y", false, "Skip confirmation")
	rootCmd.AddCommand(pruneCmd)
}

func printPruneResult(label string, result *indexer.PruneResult) {
	if result.Pruned == 0 && result.Errors == 0 {
		fmt.Printf("%s: no stale sources found (%d checked)\n", label, result.Checked)
		return
	}
	errStr := fmt.Sprintf("%d errors", result.Errors)
	if result.Errors > 0 {
		errStr = fmt.Sprintf("%d errors!", result.Errors)
	}
	fmt.Printf("%s: pruned %d stale sources (%d checked, %s)\n",
		label, result.Pruned, result.Checked, errStr)
	for _, msg := range result.ErrorMessages {
		fmt.Fprintf(os.Stderr, "  error: %s\n", msg)
	}
}

// pruneOrphanedVectors removes embeddings whose document no longer exists.
//
// These accumulate from any code path that deleted documents without clearing
// their vectors — the vec0 tables have no foreign keys, so a cascade alone
// leaves them behind. They are not just wasted space: the binary KNN returns
// them as candidates that resolve to no document, shrinking the effective
// result pool for every search.
func pruneOrphanedVectors() error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	conn, err := db.Open(cfg.ExpandedDBPath())
	if err != nil {
		return err
	}
	defer conn.Close()

	stats, err := db.CountOrphanedVectors(conn)
	if err != nil {
		return err
	}
	fmt.Printf("documents: %d, vectors: %d, orphaned: %d\n",
		stats.Documents, stats.Vectors, stats.Orphaned)
	if stats.Orphaned == 0 {
		fmt.Println("Nothing to clean up.")
		return nil
	}

	if !pruneYes {
		fmt.Printf("Delete %d orphaned embeddings? [y/N] ", stats.Orphaned)
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	deleted, err := db.DeleteOrphanedVectors(conn)
	if err != nil {
		return fmt.Errorf("after deleting %d: %w", deleted, err)
	}
	fmt.Printf("Deleted %d orphaned embeddings.\n", deleted)
	return nil
}
