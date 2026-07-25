package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := pingLoki(); err != nil {
			return err
		}

		cfg := server.HTTPConfig{
			Host:       viper.GetString("host"),
			Port:       resolvePort(cmd.Flags()),
			DisableSSE: viper.GetBool("disable-sse"),
		}

		if err := server.RunHTTP(Version, cfg, handlerConfig); err != nil {
			logging.Errorf("HTTP server error: %v", err)
			return err
		}
		return nil
	},
}

// defaultPort is the port the HTTP server binds when neither the flag nor the
// environment says otherwise.
const defaultPort = "8080"

// resolvePort returns the HTTP port that flags — the http command's flag set —
// and the environment ask for. Precedence, highest first:
//
//  1. --port, but only when it was actually passed on the command line;
//  2. the PORT environment variable, when set to a non-empty value;
//  3. defaultPort.
//
// LOKI_PORT is deliberately not consulted. It used to win over PORT, because
// the port key is registered with viper and viper's AutomaticEnv resolves the
// prefixed LOKI_PORT before any explicit BindEnv alias — so the undocumented
// name silently shadowed the documented one (FINDINGS.md §8). Reading the two
// sources by hand here is what keeps the order above true and visible; viper
// has no per-key way to express it.
func resolvePort(flags *pflag.FlagSet) string {
	if f := flags.Lookup("port"); f != nil && f.Changed {
		return f.Value.String()
	}
	if port, ok := os.LookupEnv("PORT"); ok && port != "" {
		return port
	}

	return defaultPort
}

func init() {
	rootCmd.AddCommand(httpCmd)

	envFlag(httpCmd.Flags(), "host", "", "0.0.0.0", "HTTP server bind address")

	envBoolFlag(httpCmd.Flags(), "disable-sse", "", false,
		"Disable legacy SSE endpoints (/sse, /mcp)")

	// PORT (without LOKI_ prefix) is the conventional env var for HTTP port.
	// The flag is registered by hand because envFlag would document it as
	// LOKI_PORT, and it is left unbound from viper on purpose: binding it
	// would put it back under AutomaticEnv, where LOKI_PORT outranks PORT.
	// resolvePort reads the flag and PORT directly instead.
	httpCmd.Flags().StringP("port", "p", defaultPort, "HTTP server port [env: PORT]")
}
