// Package cmd is the command-line surface of loki-mcp: the cobra command
// tree, the viper flag/environment binding behind it, and the startup wiring
// that turns resolved configuration into the values internal/server needs.
//
// # What belongs here
//
//   - root.go — rootCmd, the persistent flags, the envFlag helpers, and the
//     PersistentPreRunE that initialises logging, telemetry and the handler
//     config. The Loki connectivity check is not in the hook: it belongs to
//     the commands that serve traffic, so version, help and completion work
//     with Loki down.
//   - stdio.go, http.go — the two serving subcommands, each a thin RunE over
//     server.RunStdio / server.RunHTTP. rootCmd reuses stdio's RunE, so a bare
//     `loki-mcp` and `loki-mcp stdio` are the same command.
//   - version.go — the version subcommand.
//
// # What does not belong here
//
// Loki logic and MCP logic. This package only decides *what* to run and with
// which configuration; internal/handlers owns everything Loki, internal/server
// owns transports.
//
// # Concurrency
//
// Single-goroutine by construction. Every package-level variable — rootCmd and
// its subcommands, Version, handlerConfig, and viper's global registry — is
// written during package initialisation and PersistentPreRunE and only read
// afterwards, all on the goroutine that called Execute. Nothing here is safe
// for concurrent use and nothing needs to be. Tests that mutate viper or flag
// state must therefore not call t.Parallel().
//
// # Things that are not obvious from any single file
//
//   - viper is configured from a package-level variable initialiser in
//     root.go, not from an init() function, so that it runs before any init()
//     registers a flag regardless of file order. Prefix LOKI, AutomaticEnv,
//     and a "-" to "_" key replacer.
//   - envFlag and envBoolFlag derive the environment-variable name the same
//     way viper resolves it and append it to the flag's usage string, so
//     --help cannot go stale.
//   - handlerConfig is the only channel from PersistentPreRunE to a
//     subcommand's RunE; cobra offers no other way to pass state between them.
//   - rootCmd sets SilenceUsage so a runtime failure does not dump the usage
//     block, and restores it from a SetFlagErrorFunc hook so genuine
//     flag-parsing errors still print it.
//   - Version is not set by ldflags directly. -ldflags sets main.version, and
//     main assigns it to cmd.Version before calling Execute.
//
// # Known defects, reported not fixed
//
// FINDINGS.md documents behaviour of this package that a newcomer will
// otherwise mistake for a bug in their own change:
//
//   - §8: the port flag is bound to PORT by hand, but viper's AutomaticEnv
//     resolves LOKI_PORT first, so the undocumented LOKI_PORT wins.
package cmd
