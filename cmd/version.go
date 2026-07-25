package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Long:  `Print the version number of loki-mcp.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// A failed write to the version writer is not worth a nonzero exit;
		// there is nowhere left to report it to.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "loki-mcp", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
