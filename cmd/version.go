package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Long:  `Print the version number of loki-mcp.`,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println("loki-mcp", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
