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

// envUsage appends the environment variable viper will resolve for the flag to
// its usage string, so --help surfaces it. The name is derived the same way
// viper's AutomaticEnv + key replacer derive it: <PREFIX>_<NAME>, upper-cased,
// dashes replaced by underscores. Deriving it here rather than spelling it out
// at each call site keeps --help from going stale.
func envUsage(name, usage string) string {
	env := strings.ReplaceAll(strings.ToUpper(name), "-", "_")
	if prefix := viper.GetEnvPrefix(); prefix != "" {
		env = prefix + "_" + env
	}
	return fmt.Sprintf("%s [env: %s]", usage, env)
}

// envFlag registers a string flag on fs, binds it to viper, and documents its
// environment variable.
func envFlag(fs *pflag.FlagSet, name, shorthand, def, usage string) {
	fs.StringP(name, shorthand, def, envUsage(name, usage))
	_ = viper.BindPFlag(name, fs.Lookup(name))
}

// envBoolFlag is envFlag's boolean equivalent.
func envBoolFlag(fs *pflag.FlagSet, name, shorthand string, def bool, usage string) {
	fs.BoolP(name, shorthand, def, envUsage(name, usage))
	_ = viper.BindPFlag(name, fs.Lookup(name))
}

// Version is the version string this binary reports: to `loki-mcp version`, to
// MCP clients as the server version, and to OpenTelemetry as service.version.
//
// It is not set by ldflags. -ldflags sets main.version; main assigns it here
// before calling Execute, so this variable is authoritative only after that
// assignment. A binary built without ldflags — `go build`, `make build`, `go
// test` — reports "dev".
//
// Write it once, from main, before Execute. Nothing synchronises it.
var Version = "dev"

// Configure viper at package-init time (before any file's init() registers
// flags), so envFlag can resolve the env prefix regardless of init() order.
var _ = func() bool {
	viper.SetEnvPrefix("LOKI")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	return true
}()

// rootCmd carries the persistent flags and the startup hook shared by every
// subcommand. Its RunE is stdio's, so a bare `loki-mcp` serves stdio rather
// than printing help and exiting 0; being runnable also means cobra runs its
// PersistentPreRunE for the bare invocation.
var rootCmd = &cobra.Command{
	Use:   "loki-mcp",
	Short: "MCP server for Grafana Loki",
	Long: `An MCP (Model Context Protocol) server that provides Grafana Loki query tools
to AI assistants like Claude Desktop, Cursor, and n8n.

With no subcommand, the server runs in stdio mode. Use subcommands to pick a
mode explicitly:
  - stdio: For Claude Desktop and similar MCP clients (the default)
  - http:  For web-based clients and n8n`,
	RunE: runStdio,
	// A runtime failure — an unreachable Loki, a port already taken — is not
	// misuse of the command line, so it must not dump the usage block after
	// the error. Genuine flag-parsing errors still get it; see the
	// SetFlagErrorFunc hook in init().
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
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

		// pingLoki is deliberately not called here: cobra runs this hook for
		// every runnable command, and version, help and completion have no
		// business needing a reachable Loki — they must work offline and exit
		// 0. The serving commands ping from their own RunE instead.
		return nil
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

// pingLoki verifies connectivity to Loki before the server starts. Every
// command that serves traffic calls it first — the bare invocation and stdio
// through runStdio, http through its own RunE — and no other command calls it
// at all.
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

// Execute runs the command tree against os.Args and is the whole body of
// main. Subcommands are registered by each file's init(), not here.
//
// It either returns normally, having run a subcommand that succeeded, or exits
// the process with status 1. It never returns an error, and a failing
// subcommand's own cleanup — telemetry flushing in particular — has already
// run by then, because it happens in deferred calls inside the Run* functions
// rather than out here.
//
// Cobra prints the error on failure. The usage block comes with it only for
// flag-parsing errors and unknown subcommands, not for runtime failures such
// as an unreachable Loki; see rootCmd's SilenceUsage.
//
// Call it once, from the main goroutine.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// rootCmd.SilenceUsage suppresses the usage block for every error, which
	// is wrong for the one class of error it genuinely helps with: a
	// mistyped or malformed flag. Cobra reads SilenceUsage only after
	// execute() returns, so clearing it from here — the hook cobra calls the
	// moment flag parsing fails — restores the usage block for exactly those
	// errors, in cobra's own "Error: …" then usage order. Unknown
	// subcommands take a different path and keep their own hint.
	rootCmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		c.SilenceUsage = false
		rootCmd.SilenceUsage = false
		return err
	})

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
