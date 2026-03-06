package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
)

var collectionsCmd = &cobra.Command{
	Use:   "collections",
	Short: "Manage collections",
}

// --- collections list ---

var collectionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all collections with stats",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		conn, err := db.Open(cfg.ExpandedDBPath())
		if err != nil {
			return err
		}
		defer conn.Close()

		rows, err := conn.Query(`
			SELECT c.name, c.collection_type, c.created_at,
			       (SELECT COUNT(*) FROM sources s WHERE s.collection_id = c.id),
			       (SELECT COUNT(*) FROM documents d WHERE d.collection_id = c.id)
			FROM collections c ORDER BY c.name
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		fmt.Printf("%-25s %-10s %8s %8s  %-20s\n", "Name", "Type", "Sources", "Chunks", "Created")
		fmt.Println("----------------------------------------------------------------------")

		count := 0
		for rows.Next() {
			var name, collType, created string
			var sources, chunks int
			rows.Scan(&name, &collType, &created, &sources, &chunks)
			fmt.Printf("%-25s %-10s %8d %8d  %-20s\n", name, collType, sources, chunks, created)
			count++
		}

		if count == 0 {
			fmt.Println("No collections found.")
		}
		return nil
	},
}

// --- collections info ---

var collectionsInfoCmd = &cobra.Command{
	Use:   "info NAME",
	Short: "Show details of a collection",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		conn, err := db.Open(cfg.ExpandedDBPath())
		if err != nil {
			return err
		}
		defer conn.Close()

		var id int64
		var collType string
		var created string
		var description sql.NullString
		err = conn.QueryRow("SELECT id, collection_type, created_at, description FROM collections WHERE name = ?", name).
			Scan(&id, &collType, &created, &description)
		if err != nil {
			return fmt.Errorf("collection %q not found", name)
		}

		var sourceCount, docCount int
		conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ?", id).Scan(&sourceCount)
		conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", id).Scan(&docCount)

		var lastIndexed sql.NullString
		conn.QueryRow("SELECT MAX(last_indexed_at) FROM sources WHERE collection_id = ?", id).Scan(&lastIndexed)

		fmt.Printf("Collection:   %s\n", name)
		fmt.Printf("Type:         %s\n", collType)
		fmt.Printf("Created:      %s\n", created)
		if description.Valid && description.String != "" {
			fmt.Printf("Description:  %s\n", description.String)
		}
		fmt.Printf("Sources:      %d\n", sourceCount)
		fmt.Printf("Chunks:       %d\n", docCount)
		if lastIndexed.Valid {
			fmt.Printf("Last indexed: %s\n", lastIndexed.String)
		}

		// Source type breakdown
		typeRows, err := conn.Query("SELECT source_type, COUNT(*) FROM sources WHERE collection_id = ? GROUP BY source_type ORDER BY source_type", id)
		if err == nil {
			defer typeRows.Close()
			fmt.Println("\nSource types:")
			for typeRows.Next() {
				var st string
				var cnt int
				typeRows.Scan(&st, &cnt)
				fmt.Printf("  %-15s %d\n", st, cnt)
			}
		}

		// Sample titles
		sampleRows, err := conn.Query("SELECT DISTINCT title FROM documents WHERE collection_id = ? AND title IS NOT NULL LIMIT 5", id)
		if err == nil {
			defer sampleRows.Close()
			fmt.Println("\nSample titles:")
			for sampleRows.Next() {
				var t string
				sampleRows.Scan(&t)
				fmt.Printf("  - %s\n", t)
			}
		}

		return nil
	},
}

// --- collections delete ---

var deleteYes bool

var collectionsDeleteCmd = &cobra.Command{
	Use:   "delete NAME",
	Short: "Delete a collection and all its data",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		conn, err := db.Open(cfg.ExpandedDBPath())
		if err != nil {
			return err
		}
		defer conn.Close()

		var id int64
		err = conn.QueryRow("SELECT id FROM collections WHERE name = ?", name).Scan(&id)
		if err != nil {
			return fmt.Errorf("collection %q not found", name)
		}

		if !deleteYes {
			var docCount int
			conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", id).Scan(&docCount)
			fmt.Printf("Delete collection '%s' and all %d documents? [y/N] ", name, docCount)
			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		// Delete vector entries first
		conn.Exec(`DELETE FROM vec_documents WHERE document_id IN (SELECT id FROM documents WHERE collection_id = ?)`, id)
		// CASCADE handles sources and documents
		conn.Exec("DELETE FROM collections WHERE id = ?", id)

		fmt.Printf("Collection '%s' deleted.\n", name)
		return nil
	},
}

// --- collections export (optional utility) ---

var collectionsExportCmd = &cobra.Command{
	Use:   "export NAME",
	Short: "Export collection metadata as JSON",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		conn, err := db.Open(cfg.ExpandedDBPath())
		if err != nil {
			return err
		}
		defer conn.Close()

		var id int64
		err = conn.QueryRow("SELECT id FROM collections WHERE name = ?", name).Scan(&id)
		if err != nil {
			return fmt.Errorf("collection %q not found", name)
		}

		rows, err := conn.Query("SELECT source_type, source_path, file_hash, last_indexed_at FROM sources WHERE collection_id = ?", id)
		if err != nil {
			return err
		}
		defer rows.Close()

		type sourceInfo struct {
			Type        string `json:"type"`
			Path        string `json:"path"`
			Hash        string `json:"hash,omitempty"`
			LastIndexed string `json:"last_indexed,omitempty"`
		}
		var sources []sourceInfo
		for rows.Next() {
			var s sourceInfo
			var hash, lastIndexed sql.NullString
			rows.Scan(&s.Type, &s.Path, &hash, &lastIndexed)
			if hash.Valid {
				s.Hash = hash.String
			}
			if lastIndexed.Valid {
				s.LastIndexed = lastIndexed.String
			}
			sources = append(sources, s)
		}

		out, _ := json.MarshalIndent(map[string]any{
			"collection": name,
			"sources":    sources,
		}, "", "  ")
		os.Stdout.Write(out)
		fmt.Println()
		return nil
	},
}

// --- collections paths ---

var pathsCmd = &cobra.Command{
	Use:   "paths",
	Short: "Manage collection paths",
}

var pathsListCmd = &cobra.Command{
	Use:   "list NAME",
	Short: "List stored paths for a collection",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		conn, err := db.Open(cfg.ExpandedDBPath())
		if err != nil {
			return err
		}
		defer conn.Close()

		paths, err := db.GetCollectionPaths(conn, name)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return fmt.Errorf("no paths stored for collection %q", name)
		}
		for _, p := range paths {
			fmt.Println(p)
		}
		return nil
	},
}

var pathsAddCmd = &cobra.Command{
	Use:   "add NAME PATH...",
	Short: "Add paths to a collection",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		newPaths := args[1:]

		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		conn, err := db.Open(cfg.ExpandedDBPath())
		if err != nil {
			return err
		}
		defer conn.Close()

		if err := db.InitSchema(conn, cfg.EmbeddingDimensions); err != nil {
			return err
		}

		// Resolve paths to absolute
		resolved := make([]string, 0, len(newPaths))
		for _, p := range newPaths {
			p = expandHome(p)
			abs, err := filepath.Abs(p)
			if err != nil {
				return fmt.Errorf("resolve path %q: %w", p, err)
			}
			resolved = append(resolved, abs)
		}

		// Load existing paths (collection may not exist yet)
		existing, _ := db.GetCollectionPaths(conn, name)

		// Merge and deduplicate
		seen := make(map[string]bool, len(existing))
		for _, p := range existing {
			seen[p] = true
		}
		merged := append([]string{}, existing...)
		for _, p := range resolved {
			if !seen[p] {
				merged = append(merged, p)
				seen[p] = true
			}
		}

		// Ensure collection exists, then set paths
		if _, err := db.GetOrCreateCollection(conn, name, "project", nil, merged); err != nil {
			return err
		}

		fmt.Printf("Paths for %q:\n", name)
		for _, p := range merged {
			fmt.Printf("  %s\n", p)
		}
		return nil
	},
}

var pathsRemoveCmd = &cobra.Command{
	Use:   "remove NAME PATH...",
	Short: "Remove paths from a collection",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		removePaths := args[1:]

		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		conn, err := db.Open(cfg.ExpandedDBPath())
		if err != nil {
			return err
		}
		defer conn.Close()

		existing, err := db.GetCollectionPaths(conn, name)
		if err != nil {
			return err
		}

		// Resolve paths for comparison
		removeSet := make(map[string]bool, len(removePaths))
		for _, p := range removePaths {
			p = expandHome(p)
			abs, err := filepath.Abs(p)
			if err != nil {
				removeSet[p] = true
			} else {
				removeSet[abs] = true
			}
		}

		var remaining []string
		for _, p := range existing {
			if !removeSet[p] {
				remaining = append(remaining, p)
			}
		}

		if err := db.SetCollectionPaths(conn, name, remaining); err != nil {
			return err
		}

		if len(remaining) == 0 {
			fmt.Printf("No paths remaining for %q.\n", name)
		} else {
			fmt.Printf("Paths for %q:\n", name)
			for _, p := range remaining {
				fmt.Printf("  %s\n", p)
			}
		}
		return nil
	},
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

func init() {
	collectionsDeleteCmd.Flags().BoolVarP(&deleteYes, "yes", "y", false, "Skip confirmation")

	pathsCmd.AddCommand(pathsListCmd)
	pathsCmd.AddCommand(pathsAddCmd)
	pathsCmd.AddCommand(pathsRemoveCmd)

	collectionsCmd.AddCommand(collectionsListCmd)
	collectionsCmd.AddCommand(collectionsInfoCmd)
	collectionsCmd.AddCommand(collectionsDeleteCmd)
	collectionsCmd.AddCommand(collectionsExportCmd)
	collectionsCmd.AddCommand(pathsCmd)

	rootCmd.AddCommand(collectionsCmd)
}
