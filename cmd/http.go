package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/kaiko-ai/loki-mcp/internal/logging"
	"github.com/kaiko-ai/loki-mcp/internal/server"
)

var httpCmd = &cobra.Command{
	Use:   "http",
	Short: "Run the MCP server in HTTP mode",
	Long: `Run the MCP server in HTTP mode with streamable HTTP endpoint.

Endpoints:
  /stream - Streamable HTTP endpoint (modern clients)
  /sse    - SSE event stream (legacy, can be disabled)
  /mcp    - SSE message endpoint (legacy, can be disabled)`,
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg := server.HTTPConfig{
			Host:       viper.GetString("host"),
			Port:       viper.GetString("port"),
			DisableSSE: viper.GetBool("disable-sse"),
		}

		if err := server.RunHTTP(Version, cfg, handlerConfig); err != nil {
			logging.Errorf("HTTP server error: %v", err)
			return err
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(httpCmd)

	envFlag(httpCmd.Flags(), "host", "", "0.0.0.0", "HTTP server bind address")

	envBoolFlag(httpCmd.Flags(), "disable-sse", "", false,
		"Disable legacy SSE endpoints (/sse, /mcp)")

	// PORT (without LOKI_ prefix) is the conventional env var for HTTP port.
	// It is registered by hand because envFlag would document it as
	// LOKI_PORT. Note that LOKI_PORT does resolve too, and outranks PORT:
	// viper checks AutomaticEnv before any explicit BindEnv.
	httpCmd.Flags().StringP("port", "p", "8080", "HTTP server port [env: PORT]")
	_ = viper.BindPFlag("port", httpCmd.Flags().Lookup("port"))
	_ = viper.BindEnv("port", "PORT")
}
