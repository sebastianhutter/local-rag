package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/gui"
)

var (
	verbose bool
	version = "dev"
)

var rootCmd = &cobra.Command{
	Version: version,
	Use:   "local-rag",
	Short: "Local RAG — privacy-preserving retrieval augmented generation",
	RunE: func(cmd *cobra.Command, args []string) error {
		return gui.Run()
	},
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		level := slog.LevelInfo
		if verbose {
			level = slog.LevelDebug
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable debug logging")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
