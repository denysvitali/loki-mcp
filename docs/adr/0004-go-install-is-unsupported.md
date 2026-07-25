# 4. `go install` is not a supported installation path

- **Status:** Accepted
- **Date:** 2026-07-25

## Context

`go install github.com/kaiko-ai/loki-mcp@latest` fails:

```
$ go install github.com/kaiko-ai/loki-mcp@latest
go: github.com/kaiko-ai/loki-mcp@latest (in github.com/kaiko-ai/loki-mcp@v1.5.0):
	The go.mod file for the module providing named packages contains one or
	more replace directives. It must not contain directives that would cause
	it to be interpreted differently than if it were the main module.
```

`go install pkg@version` refuses any module whose `go.mod` carries a `replace`
directive, and this one does:

```
// Loki v3.7.3 depends on Grafana's memberlist fork for APIs not yet upstreamed.
replace github.com/hashicorp/memberlist => github.com/grafana/memberlist v0.3.1-0.20251126142931-6f9f62ab6f86
```

The chain that makes the `replace` necessary, as `go mod why` reports it:

```
$ go mod why github.com/hashicorp/memberlist
# github.com/hashicorp/memberlist
github.com/kaiko-ai/loki-mcp/internal/handlers
github.com/grafana/loki/v3/pkg/logcli/client
github.com/grafana/loki/v3/pkg/logql
github.com/grafana/loki/v3/pkg/analytics
github.com/grafana/dskit/kv/memberlist
github.com/hashicorp/memberlist
```

`internal/handlers` cannot avoid the Loki packages. `--query-filter` is enforced
by parsing the operator's stream selector and the client's query into LogQL ASTs
and injecting matchers, which needs `logql/syntax`; the response types come from
`loghttp` and the query direction from `logproto`. Each of them drags in most of
the Loki server, and all three reach memberlist:

| Package imported by `internal/handlers` | Packages in its dependency closure | Reaches memberlist |
|---|---|---|
| `github.com/grafana/loki/v3/pkg/logql/syntax` | 879 | yes |
| `github.com/grafana/loki/v3/pkg/loghttp` | 1337 | yes |
| `github.com/grafana/loki/v3/pkg/logproto` | 884 | yes |

(`go list -deps <pkg> | wc -l`, and `| grep memberlist`, on Go 1.26.4.)

Dropping `--query-filter` would remove `logql/syntax`, but `loghttp` and
`logproto` would remain and the closure would not shrink below memberlist. The
LogQL parser is the reason the feature can be enforced at all, so removing it is
not on the table as a packaging fix.

## Decision

Accept that `go install` does not work. Do not remove the `replace`, and do not
contort the dependency graph to get rid of it.

Two alternatives were considered and rejected:

1. **Drop the `replace` and build against upstream `github.com/hashicorp/memberlist`.**
   It does not compile. `github.com/grafana/dskit/kv/memberlist` uses APIs that
   exist only in the fork: `memberlist.NodeState` (upstream declares the type as
   unexported `nodeState`), `Config.NodeSelection` with the
   `NodeSelectionDelegate` interface, and `Config.PushPullNodes`. None of the
   three is present in upstream v0.5.4 or v0.6.0.
2. **Require the fork under its own module path, `github.com/grafana/memberlist`,
   so no `replace` is needed.** Impossible: the fork's `go.mod` declares
   `module github.com/hashicorp/memberlist`. A module can only be required under
   the path it declares, so redirecting the original path — a `replace` — is the
   only mechanism available.

## Consequences

- Distribution is source builds, the goreleaser release binaries and the
  container image. `README.md` and `CLAUDE.md` say so, and point here.

  ```bash
  git clone https://github.com/kaiko-ai/loki-mcp.git
  cd loki-mcp
  go build -o loki-mcp .
  ```

- The failure is a documented constraint, not an open bug. It should not be
  re-reported; a future report should be closed by pointing at this ADR.
- The constraint lifts on its own if Grafana upstreams the fork's APIs and
  `dskit` moves back to `github.com/hashicorp/memberlist`. Nothing in this
  repository has to change for that to take effect beyond deleting the
  `replace` line and re-running `go mod tidy`.
- Alternatively it lifts if `internal/handlers` stops importing the Loki
  packages — which means replacing the LogQL parser that `--query-filter`
  depends on, plus the `loghttp`/`logproto` types, with a local implementation.
  That is a substantial piece of work with its own correctness risk (a
  hand-rolled LogQL parser enforcing a containment control), and it is not
  planned. It is recorded here so the trade is visible if someone revisits it.
