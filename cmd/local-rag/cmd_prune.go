package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/indexer"
)

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
