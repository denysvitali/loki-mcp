# Refactor audit — loki-mcp

> **Historical record.** This document describes the tree as it stood at commit
> `7441901` and the plan drawn up from it. It is not kept current. The bugs it
> lists were fixed later on the `refactor/complexity-reduction` branch, and the
> "BREAKING: none" at the end holds for the refactor itself but not for those
> fixes — see [`refactor-report.md`](refactor-report.md) for what shipped and
> what changed observably.

Baseline commit: `7441901` (main). All numbers measured on a clean checkout of that
commit with Go 1.26.4.

## 1. Metrics baseline

| Metric | Value |
|---|---|
| Go LOC (total / non-test) | 3982 / 1937 |
| Go files (non-test) | 15 |
| Longest files | `internal/handlers/loki_integration_test.go` 996, `internal/handlers/loki.go` 769, `internal/handlers/loki_test.go` 578, `internal/telemetry/telemetry.go` 271 |
| Functions over 60 lines | `HandleLokiQuery` (109), `formatLokiResults` (82), `telemetry.Initialize` (92), `injectMatchers` (112), `RunHTTP` (73), `NewLokiQueryTool` (52 — flat data, acceptable) |
| gocognit > 15 | `formatLokiResults` 33, `HandleLokiQuery` 32, `injectMatchers` 31, `telemetry.Initialize` 28 |
| gocyclo > 12 | `HandleLokiQuery` 25, `injectMatchers` 21, `telemetry.Initialize` 20, `formatLokiResults` 17 |
| Nesting depth > 3 | `HandleLokiQuery` (4), `formatLokiResults` (4) |
| `dupl -threshold 60` | 5 clone groups (2 production: `filter.go:88-102` vs `104-118`; rest in tests) |
| Locks | 3 `sync.RWMutex` (`filterMu`, `secretFilterMu`, `instrumentMu`), 1 `sync.Mutex` (`shutdownMu`) |
| Bare `go` statements | 1 (`internal/server/http.go:73`, no owner, no exit guarantee) |
| `go vet` | clean |
| golangci-lint (current config) | clean — but the config only enables 6 linters |
| `go test ./...` | pass |
| `go test -race ./...` | pass |
| Coverage | 29.9% total; `cmd` 0%, `internal/server` 0%, `internal/telemetry` 0%, `internal/logging` 0% |

## 2. Findings

Severity: H = fix now, M = fix in this pass, L = note only.

### Complexity

| Path | Issue | Sev | Action | Risk |
|---|---|---|---|---|
| `internal/handlers/loki.go:154` | `HandleLokiQuery`: 109 lines, cyclo 25, cognit 32, nesting 4. Mixes arg binding, filter merge, time defaults, query, post-processing, formatting | H | Split into `resolveTimeRange`, `postProcessEntries`, thin handler | Low — covered by new characterization tests |
| `internal/handlers/loki.go:418` | `formatLokiResults`: cognit 33; three format branches × three empty/non-stream pre-checks | H | One function per format + `Format` named type with validation | Low |
| `internal/handlers/loki.go` | 769 lines, 10 unrelated concepts in one file | H | Split into `params.go`, `tools.go`, `query.go`, `labels.go`, `entries.go`, `format.go` (`git mv`-style move commit, no content edits) | Low |
| `internal/telemetry/telemetry.go:51` | `Initialize`: 92 lines, cognit 28, three near-identical exporter blocks | H | Extract `traceProvider`/`meterProvider`/`loggerProvider` helpers | Low |
| `internal/handlers/filter.go:57` | `injectMatchers`: 112 lines, cyclo 21 — but it is a flat type switch over the LogQL AST, one case per node type | M | Keep the switch, extract each case body; do not "abstract" it | Low |
| `internal/server/http.go:36` | `RunHTTP`: 73 lines mixing routing, listening, signals, shutdown | M | `newMux` + `serve(ctx)` + thin `RunHTTP` | Low |
| `cmd/root.go:62` | `PersistentPreRunE`: 45 lines doing 5 unrelated setup steps | M | One helper per step | Low |

### Duplication (true, must extract)

| Path | Issue | Sev | Action |
|---|---|---|---|
| `loki.go:181-195`, `:588-602`, `:642-656` | Identical 15-line start/end time resolution in all three handlers — same rule, must change together | H | Extract `resolveTimeRange(params) (start, end time.Time, err error)` |
| `loki.go:496`, `:722`, `:766` | Identical `unsupported format: %s. Supported formats: raw, json, text` triple | H | Single `Format` type + one error |
| `loki.go:576-627` vs `:630-681` | Label-names and label-values handlers differ only in the call and the formatter | H | Extract shared `runLabelRequest` |
| `loki_http.go:39-78` vs `:81-120` | Two label endpoints differ only in URL path and one param | H | Extract `getLabelResponse(path, query, start, end)` |
| `filter.go:88-102` vs `:104-118` | `dupl` hit: `VectorAggregationExpr` / `LogRangeExpr` cases | L | **Coincidental** — different AST node types with different fields; leave, annotate |
| `loki_test.go:375-405`, `:421-451` | Near-identical test functions | M | Table-driven subtests |

### Footguns

| Path | Issue | Sev | Action |
|---|---|---|---|
| `internal/handlers/config.go`, `secret_filter.go` | Mutable package-global config behind `RWMutex`, plus exported `Reset*` functions that exist only for tests. Handler behaviour depends on hidden global state; tests must remember to reset | H | Keep the globals **or** inject — see §4. Chosen: inject config into the handler set, delete `Reset*` |
| `internal/handlers/loki.go:48` | `LokiParams` carries every tool's params in one struct (14 fields); `Label` is meaningless for `loki_query`, `Query` for label tools | M | Document the shared-vs-per-tool split; do not silently change the wire schema |
| `internal/handlers/loki.go:57-61` | `Limit`/`Head`/`Tail` are `float64` and silently truncate via `int(...)`; `2.7` becomes `2` | M | Keep the wire type (JSON), centralise the conversion in one documented place, record the truncation as intended |
| `internal/handlers/loki.go:162` | Head/Tail mutual exclusion is a runtime error, not a type | L | Leave — MCP arguments are JSON; a runtime check is the only option |
| `internal/server/http.go:81` | `err != http.ErrServerClosed` — direct comparison instead of `errors.Is` | M | `errors.Is` |
| `internal/server/http.go:68` | `signal.Notify` without `signal.Stop` | M | `signal.NotifyContext` + `defer stop()` |
| `internal/server/http.go:73` | Goroutine with no documented owner and no guaranteed exit | H | See §4 |
| `internal/telemetry/telemetry.go:219-238` | `meter.Int64Counter` errors discarded with `_` four times | M | Join and return, or log — currently a silent failure path |
| `internal/handlers/loki_http.go:54,96` | `http.NewRequest` without context (`noctx`) | M | `http.NewRequestWithContext` — requires threading `ctx`, which the callers already have |
| `internal/logging/logger.go:12` | `init()` with side effects building a mutable global logger | M | Keep the global (the whole codebase logs through it) but delete the 9 unused wrappers |
| `internal/handlers/loki.go:33` | `SSEEvent` type is never constructed | H | Delete |
| `internal/logging` | `Logger`, `Debug`, `Debugf`, `Warnf`, `Error`, `Fatal`, `Fatalf`, `WithField`, `WithFields` — zero call sites | H | Delete |
| `internal/server/http.go:25` | `DefaultHTTPConfig` — zero call sites (flags supply the defaults) | H | Delete |

### Concurrency inventory

| Item | Disposition |
|---|---|
| `filterMu` + `filterConfig` (`config.go`) | **Remove.** Parse once at startup, pass the parsed matchers into the handler set. No sharing, no lock. |
| `secretFilterMu` + `secretFilterConfig` (`secret_filter.go`) | **Remove.** Same: build the detector once, own it in the handler set. |
| `instrumentMu` + 4 instrument globals (`telemetry.go`) | **Convert to `atomic.Pointer[instruments]`** — read-mostly snapshot, written once at init, read on every tool call (§7.4). |
| `shutdownMu` + `shutdowns` (`telemetry.go`) | **Keep with justification.** Short critical section, no I/O under the lock (functions are copied out and called after unlock), and the process has exactly one telemetry stack. Documented on the variable. |
| `go func()` in `RunHTTP` | **Convert to owned goroutine**: `signal.NotifyContext` + `errgroup`, one documented exit path per goroutine. |
| `filterSecretEntries` `ctx.Done()` poll | Keep — cooperative cancellation on a CPU-bound scan; document that a cancelled scan returns the entries filtered so far. |

### Doc drift (every item verified false against the code)

| Doc | Statement | Reality |
|---|---|---|
| `README.md:31-39` | Project structure shows `cmd/loki-mcp/` and only `internal/handlers/` | `cmd/` holds cobra commands; `internal/server`, `internal/logging`, `internal/telemetry` are missing |
| `README.md:204` | "**Server**: The main MCP server implementation in `main.go`" | `main.go` is 13 lines; the server lives in `internal/server` |
| `README.md:213-236, 247-260, 277-290` | Claude Code / Claude Desktop / Cursor configs use `"args": []` | Running the binary with no subcommand prints help and exits 0 — no MCP server starts. Must be `["stdio"]` |
| `README.md:110-113` | `docker run --rm -i loki-mcp` "runs the server" | Same: prints help and exits |
| `README.md:27` | "communicates using stdin/stdout and SSE" | True, but only via the `stdio`/`http` subcommands, which the README never mentions |
| `README.md:162` | "default port … configured using the `PORT` environment variable" | True (`cmd/http.go:47`), but `--port`/`--host`/`--disable-sse` are undocumented |
| `CLAUDE.md:19-24` | "`main.go` — Creates an MCP server … Serves via stdio … HTTP … Streamable" | `main.go` only sets a version string and calls `cmd.Execute()` |
| `CLAUDE.md:27-31` | "`internal/handlers/loki.go` — All Loki API integration" | Loki access is spread over `loki.go`, `loki_http.go`, `filter.go`, `secret_filter.go` |
| `CLAUDE.md` | Documents env vars and flags but never the `stdio`/`http`/`version` subcommands | Same gap as the README |
| `README.md`, `CLAUDE.md` | No mention that `--log-level`, `--log-format`, `--host`, `--port`, `--disable-sse` exist | Undocumented flags |

### Bugs found (reported, **not** fixed in this refactor)

1. Bare `loki-mcp` (no subcommand) prints help and exits 0. `Dockerfile`'s `ENTRYPOINT ["./loki-mcp"]` plus `docker-compose.yml`'s `loki-mcp-server` service therefore start a container that exits immediately, even though the compose file publishes port 8080.
2. `loki-mcp version` fails when Loki is unreachable: `PersistentPreRunE` pings Loki before every runnable subcommand, including `version`.
3. `--secret-filter=false` disables filtering, but `HandleLokiQuery` still enters the post-processing branch when a keyword filter or head/tail is set; the resulting `Warnings` note is only appended for non-JSON formats. Cosmetic inconsistency, documented, not changed.

## 3. Proposed target tree (path diff)

```
  cmd/{root,http,stdio,version}.go          unchanged
  internal/handlers/
-   loki.go                    769 lines, 10 concepts
+   params.go                  LokiParams, defaults, time-range resolution
+   tools.go                   the three mcp.Tool definitions
+   query.go                   loki_query handler
+   labels.go                  loki_label_names / loki_label_values handlers
+   entries.go                 logEntry, sort, keyword filter, head/tail, regroup
+   format.go                  Format type + the three renderers
+   doc.go                     package doc: what belongs here, what does not
    filter.go                  unchanged (LogQL matcher injection)
    loki_http.go               deduplicated
-   config.go                  global filter config (deleted, injected instead)
-   secret_filter.go           split: secrets.go keeps the scan, config moves
  internal/server/
    server.go, http.go, stdio.go
  internal/telemetry/
    telemetry.go               split: provider setup vs instruments
+ docs/architecture.md
+ docs/adr/0001-injected-handler-config.md
+ docs/adr/0002-telemetry-shutdown-registry-keeps-a-mutex.md
+ a bug-report document, so bug reports stay out of the refactor commits
```

Renaming `internal/handlers` → `internal/loki` (a domain name rather than a layer
name, per §4.2) is **deferred**: a parallel branch is mid-migration to a different
MCP SDK in that exact package, and a package rename would make that merge
gratuitously painful. Recorded here so the next pass can do it as a pure move.

## 4. Ordered plan

Each step is one commit and is independently green (`go build`, `go vet`,
`go test ./...`, `go test -race ./...`, `golangci-lint run`).

1. `test:` characterization tests for `parseTime`, `applyDefaults`, format
   dispatch, `LokiHTTPClient` (httptest), and `HandleLokiQuery` end-to-end
   against an `httptest` Loki. Pins current behaviour, including the odd cases.
2. `chore:` delete dead code: `SSEEvent`, `DefaultHTTPConfig`, 9 unused
   `logging` wrappers.
3. `refactor:` split `internal/handlers/loki.go` into the files above — moves only.
4. `refactor:` complexity: `Format` type, `resolveTimeRange`, per-format
   renderers, `postProcessEntries`, `runLabelRequest`.
5. `refactor:` duplication: `loki_http.go` shared request path, table-driven tests.
6. `refactor:` concurrency, one type per commit: injected handler config (removes
   `filterMu` + `secretFilterMu`), `atomic.Pointer` instruments, owned goroutines
   in `RunHTTP`.
7. `docs:` truthful README/CLAUDE.md, `doc.go`, ADRs, the bug reports, runnable
   `Example` tests.
8. `ci:` tighten `.golangci.yml`.

**BREAKING:** none. Every changed identifier is under `internal/`, and the MCP
tool schemas (`loki_query`, `loki_label_names`, `loki_label_values`), their
parameter names, defaults, output formats, flags, and environment variables are
unchanged.
