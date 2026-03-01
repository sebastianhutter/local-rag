package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var servePort int

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		// MCP server implementation in Phase 5
		if servePort > 0 {
			fmt.Printf("Starting MCP server on port %d... (not yet implemented — Phase 5)\n", servePort)
		} else {
			fmt.Println("Starting MCP server (stdio)... (not yet implemented — Phase 5)")
		}
		return nil
	},
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "Port for HTTP/SSE transport (default: stdio)")
	rootCmd.AddCommand(serveCmd)
}
