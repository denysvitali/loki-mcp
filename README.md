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

`go install github.com/kaiko-ai/loki-mcp@latest` does **not** work, and this is
an accepted constraint rather than an open bug — see
[ADR 0004](docs/adr/0004-go-install-is-unsupported.md). In short: `--query-filter`
is enforced by parsing LogQL with `github.com/grafana/loki/v3/pkg/logql/syntax`,
that package transitively pulls in `github.com/grafana/dskit/kv/memberlist`,
which needs Grafana's memberlist fork, which is only reachable through a
`replace` directive — and `go install pkg@version` refuses any module whose
`go.mod` contains one. Clone and `go build`, or take a release binary or the
container image.

### Release binary

Pre-built binaries for linux/darwin/windows on amd64/arm64 are published by
goreleaser on the [Releases page](https://github.com/kaiko-ai/loki-mcp/releases).

## Running the server

Bare `loki-mcp` with no subcommand **serves stdio**: it is the same command as
`loki-mcp stdio`, not a help dump. An MCP client config that passes no
arguments therefore gets a working server.

| Command | What it does |
|---|---|
| `loki-mcp` | Same as `loki-mcp stdio`. |
| `loki-mcp stdio` | Serves MCP over stdin/stdout. All logs go to stderr. Use this for Claude Desktop, Claude Code and Cursor. |
| `loki-mcp http` | Serves MCP over HTTP on `0.0.0.0:8080` by default. |
| `loki-mcp version` | Prints `loki-mcp <version>`. |

Only the commands that serve traffic — the bare invocation, `stdio` and `http` —
ping Loki (`GET /loki/api/v1/labels`) at startup and exit 1 if it is
unreachable. `version`, `help`, `completion` and `--help` never touch the
network and exit 0 with Loki down.

A runtime failure (unreachable Loki, port already in use) prints just
`Error: …`. The usage block is reserved for command-line mistakes such as an
unknown flag or subcommand.

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
| `head` | number | no | — | Keep only the N **oldest** entries. Mutually exclusive with `tail`. |
| `tail` | number | no | — | Keep only the N **newest** entries. Mutually exclusive with `head`. |
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

### Output contract

These hold for `loki_query` in every format, whether or not the secret filter,
the keyword `filter` or `head`/`tail` removed anything:

- **Newest first.** Entries are sorted by descending timestamp — the same
  order Loki's `direction=BACKWARD` range query returns — and post-processing
  preserves it. `head` still selects the N *oldest* entries and `tail` the N
  *newest*; both then render newest-first.
- **Stable stream order.** Entries are regrouped into streams sorted by their
  label-set string, so two identical queries over identical data emit the
  streams in the same order. Entries sharing a timestamp are tie-broken by line
  and then by label set, so nothing is left to map iteration order.
- **`Warnings:` block.** `raw` and `text` end with a trailing

  ```
  Warnings:
    - <one per line>
  ```

  block whenever the response carries warnings, including those Loki itself
  returned. It is omitted when there are none.
- **`loki-mcp: ` prefix.** A warning this server produced rather than passed
  through from Loki is tagged with `loki-mcp: `. The secret-filter note is the
  only one today, and it is reported exactly once, as a warning, in all three
  formats: `loki-mcp: 1 line omitted due to detected secrets`.
- **`format: json` is Loki's own HTTP API shape.** A streams result is emitted
  as `{"status":…,"warnings":[…],"data":{"resultType":"streams","result":[{"stream":{…},"values":[["<unix-nanos>","<line>"]]}]}}`,
  so a Loki-aware client can parse it back. `warnings` is omitted when empty,
  and `stats` is omitted always: post-processing invalidates it and it is not
  recomputed. Metric queries (matrix/vector/scalar) are the exception — they
  are marshalled as-is and do not round-trip.

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

The port is the one setting that does not follow the `LOKI_`-prefix rule.
Precedence, highest first:

1. `--port`, but only when it is actually passed on the command line;
2. `PORT`, when set to a non-empty value;
3. `8080`.

**`LOKI_PORT` is not consulted at all.** It is not a supported name; setting it
has no effect.

Credentials passed as flags are visible in the process list; prefer the
environment variables, and prefer `LOKI_TOKEN` over username/password.

## Safety controls

### Secret filter (on by default)

Every returned log line is scanned with the default betterleaks ruleset. A
matching line is dropped whole, and the response gains exactly one warning
saying so — `loki-mcp: 1 line omitted due to detected secrets` — in the
`Warnings:` block for `raw` and `text` and in the `warnings` array for `json`.
Turn it off only for trusted local debugging:

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

**Bare stream selectors only.** A value carrying a pipeline is rejected at
startup, naming the stage that could not be enforced:

```
$ loki-mcp --query-filter='{a="b"} |= "x"'
Error: invalid LogQL stream selector "{a=\"b\"} |= \"x\"": only bare stream
selectors are supported, but "|= \"x\"" cannot be enforced and would be
silently discarded
```

The reason is that the filter is stored as a matcher list and injected into the
client's stream selectors; there is nowhere for a line filter, a parser stage or
a label-format stage to go. Accepting `{namespace="prod"} |= "error"` would
therefore enforce only `namespace="prod"` — a silent weakening of a containment
control — so it fails closed instead. An unparseable selector and a metric
query (`rate({job="api"}[5m])`) are startup errors for the same reason.

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

The image's entrypoint is the bare binary and its `CMD` is `["http"]`, so it
serves HTTP with no arguments. Any container argument replaces the `CMD` and
becomes the subcommand:

```bash
docker build -t loki-mcp .

# HTTP mode — the default
docker run --rm -p 8080:8080 loki-mcp

# stdio mode (needs -i so the client can write to stdin)
docker run --rm -i loki-mcp stdio
```

`docker run --rm -p 8080:8080 loki-mcp http` is the same thing spelled out.

## Local development stack

`docker-compose.yml` brings up Loki (`:3100`), Grafana (`:3000`, anonymous
admin, Loki pre-provisioned as the default datasource), promtail, a
log-generator container, and a `loki-mcp-server` service publishing `:8080`.

```bash
docker compose up -d
```

The `loki-mcp-server` service needs no `command:`: the image's `CMD ["http"]`
makes it serve HTTP on the published `:8080`.

Promtail has two scrape jobs: `job=varlogs` over the host's `/var/log/*log`,
and `job=docker` over the compose containers' stdout/stderr through the mounted
Docker socket — which is what carries the `log-generator` output into Loki.

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

`"args": ["stdio"]` and `"args": []` both work — a bare invocation is the stdio
command. The examples spell `stdio` out because it is self-documenting: a reader
of the config can see which transport is in use without knowing the default.

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
├── main.go              # 16 lines: sets the version string, calls cmd.Execute()
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

MIT — see [`LICENSE`](LICENSE).
