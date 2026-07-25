# Architecture

`loki-mcp` is a single Go binary exposing three Grafana Loki tools — `loki_query`,
`loki_label_names`, `loki_label_values` — over the Model Context Protocol, via
`github.com/mark3labs/mcp-go`. It is a stateless translator: an MCP tool call in, a
LogQL/label request to Loki out, formatted text back; nothing is stored or cached.
`main.go` only calls `cmd.Execute()`; the subcommand (`stdio`, `http`, `version`)
picks the transport, and `stdio`/`http` serve until interrupted.

## Request path

1. MCP client → `loki-mcp stdio` (stdin/stdout) or `http` (`/stream`; `/sse`+`/mcp` unless `--disable-sse`).
2. `internal/server.New` builds the `mcp-go` server, registers the three tools from `handlers.New(cfg)`.
3. `telemetry.ToolHandlerMiddleware` wraps every call in a span and metrics.
4. Handler binds arguments into `LokiParams`; `applyDefaults` fills blanks from viper flags/env.
5. **Query filter**: `applyQueryFilter` ANDs `cfg.QueryFilter` into every stream selector of the client's LogQL AST — before the request leaves the process.
6. `resolveTimeRange` picks the window; the request goes out via the logcli client (`query_range`) or `LokiHTTPClient` (label endpoints).
7. **Secret filter**: `applyPostProcessing` runs on the response — keyword filter, betterleaks scan (matching lines dropped), then head/tail.
8. `formatLokiResults` renders `raw`/`json`/`text` and returns a text tool result.

## Directory map

| Package | Belongs here | Does not |
|---|---|---|
| `main.go` | Set the ldflags version, call `cmd.Execute()` | Anything else |
| `cmd/` | Cobra commands, flag/env binding via viper, startup wiring | Loki or MCP logic |
| `internal/server/` | MCP server construction, stdio and HTTP transports, graceful shutdown | Tool implementations |
| `internal/handlers/` | All Loki logic: tool schemas, params, query/label handlers, LogQL filter injection, secret scan, formatting, Loki HTTP | Transport or flag parsing |
| `internal/telemetry/` | OTel providers, instruments, tool middleware, logrus→OTel bridge | Business logic |
| `internal/logging/` | The process-wide logrus logger, stderr only | Everything else |

## Dependency direction

Rule: a package may import only packages to its right in

```
main → cmd → internal/server → internal/handlers → internal/telemetry → internal/logging
```

Actual edges (`go list -f '{{.ImportPath}} {{.Imports}}' ./...`): `main→cmd`;
`cmd→{server, handlers, logging, telemetry}`; `server→{handlers, logging,
telemetry}`; `handlers→telemetry`; `telemetry→logging`; `logging→` nothing internal.
Every edge points right, so the graph is acyclic. Skipping levels is allowed; going
left is not.

## Concurrency model

- **Handler config is immutable.** `handlers.Config` is built once by
  `handlers.NewConfig` in `cmd/root.go`'s `PersistentPreRunE`, copied into
  `Handlers`, and never written again. Concurrent tool calls share it read-only, so
  no handler needs a lock.
- **Telemetry instruments** live in `atomic.Pointer[instruments]`
  (`telemetry.currentInstruments`), replaced as one whole snapshot during
  `telemetry.Initialize` at startup and only `Load`ed afterwards on the hot path.
- **`telemetry.shutdowns`** is the only mutex in the non-test tree
  (`shutdownMu sync.Mutex`). Both critical sections are trivial: `Shutdown` copies
  the slice out and calls the functions after unlocking.
- **Goroutines:** exactly one explicit `go` statement in non-test code —
  `internal/server/http.go`, which runs `ListenAndServe` while `RunHTTP` waits on a
  signal or the error channel. Stdio mode spawns none.
- **Cancellation:** `filterSecretEntries` polls `ctx.Done()` during its CPU-bound
  scan; a cancelled scan returns the entries filtered so far.
