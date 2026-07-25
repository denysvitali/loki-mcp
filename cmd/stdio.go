package cmd

import (
	"github.com/spf13/cobra"

	"github.com/kaiko-ai/loki-mcp/internal/logging"
	"github.com/kaiko-ai/loki-mcp/internal/server"
)

var stdioCmd = &cobra.Command{
	Use:   "stdio",
	Short: "Run the MCP server in stdio mode",
	Long: `Run the MCP server in stdio mode for integration with Claude Desktop
and other MCP clients that communicate via stdin/stdout.

All log output is sent to stderr to avoid interfering with the MCP protocol.`,
	RunE: runStdio,
}

// runStdio is the body of `loki-mcp stdio`. rootCmd uses it as its own RunE,
// so a bare `loki-mcp` is the same command rather than a help dump
// (FINDINGS.md §1): every MCP client config that passes "args": [] gets a
// working stdio server.
func runStdio(_ *cobra.Command, _ []string) error {
	if err := pingLoki(); err != nil {
		return err
	}

	if err := server.RunStdio(Version, handlerConfig); err != nil {
		logging.Errorf("Stdio server error: %v", err)
		return err
	}
	return nil
}

func init() {
	rootCmd.AddCommand(stdioCmd)
}
