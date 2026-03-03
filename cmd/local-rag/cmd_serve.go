package main

import (
	"github.com/spf13/cobra"

	localMCP "github.com/sebastianhutter/local-rag-go/internal/mcp"
)

var servePort int

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		if servePort > 0 {
			return localMCP.ServeSSE(servePort)
		}
		return localMCP.ServeStdio()
	},
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "Port for HTTP/SSE transport (default: stdio)")
	rootCmd.AddCommand(serveCmd)
}
