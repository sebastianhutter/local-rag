package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/graph"
)

var (
	neighborsRels    []string
	neighborsOrigins []string
	neighborsHops    int
	neighborsHubCap  int
	neighborsTop     int
)

var neighborsCmd = &cobra.Command{
	Use:   "neighbors SOURCE",
	Short: "Show what an indexed source is connected to",
	Long: `Neighbors walks the relation graph out from one source and lists what it
is connected to -- linked notes, a page's parent and children, the tickets it
mentions, whatever mentions it.

SOURCE is either a source id (as reported by search) or a case-insensitive
substring of a file path. A substring matching several sources is rejected
rather than guessed at.

Run 'local-rag graph rebuild' first: without stored edges there is nothing to
walk.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, conn, err := openConfigAndDB()
		if err != nil {
			return err
		}
		defer conn.Close()

		seed, path, err := resolveSource(conn, args[0])
		if err != nil {
			return err
		}

		neighbours, err := graph.Expand(conn, []int64{seed}, graph.ExpandOptions{
			Rels:    neighborsRels,
			Origins: neighborsOrigins,
			Hops:    neighborsHops,
			HubCap:  neighborsHubCap,
			Limit:   neighborsTop,
		})
		if err != nil {
			return err
		}

		fmt.Printf("%s (source %d)\n", path, seed)
		if len(neighbours) == 0 {
			fmt.Println("\nNo neighbours. Either this source has no edges, or it is a hub and " +
				"expansion stopped there (raise --hub-cap to expand through it anyway).")
			return nil
		}

		fmt.Printf("\n%d neighbour(s):\n", len(neighbours))
		for i, n := range neighbours {
			fmt.Printf("\n--- %d. %s ---\n", i+1, displayTitle(n))
			fmt.Printf("  Relation:   %s (%s, %d hop", n.Rel, n.Origin, n.Hops)
			if n.Hops != 1 {
				fmt.Print("s")
			}
			fmt.Printf(", degree %d)\n", n.Degree)
			fmt.Printf("  Collection: %s\n", n.Collection)
			fmt.Printf("  Source:     %s\n", n.SourcePath)
			if n.Snippet != "" {
				fmt.Printf("  Content:    %s\n", n.Snippet)
			}
		}
		return nil
	},
}

func displayTitle(n graph.Neighbour) string {
	if n.Title != "" {
		return n.Title
	}
	return shortenPath(n.SourcePath)
}

// resolveSource turns a CLI argument into one source id. A numeric argument is
// an id; anything else is matched as a path substring. Ambiguity is an error
// rather than a guess: picking one of several files silently would make the
// output impossible to trust.
func resolveSource(conn *sql.DB, arg string) (int64, string, error) {
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		var path string
		if err := conn.QueryRow("SELECT source_path FROM sources WHERE id = ?", id).Scan(&path); err != nil {
			if err == sql.ErrNoRows {
				return 0, "", fmt.Errorf("no source with id %d", id)
			}
			return 0, "", fmt.Errorf("look up source %d: %w", id, err)
		}
		return id, path, nil
	}

	rows, err := conn.Query(
		"SELECT id, source_path FROM sources WHERE lower(source_path) LIKE ? ORDER BY id LIMIT 11",
		"%"+strings.ToLower(arg)+"%")
	if err != nil {
		return 0, "", fmt.Errorf("search sources: %w", err)
	}
	defer rows.Close()

	type match struct {
		id   int64
		path string
	}
	var matches []match
	for rows.Next() {
		var m match
		if err := rows.Scan(&m.id, &m.path); err != nil {
			return 0, "", fmt.Errorf("scan source: %w", err)
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}

	switch len(matches) {
	case 0:
		return 0, "", fmt.Errorf("no indexed source matches %q", arg)
	case 1:
		return matches[0].id, matches[0].path, nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%q matches %d sources:\n", arg, len(matches))
		for i, m := range matches {
			if i == 10 {
				fmt.Fprintf(&b, "  ... and more\n")
				break
			}
			fmt.Fprintf(&b, "  %d  %s\n", m.id, m.path)
		}
		b.WriteString("Narrow the substring, or pass one of the ids above.")
		return 0, "", fmt.Errorf("%s", b.String())
	}
}

func init() {
	neighborsCmd.Flags().StringSliceVar(&neighborsRels, "rel", nil,
		"Only follow these relations (e.g. links_to, child_of, mentions, related)")
	neighborsCmd.Flags().StringSliceVar(&neighborsOrigins, "origin", nil,
		"Only follow edges from these origins ("+strings.Join(graph.AllOrigins, ", ")+")")
	neighborsCmd.Flags().IntVar(&neighborsHops, "hops", graph.DefaultHops,
		fmt.Sprintf("Traversal depth (max %d)", graph.MaxHops))
	neighborsCmd.Flags().IntVar(&neighborsHubCap, "hub-cap", graph.DefaultHubCap,
		"Do not expand through a source with more edges than this")
	neighborsCmd.Flags().IntVar(&neighborsTop, "top", graph.DefaultLimit,
		"Maximum neighbours to return")
	rootCmd.AddCommand(neighborsCmd)
}
