# Refactor report — loki-mcp

Baseline: `7441901` (`main`). Final: this branch, `refactor/complexity-reduction`.
48 non-merge commits across 13 merges.

The branch has two halves. The first 28 non-merge commits, through `9b4bf01`,
are the refactor proper: behaviour-preserving, and tested to be so (§2). The 20
after it fix fourteen of the fifteen bugs the refactor found and re-true the
docs, and those fixes **do** change observable behaviour — read §4 before
upgrading.

Everything below was measured, not remembered. The baseline column comes from a
`git worktree add /tmp/baseline 7441901` checkout; the final column from this
worktree. Both were measured with the same tools on the same machine: Go
1.26.4, linux/amd64, `gocognit`, `gocyclo`, `dupl` and `golangci-lint` 2.11.4
from `/home/workspace/go/bin`.

## 1. Metrics

### Size

| Metric | Baseline `7441901` | Final | Δ |
|---|---|---|---|
| Go LOC, production | 2045 | 3225 | +1180 (+58%) |
| Go LOC, test | 1937 | 6602 | +4665 (+241%) |
| Go LOC, total | 3982 | 9827 | +5845 |
| Go files, production | 15 | 23 | +8 |
| Go files, test | 4 | 21 | +17 |
| Longest file, any | `internal/handlers/loki_integration_test.go` 996 | `internal/handlers/loki_integration_test.go` 1020 | +24 |
| Longest production file | `internal/handlers/loki.go` 769 | `internal/telemetry/telemetry.go` 477 | −292 |
| 2nd/3rd longest production file | `telemetry.go` 271, `filter.go` 198 | `format.go` 377, `filter.go` 301 | — |

Production LOC grew because doc comments grew: every exported identifier now
carries its guarantees, and every multi-file package has a `doc.go`. The unit
of work — the longest file a reader has to hold in their head — shrank by 38%.
`format.go` and `filter.go` gained the most after the refactor proper: the
Loki wire-JSON DTO and the warnings plumbing in one, the fail-closed selector
validation in the other.

### Complexity

Measured with the standalone tools over the whole tree, then split by
`_test.go`. Production figures are the ones that matter; test figures are
included because the linter config excludes tests from these checks and the
reader deserves to know what is behind that exclusion.

| Metric (production only) | Baseline | Final |
|---|---|---|
| Max cognitive complexity | 33 (`formatLokiResults`) | 16 (`ToolHandlerMiddleware`) |
| Functions with gocognit > 15 | 4 | 1 |
| Functions with gocognit > 10 | 7 | 1 |
| Max cyclomatic complexity | 25 (`HandleLokiQuery`) | 9 (`injectMatchers`) |
| Functions with gocyclo > 12 | 4 | 0 |
| Functions with gocyclo > 8 | 8 | 1 |
| Functions over 60 lines (funlen) | 5 | 0 |
| Functions over 40 statements (funlen) | 4 | 0 |
| `nestif` blocks over complexity 4 | 1 | 0 |

Baseline offenders, all gone: `formatLokiResults` (cognit 33, cyclo 17, 72
lines), `HandleLokiQuery` (32 / 25 / 90), `injectMatchers` (31 / 21 / 102),
`telemetry.Initialize` (28 / 20 / 90), `RunHTTP` (63 lines).

The one remaining production function over the cognitive budget is
`telemetry.ToolHandlerMiddleware` at 16. Its body is straight-line; the score
is the nesting penalty from the two closures the
`mcpserver.ToolHandlerMiddleware` signature requires. It carries a
`//nolint:gocognit` saying exactly that.

Test complexity is higher than production and deliberately so — the peaks are
table-driven cases (`TestParseTime` cognit 30, `TestHandleLokiLabelNames` 24).
14 test functions exceed 60 lines.

### Duplication

`dupl -threshold 60` over the whole tree:

| | Baseline | Final |
|---|---|---|
| Clone groups, total | 5 | 4 |
| Clone groups, production | 1 | 1 |
| Clone groups, tests | 4 | 3 |

`golangci-lint dupl` at the configured threshold 100, production only: **0 in
both**. The single production clone at threshold 60 is the same one in both
trees — `filter.go`'s `VectorAggregationExpr` and `LogRangeExpr` cases, which
the audit already classified as coincidental (different AST node types, different
fields). It is annotated in place rather than "abstracted".

The real duplication the audit named is gone and does not show in these counts
because it was below `dupl`'s token threshold: three copies of the 15-line
start/end resolution (now `resolveTimeRange`), three copies of the
`unsupported format` error (now one `Format` type), two near-identical label
handlers (now `runLabelRequest`), two near-identical label HTTP paths (now
`getLabelResponse`), and the thrice-repeated env-name derivation in the flag
helpers (now `envUsage`, with `--disable-sse` no longer spelling its env var out
by hand where it could drift).

### Concurrency

| | Baseline | Final |
|---|---|---|
| `sync.Mutex` / `sync.RWMutex`, production | 4 | 1 |
| `sync.Mutex` / `sync.RWMutex`, tests | 0 | 2 |
| `go` statements, production | 1 | 1 |
| — of those, with a documented owner and guaranteed join | 0 | 1 |
| Exported `Reset*`-for-tests functions | 2 | 0 |

Removed: `filterMu` and `secretFilterMu` (the config they guarded is now built
once and injected, ADR 0001) and `instrumentMu` (now `atomic.Pointer[instruments]`).
Kept: `telemetry.shutdownMu`, with an ADR (0002) and a `//nolint:forbidigo`
naming the field it guards. The two test mutexes are output collectors in
`cmd/main_test.go`.

The one goroutine is still one goroutine, but `serve` now owns it: it is joined
through a deferred receive on `listenDone` on every path, and the doc comment
enumerates why it is guaranteed to exit.

### Lint

`golangci-lint` 2.11.4. "Old config" = the 6-linter config at `7441901`; "new
config" = the 26-linter config committed here.

| | Old config | New config |
|---|---|---|
| Baseline `7441901` | 0 issues | **26 issues** |
| Final | 0 issues | **0 issues** |

The 26 baseline issues under the new config: revive 9, forbidigo 4, gocognit 4,
thelper 3, noctx 2, errcheck 1, funlen 1, gocritic 1, nestif 1. (`golangci-lint`
reports at most one issue per line, so funlen shows 1 rather than 5 — the other
four share a line with a gocognit report. Measured in isolation, baseline
production funlen is 5 and final is 0.)

### Coverage

`go test -coverprofile` + `go tool cover -func`.

| Package | Baseline | Final |
|---|---|---|
| `github.com/kaiko-ai/loki-mcp` (main) | 0.0% | 0.0% |
| `cmd` | 0.0% | 82.1% |
| `internal/handlers` | 44.7% | 90.5% |
| `internal/logging` | 0.0% | 100.0% |
| `internal/server` | 0.0% | 86.8% |
| `internal/telemetry` | 0.0% | 79.9% |
| **Total** | **29.9%** | **88.1%** |

`main` stays at 0%: `main.go` is a version string and a call to `cmd.Execute()`,
and `cmd`'s own `TestMain`-level behaviour is covered in `cmd/main_test.go`.

## 2. What changed and why

### Correctness preserved (the refactor commits)

Nothing in the refactor half of this branch — everything up to and including
`9b4bf01` — was allowed to change behaviour, and the claim was tested rather
than asserted. The bug fixes that came afterwards deliberately do change
behaviour; §4 lists exactly how.

- Characterization tests came **first**, before any refactor commit:
  `1224b82` (handlers), `c1d09d9` (telemetry, server), `1c9e424` (cmd),
  `72f6dcb` (logging). They pin the odd cases too — `float64` limits truncating,
  head/tail mutual exclusion, blank filter meaning "unrestricted".
- Tool schemas are golden files (`internal/handlers/testdata/*.tool.json`)
  compared on every run by `tools_test.go`.
- Live differential test: baseline and final binaries were driven over stdio
  against the same fake Loki, for `loki_query` / `loki_label_names` /
  `loki_label_values` × `raw` / `json` / `text`, plus `head`, `filter`, an
  invalid format, missing required arguments, and the head+tail conflict.
  Byte-identical in every case. See §4.3.
- Coverage rose from 29.9% to 88.1%, so the pinning is real and not
  concentrated in one package.

### Footguns removed

- **Mutable package-global handler config** (`d67e57d`, ADR 0001). `Config` is
  built once by `handlers.NewConfig` and injected; handlers became methods on
  `*Handlers`. This deleted two locks and three exported `Reset*` functions that
  existed only so tests could undo each other.
- **Telemetry instruments** moved from `RWMutex`-guarded globals to
  `atomic.Pointer[instruments]` (`633f7dc`): a whole snapshot swapped at init,
  `Load`ed on the hot path.
- **Unowned goroutine** in `RunHTTP` (`35379ce`): `signal.NotifyContext` with
  `defer stop()`, `errors.Is(err, http.ErrServerClosed)` instead of `!=`, and a
  join the function cannot skip.
- **Dead code** deleted (`43cdb8c`, `81da3b2`): `SSEEvent` (never constructed),
  `DefaultHTTPConfig` (zero call sites), nine unused `logging` wrappers.
- The ban is now mechanical: `forbidigo` fails the build on a new
  `sync.Mutex`/`RWMutex` unless the author writes down what it guards
  (`eda5060`).

### Complexity reduced

`cb14812` split the 769-line `internal/handlers/loki.go` into
`params.go` / `tools.go` / `query.go` / `labels.go` / `entries.go` /
`format.go` as a pure move, so the content diff is reviewable separately in
`c3ee111`, which introduced the `Format` named type, `resolveTimeRange`,
per-format renderers and `postProcessEntries`. `499e22c` split
`telemetry.Initialize` into one helper per signal. `35379ce` split `RunHTTP`
into `newMux` + `serve` + a thin `RunHTTP`. Result: no production function over
60 lines, none over cyclomatic 12, one over cognitive 15.

The budget is now enforced: `gocognit 15`, `gocyclo 12`, `funlen 60/40`,
`nestif 4` in `.golangci.yml` (`eda5060`).

### Truthful docs

Every doc-drift item in the audit was verified false against the code and then
fixed: `695fb54` (CLAUDE.md), `b2db277` (README.md — the `"args": []` client
configs that start no server, the undocumented `stdio`/`http`/`version`
subcommands, the undocumented `--host`/`--port`/`--disable-sse`/`--log-*`
flags). `851a245` added `docs/architecture.md` with the request path, the
directory map and the dependency direction. `6091dcf` gave every multi-file
package a `doc.go`; `95b3417` documented every exported identifier with its
guarantees; `1565432` added runnable `Example` tests so the doc claims execute
in CI.

`eda5060` turns the architecture doc's dependency direction into a `depguard`
rule, so the diagram cannot silently go stale.

### Duplication eliminated

`c3ee111` (time range, format error), `bde2eff` (shared `runLabelRequest` and
`getLabelResponse`), `af4c81c` (`envUsage` shared by `envFlag`, `envBoolFlag`
and the hand-rolled `--disable-sse` registration). See §1 for what is left and
why it is not duplication.

### Intuitive structure

One concept per file in `internal/handlers`, a `doc.go` per package saying what
belongs there and what does not, four ADRs recording the decisions that would
otherwise look arbitrary (0004 was added with the bug-fix half), and bug reports
written up separately so they stay out of the refactor commits.

## 3. Deferred, with reasons

1. **`internal/handlers` → `internal/loki` rename** (ADR 0003). `handlers` is a
   layer name; the package is the Loki domain. Deferred because a parallel
   branch is mid-migration to a different MCP SDK inside that exact package, and
   a package rename turns that merge into a manual conflict resolution for no
   functional gain. It is a pure `git mv` whenever that branch lands.
2. **`cmd`'s package-level `handlerConfig handlers.Config`** (ADR 0001, "Honest
   limitation"). Cobra offers no channel from `PersistentPreRunE` to a
   subcommand's `RunE`: the hook returns only an `error`, and stuffing the value
   into `cmd.Context()` trades one hidden global for another. The global was
   pushed out to the outermost layer, where it is written exactly once before
   any command runs, rather than eliminated. Three of the four locks are gone;
   this one variable is not lock-protected and not concurrent.
3. **`internal/handlers` is still 22 files** (11 production + 11 test), the
   largest package in the tree. The split made each file single-purpose but did
   not reduce the package's surface. The natural next move is the ADR 0003
   rename plus a `loki/` sub-package for the HTTP client — deferred for the same
   reason as (1).
4. **`internal/handlers/loki_integration_test.go` is 1020 lines** and grew by 24.
   It is a `//go:build integration` file driving a real Loki in Docker; splitting
   it is a separate change with its own risk (container lifecycle is shared
   across the tests in it) and no reader benefit, since it is read top-to-bottom
   when a Loki behaviour changes and not otherwise.
5. **No linter was disabled to make the build pass.** Everything the new set
   flagged was fixed. The only linter deliberately *not* enabled is
   `paralleltest`: `cmd`, `internal/logging` and parts of `internal/handlers`
   are intentionally serial, because viper's global registry, logrus's global
   logger and a betterleaks/tiktoken global all race under `t.Parallel`.
   Enabling it would require blanket per-package exclusions covering most of the
   tree, which makes the linter meaningless. This is recorded as a comment in
   `.golangci.yml`.
6. **Narrow exclusions, not weakened thresholds.** `_test.go` files are excluded
   from `dupl`/`funlen`/`gocognit`/`gocyclo`/`nestif` (table-driven cases are
   data; splitting them hides the assertions) and from `forbidigo` (test helpers
   collect goroutine output behind a mutex). Production thresholds are the audit
   targets, unmodified.
7. **The bugs found during the refactor were reported, not fixed inside a
   refactor commit** — by design. They were then fixed in their own commits on
   the second half of this branch; see §5. The one that was not fixed is a
   packaging constraint, recorded in §5.1 and ADR 0004.

## 4. `BREAKING:`

**Yes — ten user-visible changes.** The refactor itself was behaviour-preserving
(§4.3 has the evidence), but the bug fixes that followed it change what a user
sees, on purpose. Every one of them is a fix; several are still breaking in the
sense that a client, script or deployment built against the old behaviour can
notice.

### 4.1 Changes a user must know about

These alter output a client may be parsing, or a deployment's startup:

1. **A bare `loki-mcp` now serves stdio.** It used to print help and exit 0.
   Anything that ran the binary with no subcommand expecting a help dump — a
   health probe, a script grepping the output — now gets a server that blocks
   on stdin. Conversely, `"args": []` client configs and a
   no-argument container now work. (`e8cbd29`)
2. **Log lines come back newest-first, always.** Post-processing (secret
   filter, keyword filter, `head`/`tail`) used to reverse the order within a
   stream; it no longer does. A client that compensated for the reversal will
   now see the order it was compensating for. `head` still means the N oldest
   and `tail` the N newest — those semantics are unchanged. (`8085d3f`)
3. **Stream order is now sorted and stable.** Streams come back ordered by
   label-set string instead of in Go map iteration order, so repeated identical
   queries agree. A client relying on the previous (random) order was already
   broken; one relying on Loki's original stream order will see a different
   one. (`f40a465`)
4. **`format: "json"` has a different payload shape.** It is now Loki's own
   query-range wire format —
   `{"status":…,"warnings":[…],"data":{"resultType":"streams","result":[{"stream":{…},"values":[["<unix-nanos>","<line>"]]}]}}`
   — rather than the Go-shaped re-marshalling of the internal structs. Any
   consumer parsing the old shape must be updated. `stats` is deliberately
   absent, because post-processing invalidates it. (`680d7da`)
5. **`raw` and `text` gained a trailing `Warnings:` block**, and it carries
   Loki's own warnings, which those formats used to discard entirely. A client
   parsing raw output line-by-line will see lines it did not see before.
   (`82be4b5`)
6. **The secret-omission note moved and is now tagged.** It used to be appended
   ad hoc by the renderers, in a different way per format, and could appear
   twice. It is now a single warning in the response's `Warnings`, rendered from
   there, prefixed `loki-mcp: ` to distinguish it from a warning Loki produced:
   `loki-mcp: 1 line omitted due to detected secrets`. (`2dfce48`)
7. **`--query-filter` with a pipeline is now a startup failure.**
   `--query-filter='{ns="prod"} |= "error"'` used to start fine and silently
   enforce only `{ns="prod"}`. It now exits 1 with an error naming the discarded
   stage. An operator whose config carries a pipeline will find the server
   refusing to start — which is the point: the old behaviour was a silently
   weaker containment control. Bare selectors are unaffected. (`909cc04`)
8. **`PORT` now beats `LOKI_PORT`; `LOKI_PORT` is not read at all.** A
   deployment that set `LOKI_PORT` and relied on it will now bind `PORT`'s value
   or the 8080 default. Precedence is `--port` (when passed) > `PORT` > 8080.
   (`e1df43c`)
9. **The container image defaults to HTTP mode.** `CMD ["http"]` was added, so
   `docker run -p 8080:8080 <image>` serves HTTP instead of exiting immediately.
   The compose service no longer needs a `command:`. An image consumer that
   passed `http` explicitly is unaffected; one that passed nothing gets a
   long-running HTTP server where it previously got an instant exit.
   (`91a801e`)
10. **A repository-level change:** the tree now carries a MIT `LICENSE`, where
    it previously had none in the tree or in history. Downstream consumers who
    treated the absence as "all rights reserved" now have a grant. (`46d8fbf`)

### 4.2 Fixes that only ever made things correct

Observable in principle, but no reasonable consumer can have depended on the
old behaviour:

- **`version`, `help` and `completion` no longer need a reachable Loki.** They
  used to exit 1 with empty stdout when it was down. Nothing depends on
  `version` failing. (`a39b852`)
- **`version` prints through cobra's writer**, so `SetOut` captures it. This is
  invisible outside the test suite. (`5ea43d2`)
- **`RunHTTP` returns nil on a clean signalled shutdown.** It used to return
  `context.Canceled` because the shutdown context was derived from the signal
  context that had just fired, which also made the 5s grace period dead code.
  The process now exits 0 on SIGTERM and in-flight requests actually get drained.
  (`3ce3bbe`)
- **Label names are percent-escaped exactly once** in the label-values URL that
  the query-filter path builds, so a label containing `/` goes out as `%2F`
  rather than `%252F`. Such a request previously could not have worked.
  (`8772d7b`, `49adced`)
- **One secret detector per process**, built under a `sync.Once`, removing a
  data race inside the betterleaks/tiktoken-go dependency. No output change.
  (`e5f6f05`)
- **Promtail collects container logs** in the local compose stack, so the
  `log-generator` output reaches Loki. Development-stack only. (`c9e1c14`)

### 4.3 The refactor half really was behaviour-preserving

Every identifier the refactor commits changed lives under `internal/` or `cmd/`,
both unimportable from outside the module. Measured against the `7441901`
binary, before any of the fixes above landed:

- **Tool schemas.** `tools/list` was driven over stdio against both binaries and
  the results compared with sorted keys: identical, 183 lines of JSON. The same
  three schemas are golden files under `internal/handlers/testdata/` and are
  asserted on every `go test ./internal/handlers` run. **They are still
  identical at HEAD** — none of the fixes touched a tool schema.
- **`--help`.** `--help`, `stdio --help`, `http --help` and `version --help`
  were byte-identical between the two binaries, as was `version` output.
- **Tool output.** 19 differential tool calls (three tools × three formats, plus
  `head`, `filter`, an unknown format, missing required arguments, and the
  head+tail conflict) produced byte-identical JSON-RPC responses from both.
- **One apparent difference was chased down and was not a regression.**
  `loki_query` with `format: raw` and the secret filter on its default `true`
  sometimes emitted streams in a different order. Running the same call 24 times
  against each binary: baseline produced order A 21 times and order B 3 times;
  the refactored binary produced A 20 and B 4. That was finding 5 — the secret
  filter regrouping entries through a map — equally nondeterministic on the
  baseline. It has since been fixed (§4.1 item 3), which is itself one of the
  breaking changes.

## 5. Bugs found and fixed

Fourteen of the fifteen findings are fixed on this branch, each in its own
commit, none inside a refactor commit. The exception is finding 2, which turned
out to be a constraint rather than a bug; it is §5.1.

| # | Severity | The bug | The fix | Commit |
|---|---|---|---|---|
| 1 | High | Bare `loki-mcp` printed help and exited 0, so the Dockerfile `ENTRYPOINT` and the compose service started a container that immediately exited. | `rootCmd.RunE` is now `runStdio`, the same function `stdioCmd` uses, so the bare invocation *is* the stdio command; and the image gained `CMD ["http"]` so a container with no argument serves HTTP. | `e8cbd29`, `91a801e` |
| 3 | Medium | `version`, `help` and `completion` all required a reachable Loki, because `PersistentPreRunE` pinged it before every runnable subcommand. | `pingLoki` moved out of the persistent hook into the two serving `RunE` bodies. The hook still initialises logging, telemetry and the handler config. | `a39b852` |
| 4 | Medium | Post-processing silently reversed log line order within a stream. | `collectAndSortEntries` sorts newest-first, matching the `direction=BACKWARD` query, and ties break on line then label set so the order is a function of the input alone. `applyHeadTail` slices that descending list. | `8085d3f` |
| 5 | Medium | Stream order was nondeterministic: the regrouping ran through a map. | `entriesToStreams` sorts the grouped streams by label-set string before returning them. | `f40a465` |
| 6 | Medium | `format: "json"` was not Loki API JSON and could not be parsed back by a Loki client: `loghttp` defines `UnmarshalJSON` but no `MarshalJSON` and carries no struct tags. | A `lokiStreamsResponse` DTO renders the real wire shape, proven by a round-trip test through `loghttp.QueryResponse.UnmarshalJSON`. | `680d7da` |
| 7 | Medium | The HTTP graceful-shutdown grace period was dead code: the shutdown context was derived from the signal context, already cancelled. | The shutdown context is rooted at `context.Background()` with a 5s timeout, so the drain is real and a clean signalled shutdown returns nil. | `3ce3bbe` |
| 8 | Medium | `LOKI_PORT` silently overrode the documented `PORT`, because viper's `AutomaticEnv` resolves the prefixed name first. | The `port` flag is left unbound from viper and `resolvePort` reads the two sources by hand: `--port` when passed, then `PORT`, then 8080. `LOKI_PORT` is not consulted. | `e1df43c` |
| 9 | Medium | No `LICENSE` file existed, in the tree or anywhere in history, though the README promised one. | MIT `LICENSE` added. | `46d8fbf` |
| 10 | Low | Label names were path-escaped twice, so a label containing `/` went out as `%252F`. | The label-values URL sets `url.URL.Path` to the decoded form and `RawPath` to the encoded one, so the name is escaped exactly once, as a single path segment. | `8772d7b`, `49adced` |
| 11 | Low (upstream) | Two `*detect.Detector`s scanning concurrently race inside tiktoken-go, whose `SetBpeLoader` is a process-global written from a per-detector `sync.Once`. | One detector per process, built under a package-level `sync.Once`. The real fix belongs upstream; this closes it from our side and makes concurrent `NewConfig` calls safe. | `e5f6f05` |
| 12 | Low | The secret-filter note was reported two different ways depending on format, and could appear twice. | It is appended once to the response's `Warnings`; every renderer reads it from there, and `tagInternalWarnings` prefixes it `loki-mcp: `. `raw` and `text` gained the `Warnings:` block, which also surfaces Loki's own warnings for the first time. | `82be4b5`, `2dfce48` |
| 13 | Low | `version` wrote to the process stdout with `fmt.Println`, bypassing cobra's writer. | It prints through `cmd.OutOrStdout()`. | `5ea43d2` |
| 14 | Low | The compose `log-generator`'s output never reached Loki: promtail only scraped `/var/log/*log`. | A `docker_sd_configs` scrape job over the already-mounted Docker socket collects the compose containers' stdout/stderr as `job=docker`. | `c9e1c14` |
| 15 | Medium | `--query-filter` silently discarded everything but the stream selector: `{namespace="prod"} \|= "error"` enforced only `namespace="prod"` while the startup log echoed the full string back. | `ParseFilter` fails closed. Only bare stream selectors are accepted; anything else is a startup error naming the stage that could not be enforced. | `909cc04` |

The fixes landed through six merges: `55482b3`, `49984b8`, `e380b2b`,
`b7bebef`, `b1664d3`, `8de3662`.

Findings 4, 5 and 12 had been re-verified against `7441901` and marked
pre-existing (`4cce58e`) before being fixed. Finding 15 was verified end to end
against both binaries (`8815ec6`).

### 5.1 Accepted constraints

| # | Severity | Summary | Disposition |
|---|---|---|---|
| 2 | High | `go install github.com/kaiko-ai/loki-mcp@latest` fails: `go.mod` carries a `replace` directive for `github.com/hashicorp/memberlist`, and `go install pkg@version` refuses modules with one. | **Accepted, not fixed.** See [ADR 0004](adr/0004-go-install-is-unsupported.md). |

The dependency is not removable without giving up `--query-filter`: enforcing it
needs `logql/syntax`, which transitively pulls `dskit/kv/memberlist`, which needs
Grafana's memberlist fork, which is only reachable through a `replace`. Both ways
out are closed — upstream memberlist lacks the `NodeState`, `NodeSelection` and
`PushPullNodes` APIs `dskit` uses, and the fork declares
`module github.com/hashicorp/memberlist` so it cannot be required under its own
path. Distribution is source builds, the goreleaser release binaries and the
container image; `README.md` and `CLAUDE.md` say so.

## 6. Definition of done

The brief itself is not in this repository; this checklist is its §10 as
restated in the closing task, plus the per-commit green gates from
`docs/refactor-audit.md` §4.

| # | Item | Status | Evidence |
|---|---|---|---|
| 1 | `go build ./...` clean | done | Run on every commit; clean at HEAD. |
| 2 | `go vet ./...` clean | done | Clean at HEAD; now also a CI step (`24e2af3`). |
| 3 | `go test ./...` passes | done | 5 test packages, all `ok`. |
| 4 | `go test -race ./...` passes | done | 5 test packages, all `ok`. |
| 5 | Integration tests compile | done | `go test -tags=integration -c -o /dev/null ./internal/handlers`; CI's integration job builds and runs them. |
| 6 | `gofmt -l .` empty | done | Empty at HEAD; enforced by the CI build job. |
| 7 | `golangci-lint run ./...` clean under the new config | done | 0 issues, 26 linters, integration build tag included. |
| 8 | No production function over 60 lines | done | funlen 60/40, production: 0 hits (was 5). |
| 9 | No production function over cyclomatic 12 | done | `gocyclo -over 12`, production: 0 (was 4). The peak is `injectMatchers` at 9. |
| 10 | No production function over cognitive 15 | **not done** | One: `telemetry.ToolHandlerMiddleware` at 16. The body is straight-line; the score is the nesting penalty from the two closures its `mcpserver` signature requires. Extracting them would move code without removing a branch, so it carries a `//nolint:gocognit` with that reasoning instead. |
| 11 | No file over 500 lines | **not done** | Three test files exceed it: `internal/handlers/loki_integration_test.go` (1020), `internal/handlers/format_test.go` (1005) and `internal/handlers/query_test.go` (616). The integration file is a `//go:build integration` suite sharing one Docker Loki lifecycle; splitting it has real risk and no reader benefit. The other two are tables of expected renderings and expected handler responses — data, not logic; both grew with the bug fixes' regression cases. **Production** files are all under 500: the largest is `internal/telemetry/telemetry.go` at 477, down from `internal/handlers/loki.go` at 769. |
| 12 | Locks removed or justified | done | 4 → 1. The survivor has ADR 0002 and a `//nolint:forbidigo` naming the guarded field; `forbidigo` blocks new ones. |
| 13 | Every goroutine has an owner and a guaranteed exit | done | One `go` statement in production, joined by a deferred receive in `serve`, with the exit argument written out. |
| 14 | Dependency direction documented and enforced | done | `docs/architecture.md`; `depguard` rules verified against `go list` output and sanity-checked by temporarily denying an import the code does have. |
| 15 | Docs true against the code | done | Every audit doc-drift item fixed; then a second pass after the bug fixes, because those made several statements false again. `README.md` and `CLAUDE.md` were re-verified by executing the commands in them. Runnable `Example` tests execute the doc claims in CI. |
| 16 | Bugs reported, not silently fixed | done | 15 findings, each written up with a reproduction; none fixed inside a refactor commit. 14 then fixed in their own commits (§5), the 15th accepted as a constraint with an ADR (§5.1). |
| 17 | No breaking change | **superseded** | True of the refactor commits (§4.3) and deliberately not true of the branch as a whole. §4.1 lists ten user-visible changes, all of them bug fixes. |
| 18 | Coverage improved | done | 29.9% → 88.1%; every package except `main` went from 0% to ≥79%. |
| 19 | Each commit independently green | done | 48 non-merge commits, each built and tested at the time it was made. |
| 20 | Bug write-ups retired once fixed | done | The findings document was deleted after this report absorbed it; nothing in the tree references it. |
