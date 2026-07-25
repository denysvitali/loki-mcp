# Loki MCP Server

[![CI](https://github.com/kaiko-ai/loki-mcp/workflows/CI/badge.svg)](https://github.com/kaiko-ai/loki-mcp/actions/workflows/ci.yml)

An MCP (Model Context Protocol) server that exposes Grafana Loki as three tools —
`loki_query`, `loki_label_names`, `loki_label_values` — to MCP clients such as
Claude Code, Claude Desktop, Cursor and n8n.

## Install

### Build from source

```bash
git clone https://github.com/kaiko-ai/loki-mcp.git
cd loki-mcp
go build -o loki-mcp .        # or: make build
```

Requires Go 1.26.4 or newer (see `go.mod`).

`go install github.com/kaiko-ai/loki-mcp@latest` does **not** work: `go.mod`
carries a `replace` directive (`github.com/hashicorp/memberlist` →
`github.com/grafana/memberlist`), which `go install` refuses. Build from source
or use a release binary instead.

### Release binary

Pre-built binaries for linux/darwin/windows on amd64/arm64 are published by
goreleaser on the [Releases page](https://github.com/kaiko-ai/loki-mcp/releases).

## Running the server

The binary does nothing useful without a subcommand — bare `loki-mcp` prints
help and exits 0.

| Command | What it does |
|---|---|
| `loki-mcp stdio` | Serves MCP over stdin/stdout. All logs go to stderr. Use this for Claude Desktop, Claude Code and Cursor. |
| `loki-mcp http` | Serves MCP over HTTP on `0.0.0.0:8080` by default. |
| `loki-mcp version` | Prints `loki-mcp <version>`. |

`stdio`, `http` and `version` all ping Loki (`GET /loki/api/v1/labels`) before
doing anything and exit 1 if it is unreachable — `version` included, which is a
known wart.

### HTTP endpoints

| Path | Purpose |
|---|---|
| `/stream` | Streamable HTTP — modern MCP clients |
| `/sse` | SSE event stream — legacy |
| `/mcp` | SSE message endpoint — legacy |

`--disable-sse` removes `/sse` and `/mcp`; they then return 404.

## Tools

### `loki_query`

Runs a LogQL query.

| Parameter | Type | Required | Default | Notes |
|---|---|---|---|---|
| `query` | string | yes | — | LogQL query string |
| `url` | string | no | `LOKI_URL` / `--url` | Loki server URL |
| `username` | string | no | `LOKI_USERNAME` | Basic auth username |
| `password` | string | no | `LOKI_PASSWORD` | Basic auth password |
| `token` | string | no | `LOKI_TOKEN` | Bearer token |
| `org` | string | no | `LOKI_ORG_ID` | Sent as `X-Scope-OrgID` |
| `start` | string | no | 1h ago | See time formats below |
| `end` | string | no | now | See time formats below |
| `limit` | number | no | 100 | Max entries returned by Loki |
| `head` | number | no | — | Keep only the first N (oldest) entries. Mutually exclusive with `tail`. |
| `tail` | number | no | — | Keep only the last N (newest) entries. Mutually exclusive with `head`. |
| `filter` | string | no | — | Keep only lines containing this substring |
| `filter_case_sensitive` | boolean | no | `false` | Make `filter` case-sensitive |
| `format` | string | no | `raw` | `raw`, `json` or `text` |

### `loki_label_names`

Returns all label names. Parameters: `url`, `username`, `password`, `token`,
`org`, `start`, `end`, `format` — same meaning and defaults as above.

### `loki_label_values`

Returns all values of one label. Requires `label`. Otherwise the same
parameters as `loki_label_names`.

### Time formats

`start` and `end` accept `now`, a negative Go duration (`-1h`, `-30m`),
RFC3339, `2006-01-02T15:04:05`, `2006-01-02 15:04:05`, or `2006-01-02`.

## Flags and environment variables

Global flags, available on every subcommand. The flag wins over the env var.

| Flag | Env | Default |
|---|---|---|
| `-u`, `--url` | `LOKI_URL` | `http://localhost:3100` |
| `--org-id` | `LOKI_ORG_ID` | — |
| `--username` | `LOKI_USERNAME` | — |
| `--password` | `LOKI_PASSWORD` | — |
| `--token` | `LOKI_TOKEN` | — |
| `-f`, `--query-filter` | `LOKI_QUERY_FILTER` | — |
| `--secret-filter` | `LOKI_SECRET_FILTER` | `true` |
| `--otel-stderr` | `LOKI_OTEL_STDERR` | `false` |
| `-l`, `--log-level` | `LOKI_LOG_LEVEL` | `info` (`debug`, `info`, `warn`, `error`) |
| `--log-format` | `LOKI_LOG_FORMAT` | `text` (`text`, `json`) |

`http`-only flags:

| Flag | Env | Default |
|---|---|---|
| `--host` | `LOKI_HOST` | `0.0.0.0` |
| `-p`, `--port` | `PORT` | `8080` |
| `--disable-sse` | `LOKI_DISABLE_SSE` | `false` |

Credentials passed as flags are visible in the process list; prefer the
environment variables, and prefer `LOKI_TOKEN` over username/password.

## Safety controls

### Secret filter (on by default)

Every returned log line is scanned with the default betterleaks ruleset. A
matching line is dropped whole and the output gains a note such as
`1 line omitted due to detected secrets`. Turn it off only for trusted local
debugging:

```bash
loki-mcp stdio --secret-filter=false
LOKI_SECRET_FILTER=false loki-mcp stdio
```

### Query filter

A LogQL stream selector that is ANDed into every client query, so clients
cannot read outside it. With the filter `{namespace="prod"}`, a client query of
`{job="api"}` is executed as `{namespace="prod", job="api"}`.

```bash
loki-mcp stdio --query-filter='{namespace="prod"}'
LOKI_QUERY_FILTER='{namespace="prod"}' loki-mcp http
```

An unparseable selector is a startup error.

## OpenTelemetry

Traces, metrics and logs are exported via the standard `OTEL_*` environment
variables (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_TRACES_EXPORTER`,
`OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER`, …). For local debugging,
`--otel-stderr` adds console exporters. They write to **stderr** only, so stdio
transport stays protocol-safe.

```bash
loki-mcp stdio --otel-stderr
```

## Docker

The image's entrypoint is the bare binary, so the subcommand is the container
argument:

```bash
docker build -t loki-mcp .

# stdio mode (needs -i so the client can write to stdin)
docker run --rm -i loki-mcp stdio

# HTTP mode
docker run --rm -p 8080:8080 loki-mcp http
```

## Local development stack

`docker-compose.yml` brings up Loki (`:3100`), Grafana (`:3000`, anonymous
admin, Loki pre-provisioned as the default datasource), promtail, a
log-generator container, and a `loki-mcp-server` service publishing `:8080`.

```bash
docker compose up -d
```

Caveats, both visible in the compose file:

- The `loki-mcp-server` service inherits the bare-binary entrypoint with no
  subcommand, so the container prints help and exits. Add `command: ["http"]`
  to the service to make it serve.
- Promtail scrapes `/var/log/*log` from the host under `job=varlogs`. It does
  not collect the `log-generator` container's output.

Push test logs into Loki yourself:

```bash
./docker/insert-loki-logs.sh                                   # 10 logs, job=dummy-logs
./docker/insert-loki-logs.sh --num 20 --job my-job --app my-app
./docker/insert-loki-logs.sh --help
```

Then explore them in Grafana at http://localhost:3000.

## Client configuration

### Claude Code

```bash
claude mcp add loki-mcp -- loki-mcp stdio
```

With environment variables:

```bash
claude mcp add loki-mcp -e LOKI_URL=http://localhost:3100 -e LOKI_ORG_ID=your-org-id -- loki-mcp stdio
```

### Claude Desktop

Config file location:

- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`
- Linux: `~/.config/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "loki-mcp": {
      "command": "loki-mcp",
      "args": ["stdio"],
      "env": {
        "LOKI_URL": "http://localhost:3100",
        "LOKI_ORG_ID": "your-org-id"
      }
    }
  }
}
```

Restart Claude Desktop afterwards.

### Cursor

Same JSON in `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "loki-mcp": {
      "command": "loki-mcp",
      "args": ["stdio"],
      "env": {
        "LOKI_URL": "http://localhost:3100",
        "LOKI_ORG_ID": "your-org-id"
      }
    }
  }
}
```

Restart Cursor afterwards.

`args` must contain `stdio`. With `"args": []` the binary prints help and exits,
and the client sees a server that never speaks MCP.

### n8n

Run the server with `loki-mcp http`, then point the n8n MCP Client Tools node at
the SSE endpoint `http://your-server:8080/sse` and connect it to an AI Agent node.

## Example prompts

- "Query Loki for logs with the query {job=\"varlogs\"}"
- "Find error logs from the last hour using {job=\"varlogs\"} |= \"ERROR\""
- "Show me the 50 most recent logs from job=varlogs"

## Project structure

```
.
├── main.go              # 13 lines: sets the version string, calls cmd.Execute()
├── cmd/                 # cobra commands: root.go, stdio.go, http.go, version.go
├── internal/
│   ├── handlers/        # MCP tool definitions and all Loki access
│   │   ├── tools.go         # the three mcp.Tool schemas
│   │   ├── query.go         # loki_query handler
│   │   ├── labels.go        # loki_label_names / loki_label_values handlers
│   │   ├── params.go        # shared params, defaults, time-range resolution
│   │   ├── entries.go       # time parsing, sorting, keyword filter, head/tail
│   │   ├── format.go        # raw / json / text renderers
│   │   ├── filter.go        # LogQL stream-selector injection
│   │   ├── loki_http.go     # direct Loki HTTP calls
│   │   ├── secret_filter.go # betterleaks scan of returned lines
│   │   └── config.go        # immutable handler config, built once at startup
│   ├── server/          # MCP server construction, stdio and HTTP transports
│   ├── logging/         # logrus setup (stderr)
│   └── telemetry/       # OpenTelemetry providers and tool instrumentation
├── docker/              # grafana/ and promtail/ configs, insert-loki-logs.sh
└── docs/
```

## Development

```bash
go build -o loki-mcp .                    # make build
gofmt -w .                                # make fmt
golangci-lint run ./...                   # make lint
go mod tidy                               # make tidy

go test ./...                             # make test
go test -race ./...
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
```

Integration tests live behind the `integration` build tag and need Docker:

```bash
go test -tags=integration ./internal/handlers -timeout 5m   # make test-integration
```

`make help` lists every target.

## License

This repository contains no `LICENSE` file.
