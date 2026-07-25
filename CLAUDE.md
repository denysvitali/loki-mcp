# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build the binary
make build                    # go build -o loki-mcp -v .

# Run all tests
make test                     # go test -v ./...

# Run a single test
go test -v ./internal/handlers -run TestFormatLokiResults_TimestampParsing

# Run the integration tests (build tag `integration`, needs a reachable Loki)
make test-integration         # go test -v -tags=integration ./internal/handlers -timeout 5m

# Run tests with race detection
go test -race ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Format code
make fmt                      # gofmt -w .

# Lint
make lint                     # golangci-lint run ./...

# Tidy dependencies
make tidy                     # go mod tidy
```

Gate before committing: `go build ./...`, `go vet ./...`, `go test ./...`,
`golangci-lint run ./...`, and `gofmt -l .` producing no output.

## Local Development with Docker

```bash
# Start Loki + Grafana + Promtail + a log generator
docker compose up -d

# Insert test logs
./docker/insert-loki-logs.sh --num 20 --job "test-job"

# View Grafana at http://localhost:3000
# Loki API at http://localhost:3100
```

`docker-compose.yml` also defines a `loki-mcp-server` service. It needs no
`command:`: the image is `ENTRYPOINT ["./loki-mcp"]` plus `CMD ["http"]`, so it
serves HTTP on the published `:8080` out of the box. `docker run -i <image>
stdio` overrides the `CMD` for the stdio transport.

Promtail scrapes both `/var/log/*log` (`job=varlogs`) and the compose
containers' stdout/stderr through the Docker socket (`job=docker`), so the
`log-generator` output reaches Loki.

## Architecture

This is an MCP (Model Context Protocol) server that provides Grafana Loki query
tools to AI assistants like Claude Desktop, Cursor, and n8n.

A longer overview — request path, dependency direction, concurrency model — is in
`docs/architecture.md`.

### Commands

`main.go` is 16 lines: it sets the version string and calls `cmd.Execute()`.
Everything else is a cobra subcommand in `cmd/`:

- `loki-mcp stdio` - serve MCP over stdin/stdout (Claude Desktop and similar)
- `loki-mcp http` - serve `/stream` (streamable HTTP) plus `/sse` and `/mcp`
  (legacy SSE, disabled by `--disable-sse`)
- `loki-mcp version` - print the version and exit

**The bare binary serves stdio**: `rootCmd.RunE` is `runStdio`, the same
function `stdioCmd` uses, so `loki-mcp` and `loki-mcp stdio` are one command.
MCP client configs work with `"args": ["stdio"]` and with `"args": []`; prefer
the explicit form in examples so the transport is visible in the config.

`cmd/root.go`'s `PersistentPreRunE` runs before every runnable command: it
initializes logging, initializes OpenTelemetry and builds the immutable
`handlers.Config`. It deliberately does **not** ping Loki — cobra runs it for
`version`, `help` and `completion` too, and those must work offline. The
serving commands (`runStdio` and `http`'s `RunE`) call `pingLoki` from their own
body instead, and a failed ping aborts them.

`rootCmd` sets `SilenceUsage: true` so a runtime failure prints only
`Error: …`. A `SetFlagErrorFunc` hook clears it again when flag parsing fails,
so an unknown flag still gets the usage block.

### Packages

- `cmd/` - cobra commands, flag/env binding (viper), startup wiring. No Loki logic.
- `internal/server/` - MCP server construction (`server.go`) and the two
  transports (`stdio.go`, `http.go`).
- `internal/handlers/` - all Loki logic. Split by concept:
  - `config.go` - `Config`, `NewConfig`, `Handlers`, `New`
  - `tools.go` - the three `mcp.Tool` schemas
  - `params.go` - `LokiParams`, defaults, `resolveTimeRange`, `PingLoki`
  - `query.go` - the `loki_query` handler
  - `labels.go` - the `loki_label_names` / `loki_label_values` handlers
  - `entries.go` - time parsing, entry flattening/sorting, keyword filter, head/tail
  - `format.go` - the `Format` type and the raw/json/text renderers
  - `filter.go` - LogQL matcher injection (the query filter)
  - `secret_filter.go` - betterleaks scan over returned entries
  - `loki_http.go` - direct HTTP calls to Loki's label endpoints
- `internal/telemetry/` - OpenTelemetry providers, instruments, tool middleware.
- `internal/logging/` - the process-wide logrus logger. Always writes to stderr.

### Tools

- **loki_query** - run a LogQL query
- **loki_label_names** - list label names
- **loki_label_values** - list values for a label

Each tool accepts per-call `url`, `username`, `password`, `token` and `org`
arguments; unset ones fall back to the corresponding flag/env value. `org` is sent
as `X-Scope-OrgID`.

### Handler configuration

Handler config is immutable and injected, not global. `cmd/root.go` calls
`handlers.NewConfig(queryFilter, secretFilter)` once at startup and passes the
resulting `handlers.Config` to `server.RunStdio` / `server.RunHTTP`, which pass it
to `handlers.New(cfg)`. `Config` holds the parsed query-filter matchers and the
secret detector (both nil when the feature is off). Nothing writes to it after
construction, so no locks are involved. To add a handler-wide setting, add a field
to `Config` and set it in `NewConfig`.

### Environment Variables

Every persistent flag is also an env var; `--help` prints the mapping. Names are
`LOKI_` + the flag name uppercased with dashes replaced by underscores.

- `LOKI_URL` - Loki server URL (default: `http://localhost:3100`)
- `LOKI_ORG_ID` - default org ID for multi-tenant setups
- `LOKI_USERNAME` / `LOKI_PASSWORD` - basic auth credentials
- `LOKI_TOKEN` - bearer token authentication
- `LOKI_QUERY_FILTER` - LogQL stream selector to restrict all queries
- `LOKI_SECRET_FILTER` - secret filtering (default: true; set false to opt out)
- `LOKI_OTEL_STDERR` - OpenTelemetry console exporters on stderr
- `LOKI_LOG_LEVEL` - debug, info, warn, error (default: info)
- `LOKI_LOG_FORMAT` - text, json (default: text)
- `LOKI_HOST` - HTTP bind address (default: `0.0.0.0`), `http` only
- `LOKI_DISABLE_SSE` - disable `/sse` and `/mcp`, `http` only
- `PORT` - HTTP server port (default: 8080), `http` only. No `LOKI_` prefix.
- Standard `OTEL_*` env vars configure OTLP traces, metrics, and logs

Port precedence is hand-rolled in `cmd/http.go`'s `resolvePort`, highest first:
`--port` when passed on the command line, then a non-empty `PORT`, then `8080`.
**`LOKI_PORT` is not a source.** The `port` flag is deliberately left unbound
from viper, because viper's `AutomaticEnv` would resolve the prefixed
`LOKI_PORT` ahead of `PORT` and the undocumented name would shadow the
documented one.

### CLI Flags

Persistent (all subcommands):

- `-f`, `--query-filter` - LogQL stream selector to restrict all queries
- `--secret-filter` - scan returned lines for secrets (default true; `=false` opts out)
- `--otel-stderr` - emit OpenTelemetry traces, metrics, and logs to stderr
- `-l`, `--log-level` / `--log-format`
- `-u`, `--url`, `--org-id`, `--username`, `--password`, `--token`

`http` only: `--host`, `-p`/`--port`, `--disable-sse`.

### Query Filter

The query filter restricts all Loki queries to logs matching the given LogQL
stream selector, for multi-tenant setups where clients should only reach some
namespaces or jobs.

```bash
./loki-mcp stdio --query-filter='{namespace="prod"}'
LOKI_QUERY_FILTER='{namespace="prod"}' ./loki-mcp stdio
```

The matchers are ANDed into every stream selector in the client's query, walking
the whole LogQL AST. With filter `{namespace="prod"}`, a client query `{job="api"}`
becomes `{namespace="prod", job="api"}`, and `sum(rate({job="api"}[5m]))` becomes
`sum(rate({namespace="prod", job="api"}[5m]))`.

`ParseFilter` accepts **bare stream selectors only** and fails closed on
anything else, at startup:

```
Error: invalid LogQL stream selector "{a=\"b\"} |= \"x\"": only bare stream
selectors are supported, but "|= \"x\"" cannot be enforced and would be
silently discarded
```

`Config.QueryFilter` is a `[]*labels.Matcher`, so a pipeline stage has nowhere
to live; enforcing `{namespace="prod"} |= "error"` as `{namespace="prod"}` alone
would silently weaken a containment control. Metric queries and unparseable
input are rejected the same way.

### Secret Filter

Returned log lines are scanned by betterleaks using its default ruleset. Matching
lines are dropped entirely. Filtering happens before formatting, so raw, text and
json all see sanitized data. The note `N line(s) omitted due to detected secrets`
is appended to the response's `Warnings` exactly once, and every renderer reads
it from there — so it appears in the `Warnings:` block for `raw`/`text` and in
the `warnings` array for `json`, never twice and never in two different
wordings. Enabled by default; disable with `--secret-filter=false` or
`LOKI_SECRET_FILTER=false`.

One `*detect.Detector` is built per process, under a `sync.Once` in
`config.go`. That is deliberate: `(*detect.Detector).Tokenizer()` writes the
process-global `tiktoken.SetBpeLoader` from a per-detector `Once`, so two
detectors scanning concurrently race inside the dependency. Tests that build a
detector must not call `t.Parallel()`.

### OpenTelemetry

Standard `OTEL_*` variables configure the OTLP exporters.
`--otel-stderr` / `LOKI_OTEL_STDERR=true` enables console exporters that write to
stderr, never stdout, so stdio transport stays protocol-safe. Logrus output also
goes to stderr and is bridged into OTel logs when a logger provider is configured.

### Output Formats

The `format` parameter accepts `raw` (default), `json`, or `text`. Anything else is
rejected by `Format.validate` in `internal/handlers/format.go`.

The contract every `loki_query` response honours:

- **Newest first.** `collectAndSortEntries` sorts by descending timestamp,
  matching the `direction=BACKWARD` range query, and ties are broken by line and
  then by label set so the order is a function of the input alone.
  `applyHeadTail` slices that descending list: `head` takes the N oldest (off
  the end), `tail` the N newest (off the front).
- **Stable stream order.** `entriesToStreams` regroups through a map, whose
  iteration order Go randomises, so it sorts the streams by label-set string
  before returning them.
- **Warnings.** `raw` and `text` end with a `Warnings:` block rendered by
  `appendWarnings`, carrying Loki's own warnings as well as this server's;
  `json` carries them in `warnings`. Warnings this server injected are tagged
  with the `loki-mcp: ` prefix (`internalWarningPrefix`).
- **`json` is Loki's wire format.** `format.go`'s `lokiStreamsResponse` DTO
  exists because `loghttp.QueryResponse` defines `UnmarshalJSON` but no
  `MarshalJSON`; the DTO round-trips back through the Loki decoder. `stats` is
  omitted because post-processing invalidates it.

## Module Path

The module is `github.com/kaiko-ai/loki-mcp`.

`go install github.com/kaiko-ai/loki-mcp@latest` does **not** work and is not
expected to — see `docs/adr/0004-go-install-is-unsupported.md`. `go.mod` needs a
`replace` for `github.com/hashicorp/memberlist`, dragged in transitively by the
LogQL parser `--query-filter` depends on, and `go install pkg@version` refuses
modules with `replace` directives. Distribution is source builds, the release
binaries and the container image:

```bash
git clone https://github.com/kaiko-ai/loki-mcp.git && cd loki-mcp && go build -o loki-mcp .
```
