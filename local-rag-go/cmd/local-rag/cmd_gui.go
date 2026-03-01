package main

import (
	"github.com/spf13/cobra"

	"github.com/sebastianhutter/local-rag-go/internal/gui"
)

var guiCmd = &cobra.Command{
	Use:   "gui",
	Short: "Start the GUI menu bar application",
	RunE: func(cmd *cobra.Command, args []string) error {
		return gui.Run()
	},
}

func init() {
	rootCmd.AddCommand(guiCmd)
}
