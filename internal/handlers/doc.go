// Package handlers implements every Loki-facing concern of this binary: the
// MCP tool schemas, the tool handlers, and all the domain logic they call.
//
// # What belongs here
//
// Anything that knows about Loki, LogQL or the shape of tool output:
//
//   - config.go — Config, the immutable process-wide handler configuration.
//   - tools.go — the three mcp.Tool schemas (loki_query, loki_label_names,
//     loki_label_values).
//   - params.go — LokiParams, its viper-backed defaults, the shared
//     start/end resolution, and the logcli client constructor.
//   - query.go, labels.go — the three tool handlers.
//   - filter.go — LogQL parsing and injection of the configured stream
//     selector into a client's query AST.
//   - entries.go — timestamp parsing, flattening streams into entries,
//     keyword filtering, head/tail, regrouping.
//   - secret_filter.go — the betterleaks scan that drops secret-bearing lines.
//   - format.go — the Format type and the raw/json/text renderers.
//   - loki_http.go — LokiHTTPClient, a hand-rolled client for the two label
//     endpoints, used only when a query filter must be passed to Loki.
//
// # What does not belong here
//
// Transports and process wiring. Building the mcp-go server and serving it
// over stdio or HTTP is internal/server; cobra commands and flag/environment
// binding are cmd.
//
// That boundary has one deliberate leak, recorded rather than defended:
// (*LokiParams).applyDefaults and the *Tool constructors call viper directly
// to fill in connection defaults (url, username, password, token, org-id) that
// the client did not supply. Nothing else in this package reads configuration
// from anywhere but its Config.
//
// # Concurrency
//
// Config is built once, by NewConfig, and never written again. New copies it
// into the Handlers value, so every method of *Handlers reads immutable state
// and is safe to call from any goroutine; there is no lock in this package and
// none is needed.
//
// The one piece of shared mutable state reachable from a handler is
// Config.Detector, a single *detect.Detector shared by all concurrent scans.
// NewConfig sets SkipFindingAppend so scans do not accumulate findings into
// the detector. There is one known concurrency hazard in that dependency — two
// *distinct* detectors racing on a process-global inside tiktoken-go, see
// config.go's sharedDetector — which a single process-wide detector avoids, and
// which is why tests that construct a detector must not call t.Parallel().
//
// filterSecretEntries polls ctx.Done() during its CPU-bound scan; a cancelled
// scan returns the entries filtered so far rather than an error.
//
// # The package name
//
// "handlers" names a layer, not a domain, and only two of these files hold
// anything that is literally a handler. docs/adr/0003-deferred-package-rename.md
// records the decision to rename this package to internal/loki as a pure
// `git mv` once a parallel MCP SDK migration has landed, and why that is
// deferred rather than done here.
package handlers
