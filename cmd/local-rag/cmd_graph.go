package main

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/graph"
)

var graphOrigins []string

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Build and inspect the relation graph over indexed sources",
	Long: `Graph derives relations between indexed sources -- Confluence page
hierarchy, frontmatter properties, wikilinks and ticket-key mentions -- and
stores them so a search result can be expanded into what it is connected to.

Every relation is parsed from data already in the database, so a rebuild costs
a scan and no embeddings.`,
}

var graphRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Derive relations from indexed content and replace the stored edges",
	Long: `Rebuild derives edges and replaces the stored set for each origin it
builds. Origins are replaced independently, so rebuilding one leaves the others
in place.

Origins: ` + strings.Join(graph.AllOrigins, ", "),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		stats, err := graph.Rebuild(conn, graphOrigins)
		if err != nil {
			return err
		}

		fmt.Printf("Rebuilt %d edges in %s\n\n", stats.Total(), stats.Elapsed.Round(time.Millisecond))
		fmt.Printf("%-16s %8s %12s %10s\n", "ORIGIN", "EDGES", "UNRESOLVED", "AMBIGUOUS")
		for _, origin := range graph.AllOrigins {
			st, ok := stats.ByOrigin[origin]
			if !ok {
				continue
			}
			fmt.Printf("%-16s %8d %12d %10d\n", origin, st.Edges, st.Unresolved, st.Ambiguous)
		}
		return nil
	},
}

var graphStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show what the stored graph contains",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()
		return printGraphStats(conn)
	},
}

func printGraphStats(conn *sql.DB) error {
	var total int
	if err := conn.QueryRow("SELECT COUNT(*) FROM graph_edges").Scan(&total); err != nil {
		return fmt.Errorf("count edges: %w", err)
	}
	if total == 0 {
		fmt.Println("No edges stored. Run 'local-rag graph rebuild' first.")
		return nil
	}

	var connected int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT src_source_id AS id FROM graph_edges
			UNION SELECT dst_source_id FROM graph_edges
		)`).Scan(&connected); err != nil {
		return fmt.Errorf("count connected sources: %w", err)
	}
	fmt.Printf("%d edges over %d connected sources\n\n", total, connected)

	if err := printGraphBreakdown(conn, "BY ORIGIN", "origin"); err != nil {
		return err
	}
	fmt.Println()
	if err := printGraphBreakdown(conn, "BY RELATION", "rel"); err != nil {
		return err
	}

	// Degree matters more than any other statistic here: expansion cost is
	// bounded by the degree of the nodes it starts from, so a handful of hubs
	// decide whether a traversal returns a neighbourhood or half the corpus.
	fmt.Println()
	rows, err := conn.Query(`
		SELECT s.source_path, COUNT(*) AS degree
		FROM (
			SELECT src_source_id AS id FROM graph_edges
			UNION ALL SELECT dst_source_id FROM graph_edges
		) e
		JOIN sources s ON s.id = e.id
		GROUP BY e.id
		ORDER BY degree DESC
		LIMIT 5`)
	if err != nil {
		return fmt.Errorf("query hubs: %w", err)
	}
	defer rows.Close()

	fmt.Printf("%-8s %s\n", "DEGREE", "HIGHEST-DEGREE SOURCES")
	for rows.Next() {
		var path string
		var degree int
		if err := rows.Scan(&path, &degree); err != nil {
			return fmt.Errorf("scan hub: %w", err)
		}
		fmt.Printf("%-8d %s\n", degree, shortenPath(path))
	}
	return rows.Err()
}

func printGraphBreakdown(conn *sql.DB, heading, column string) error {
	rows, err := conn.Query(
		"SELECT " + column + ", COUNT(*) FROM graph_edges GROUP BY 1 ORDER BY 2 DESC")
	if err != nil {
		return fmt.Errorf("group by %s: %w", column, err)
	}
	defer rows.Close()

	type row struct {
		name  string
		count int
	}
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.count); err != nil {
			return fmt.Errorf("scan %s: %w", column, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].count > out[j].count })

	fmt.Println(heading)
	for _, r := range out {
		fmt.Printf("  %-16s %8d\n", r.name, r.count)
	}
	return nil
}

// shortenPath keeps a path readable in a terminal table by dropping the
// leading directories nobody needs to read.
func shortenPath(path string) string {
	const max = 72
	if len(path) <= max {
		return path
	}
	return "..." + path[len(path)-max:]
}

func init() {
	graphRebuildCmd.Flags().StringSliceVar(&graphOrigins, "origin", nil,
		"Only rebuild these edge origins (default: all of "+strings.Join(graph.AllOrigins, ", ")+")")
	graphCmd.AddCommand(graphRebuildCmd)
	graphCmd.AddCommand(graphStatsCmd)
	rootCmd.AddCommand(graphCmd)
}
