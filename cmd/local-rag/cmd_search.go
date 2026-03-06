package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
	"github.com/sebastianhutter/local-rag-go/internal/search"
)

var (
	searchCollection string
	searchType       string
	searchFrom       string
	searchAuthor     string
	searchAfter      string
	searchBefore     string
	searchTop        int
	searchMeta       []string
)

var searchCmd = &cobra.Command{
	Use:   "search QUERY",
	Short: "Search indexed content",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		dbPath := cfg.ExpandedDBPath()
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			return fmt.Errorf("database not found at %s — run 'local-rag index' first", dbPath)
		}

		conn, err := db.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer conn.Close()

		queryEmbedding, err := embeddings.GetEmbedding(context.Background(), query, cfg.EmbeddingModel)
		if err != nil {
			return fmt.Errorf("failed to embed query (is Ollama running?): %w", err)
		}

		metadataFilters := make(map[string]string)
		for _, m := range searchMeta {
			parts := strings.SplitN(m, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid --meta format %q, expected key=value", m)
			}
			metadataFilters[parts[0]] = parts[1]
		}

		filters := &search.Filters{
			Collection:      searchCollection,
			SourceType:      searchType,
			Sender:          searchFrom,
			Author:          searchAuthor,
			DateFrom:        searchAfter,
			DateTo:          searchBefore,
			MetadataFilters: metadataFilters,
		}

		results, err := search.Search(conn, queryEmbedding, query, searchTop, filters, cfg)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		if len(results) == 0 {
			fmt.Println("No results found.")
			return nil
		}

		for i, r := range results {
			fmt.Printf("\n--- %d. %s ---\n", i+1, r.Title)
			fmt.Printf("  Collection: %s\n", r.Collection)
			fmt.Printf("  Type:       %s\n", r.SourceType)
			fmt.Printf("  Score:      %.4f\n", r.Score)
			fmt.Printf("  Source:     %s\n", r.SourcePath)
			if len(r.Metadata) > 0 {
				metaJSON, _ := json.Marshal(r.Metadata)
				fmt.Printf("  Metadata:   %s\n", string(metaJSON))
			}
			snippet := strings.ReplaceAll(r.Content, "\n", " ")
			if len(snippet) > 300 {
				snippet = snippet[:300] + "..."
			}
			fmt.Printf("  Content:    %s\n", snippet)
		}

		fmt.Printf("\n%d result(s) found.\n", len(results))
		return nil
	},
}

func init() {
	searchCmd.Flags().StringVarP(&searchCollection, "collection", "c", "", "Search within a specific collection")
	searchCmd.Flags().StringVar(&searchType, "type", "", "Filter by source type")
	searchCmd.Flags().StringVar(&searchFrom, "from", "", "Filter by email sender")
	searchCmd.Flags().StringVar(&searchAuthor, "author", "", "Filter by book author")
	searchCmd.Flags().StringVar(&searchAfter, "after", "", "Only results after this date (YYYY-MM-DD)")
	searchCmd.Flags().StringVar(&searchBefore, "before", "", "Only results before this date (YYYY-MM-DD)")
	searchCmd.Flags().IntVar(&searchTop, "top", 10, "Number of results")
	searchCmd.Flags().StringSliceVarP(&searchMeta, "meta", "m", nil, "Filter by metadata field (key=value, repeatable)")

	rootCmd.AddCommand(searchCmd)
}
