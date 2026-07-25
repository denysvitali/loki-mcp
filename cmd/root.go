package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/logging"
	"github.com/kaiko-ai/loki-mcp/internal/telemetry"
)

// envFlag registers a string flag on fs, binds it to viper, and appends the
// resolved env var to the usage string so --help surfaces it. The env var is
// derived as <LOKI_PREFIX>_<NAME> with dashes replaced by underscores, matching
// viper's AutomaticEnv + key replacer configuration.
func envFlag(fs *pflag.FlagSet, name, shorthand, def, usage string) {
	env := strings.ReplaceAll(strings.ToUpper(name), "-", "_")
	if prefix := viper.GetEnvPrefix(); prefix != "" {
		env = prefix + "_" + env
	}
	fs.StringP(name, shorthand, def, fmt.Sprintf("%s [env: %s]", usage, env))
	_ = viper.BindPFlag(name, fs.Lookup(name))
}

// envBoolFlag is envFlag's boolean equivalent.
func envBoolFlag(fs *pflag.FlagSet, name, shorthand string, def bool, usage string) {
	env := strings.ReplaceAll(strings.ToUpper(name), "-", "_")
	if prefix := viper.GetEnvPrefix(); prefix != "" {
		env = prefix + "_" + env
	}
	fs.BoolP(name, shorthand, def, fmt.Sprintf("%s [env: %s]", usage, env))
	_ = viper.BindPFlag(name, fs.Lookup(name))
}

// Version is set via ldflags at build time
var Version = "dev"

// Configure viper at package-init time (before any file's init() registers
// flags), so envFlag can resolve the env prefix regardless of init() order.
var _ = func() bool {
	viper.SetEnvPrefix("LOKI")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	return true
}()

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "loki-mcp",
	Short: "MCP server for Grafana Loki",
	Long: `An MCP (Model Context Protocol) server that provides Grafana Loki query tools
to AI assistants like Claude Desktop, Cursor, and n8n.

Use subcommands to run the server in different modes:
  - stdio: For Claude Desktop and similar MCP clients
  - http:  For web-based clients and n8n`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := initLogging(); err != nil {
			return err
		}

		if err := initTelemetry(cmd.Context()); err != nil {
			return err
		}

		cfg, err := buildHandlerConfig()
		if err != nil {
			return err
		}
		handlerConfig = cfg

		return pingLoki()
	},
}

// handlerConfig is built once in PersistentPreRunE and read by the stdio and
// http subcommands. Cobra gives no other way to pass state between a persistent
// pre-run hook and a command's RunE.
var handlerConfig handlers.Config

// initLogging configures the global logger from the resolved flags.
func initLogging() error {
	return logging.Init(viper.GetString("log-level"), viper.GetString("log-format"))
}

// initTelemetry starts the OpenTelemetry providers and bridges logrus into them.
func initTelemetry(ctx context.Context) error {
	if err := telemetry.Initialize(ctx, telemetry.Config{
		ServiceVersion: Version,
		Stderr:         viper.GetBool("otel-stderr"),
	}); err != nil {
		return fmt.Errorf("failed to initialize OpenTelemetry: %w", err)
	}
	logging.AddHook(telemetry.NewLogrusHook(Version))
	return nil
}

// buildHandlerConfig assembles the immutable handler configuration and reports
// what it decided.
func buildHandlerConfig() (handlers.Config, error) {
	secretFilter := viper.GetBool("secret-filter")
	queryFilter := viper.GetString("query-filter")

	cfg, err := handlers.NewConfig(queryFilter, secretFilter)

	// The secret-filter decision is reported before the query filter is
	// validated, so an invalid --query-filter still logs it before failing.
	if secretFilter {
		logging.Info("Secret filtering enabled")
	} else {
		logging.Warn("Secret filtering disabled")
	}

	if err != nil {
		return handlers.Config{}, err
	}
	if queryFilter != "" {
		logging.Infof("Query filter configured: %s", queryFilter)
	}

	return cfg, nil
}

// pingLoki verifies connectivity to Loki before the server starts.
func pingLoki() error {
	params := &handlers.LokiParams{
		URL:      viper.GetString("url"),
		Username: viper.GetString("username"),
		Password: viper.GetString("password"),
		Token:    viper.GetString("token"),
		Org:      viper.GetString("org-id"),
	}
	if err := handlers.PingLoki(params); err != nil {
		return fmt.Errorf("loki connectivity check failed: %w", err)
	}
	logging.Infof("Loki reachable at %s", params.URL)

	return nil
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// Persistent flags
	pf := rootCmd.PersistentFlags()
	envFlag(pf, "query-filter", "f", "",
		"LogQL stream selector to restrict all queries (e.g., {namespace=\"prod\"})")
	envBoolFlag(pf, "secret-filter", "", true,
		"Scan returned log lines for secrets and omit matching lines")
	envBoolFlag(pf, "otel-stderr", "", false,
		"Enable OpenTelemetry console exporters on stderr")
	envFlag(pf, "log-level", "l", "info", "Log level: debug, info, warn, error")
	envFlag(pf, "log-format", "", "text", "Log format: text, json")

	// Loki connection flags
	envFlag(pf, "url", "u", "http://localhost:3100", "Loki server URL")
	envFlag(pf, "org-id", "", "", "Loki org ID for multi-tenant setups (X-Scope-OrgID)")
	envFlag(pf, "username", "", "", "Basic auth username")
	envFlag(pf, "password", "", "", "Basic auth password")
	envFlag(pf, "token", "", "", "Bearer token")
}
