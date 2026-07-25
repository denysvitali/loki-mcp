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

`docker-compose.yml` also defines a `loki-mcp-server` service, but its image
(`Dockerfile`, `ENTRYPOINT ["./loki-mcp"]`) runs the binary with no subcommand,
which prints help and exits immediately, so that container does not stay up.

## Architecture

This is an MCP (Model Context Protocol) server that provides Grafana Loki query
tools to AI assistants like Claude Desktop, Cursor, and n8n.

A longer overview — request path, dependency direction, concurrency model — is in
`docs/architecture.md`.

### Commands

`main.go` is 13 lines: it sets the version string and calls `cmd.Execute()`.
Everything else is a cobra subcommand in `cmd/`:

- `loki-mcp stdio` - serve MCP over stdin/stdout (Claude Desktop and similar)
- `loki-mcp http` - serve `/stream` (streamable HTTP) plus `/sse` and `/mcp`
  (legacy SSE, disabled by `--disable-sse`)
- `loki-mcp version` - print the version and exit

**The bare binary starts nothing**: with no subcommand it prints help and exits 0.
MCP client configs must pass `["stdio"]` as args.

`cmd/root.go`'s `PersistentPreRunE` runs before every subcommand: it initializes
logging, initializes OpenTelemetry, builds the immutable `handlers.Config`, and
pings Loki. A failed ping aborts the command.

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

### Secret Filter

Returned log lines are scanned by betterleaks using its default ruleset. Matching
lines are dropped entirely. Filtering happens before formatting, so raw, text and
json all see sanitized data. The note `N line(s) omitted due to detected secrets`
is added to the response `Warnings` (visible in `format=json`) and, for `raw` and
`text`, appended to the rendered output. Enabled by default; disable with
`--secret-filter=false` or `LOKI_SECRET_FILTER=false`.

### OpenTelemetry

Standard `OTEL_*` variables configure the OTLP exporters.
`--otel-stderr` / `LOKI_OTEL_STDERR=true` enables console exporters that write to
stderr, never stdout, so stdio transport stays protocol-safe. Logrus output also
goes to stderr and is bridged into OTel logs when a logger provider is configured.

### Output Formats

The `format` parameter accepts `raw` (default), `json`, or `text`. Anything else is
rejected by `Format.validate` in `internal/handlers/format.go`.

## Module Path

The module is `github.com/kaiko-ai/loki-mcp`. Binary installs as `loki-mcp`:

```bash
go install github.com/kaiko-ai/loki-mcp@latest
```
