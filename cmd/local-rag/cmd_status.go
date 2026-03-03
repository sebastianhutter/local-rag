package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show overall database stats",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}

		dbPath := cfg.ExpandedDBPath()
		info, err := os.Stat(dbPath)
		if os.IsNotExist(err) {
			fmt.Println("No database found. Run 'local-rag index' to create one.")
			return nil
		}

		conn, err := db.Open(dbPath)
		if err != nil {
			return err
		}
		defer conn.Close()

		var collectionCount, sourceCount, docCount int
		conn.QueryRow("SELECT COUNT(*) FROM collections").Scan(&collectionCount)
		conn.QueryRow("SELECT COUNT(*) FROM sources").Scan(&sourceCount)
		conn.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)

		var lastIndexed sql.NullString
		conn.QueryRow("SELECT MAX(last_indexed_at) FROM sources").Scan(&lastIndexed)

		sizeMB := float64(info.Size()) / (1024 * 1024)

		fmt.Printf("Database:     %s\n", dbPath)
		fmt.Printf("Size:         %.1f MB\n", sizeMB)
		fmt.Printf("Collections:  %d\n", collectionCount)
		fmt.Printf("Sources:      %d\n", sourceCount)
		fmt.Printf("Chunks:       %d\n", docCount)
		if lastIndexed.Valid {
			fmt.Printf("Last indexed: %s\n", lastIndexed.String)
		} else {
			fmt.Printf("Last indexed: never\n")
		}
		fmt.Printf("Model:        %s (%dd)\n", cfg.EmbeddingModel, cfg.EmbeddingDimensions)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
