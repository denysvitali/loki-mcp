# Refactor report — loki-mcp

Baseline: `7441901` (`main`). Final: this branch, `refactor/complexity-reduction`.
27 non-merge commits.

Everything below was measured, not remembered. The baseline column comes from a
`git worktree add /tmp/baseline 7441901` checkout; the final column from this
worktree. Both were measured with the same tools on the same machine: Go
1.26.4, linux/amd64, `gocognit`, `gocyclo`, `dupl` and `golangci-lint` 2.11.4
from `/home/workspace/go/bin`.

## 1. Metrics

### Size

| Metric | Baseline `7441901` | Final | Δ |
|---|---|---|---|
| Go LOC, production | 2045 | 2887 | +842 (+41%) |
| Go LOC, test | 1937 | 5646 | +3709 (+192%) |
| Go LOC, total | 3982 | 8533 | +4551 |
| Go files, production | 15 | 23 | +8 |
| Go files, test | 4 | 21 | +17 |
| Longest file, any | `internal/handlers/loki_integration_test.go` 996 | `internal/handlers/loki_integration_test.go` 1020 | +24 |
| Longest production file | `internal/handlers/loki.go` 769 | `internal/telemetry/telemetry.go` 477 | −292 |
| 2nd/3rd longest production file | `telemetry.go` 271, `filter.go` 198 | `filter.go` 276, `format.go` 213 | — |

Production LOC grew because doc comments grew: every exported identifier now
carries its guarantees, and every multi-file package has a `doc.go`. The unit
of work — the longest file a reader has to hold in their head — shrank by 38%.

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
| Max cyclomatic complexity | 25 (`HandleLokiQuery`) | 10 (`Handlers.HandleQuery`) |
| Functions with gocyclo > 12 | 4 | 0 |
| Functions with gocyclo > 8 | 8 | 2 |
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
15 test functions exceed 60 lines.

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
| `cmd` | 0.0% | 79.5% |
| `internal/handlers` | 44.7% | 89.6% |
| `internal/logging` | 0.0% | 100.0% |
| `internal/server` | 0.0% | 83.0% |
| `internal/telemetry` | 0.0% | 79.9% |
| **Total** | **29.9%** | **87.3%** |

`main` stays at 0%: `main.go` is a version string and a call to `cmd.Execute()`,
and `cmd`'s own `TestMain`-level behaviour is covered in `cmd/main_test.go`.

## 2. What changed and why

### Correctness preserved

Nothing in this branch was allowed to change behaviour, and the claim was
tested rather than asserted:

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
  Byte-identical in every case. See §4.
- Coverage rose from 29.9% to 87.3%, so the pinning is real and not
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
belongs there and what does not, three ADRs recording the decisions that would
otherwise look arbitrary, and a `FINDINGS.md` that keeps bug reports out of the
refactor commits.

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
7. **The bugs in `FINDINGS.md` are reported, not fixed** — by design, see §5.

## 4. `BREAKING:`

**None.**

Every identifier this branch changed lives under `internal/` or `cmd/`, both
unimportable from outside the module. The MCP tool names, their parameters and
descriptions, the three output formats, the CLI flags and the environment
variables are unchanged. Verified, not assumed:

- **Tool schemas.** `tools/list` was driven over stdio against both the
  `7441901` binary and the final binary and the results compared with sorted
  keys: identical, 183 lines of JSON. The same three schemas are also golden
  files under `internal/handlers/testdata/` and are asserted on every
  `go test ./internal/handlers` run.
- **`--help`.** `--help`, `stdio --help`, `http --help` and `version --help`
  are byte-identical between the two binaries, as is `version` output.
- **Tool output.** 19 differential tool calls (three tools × three formats, plus
  `head`, `filter`, an unknown format, missing required arguments, and the
  head+tail conflict) produced byte-identical JSON-RPC responses from both
  binaries.
- **One apparent difference was chased down and is not a regression.**
  `loki_query` with `format: raw` and the secret filter on its default `true`
  sometimes emits streams in a different order. Running the same call 24 times
  against each binary: baseline produced order A 21 times and order B 3 times;
  the final binary produced A 20 and B 4. This is `FINDINGS.md` #5 — the secret
  filter regroups entries through a map — and it is equally nondeterministic on
  the baseline. Not introduced here, and not fixed here.

## 5. Bugs found and reported, not fixed

Full write-ups with reproductions in [`FINDINGS.md`](../FINDINGS.md).

| # | Severity | Summary |
|---|---|---|
| 1 | High | Bare `loki-mcp` with no subcommand prints help and exits 0, so the Dockerfile `ENTRYPOINT` and the compose service start a container that immediately exits. |
| 2 | High | `go install github.com/kaiko-ai/loki-mcp@latest` fails: `go.mod` carries a `replace` directive. |
| 3 | Medium | `version`, `help` and `completion` all require a reachable Loki, because `PersistentPreRunE` pings it before every subcommand. |
| 4 | Medium | The secret filter silently reverses log line order within a stream. |
| 5 | Medium | The secret filter makes stream order nondeterministic (map iteration). |
| 6 | Medium | `format: "json"` is not Loki API JSON and cannot be parsed back by a Loki client. |
| 7 | Medium | The HTTP graceful-shutdown grace period is dead code: the shutdown context is already cancelled. |
| 8 | Medium | `LOKI_PORT` silently overrides the documented `PORT`. |
| 9 | Medium | No LICENSE file exists, in the tree or anywhere in history. |
| 10 | Low | Label names are path-escaped twice. |
| 11 | Low (upstream) | Concurrency hazard in the secret detector dependency. |
| 12 | Low | The secret-filter note is reported two different ways depending on format. |
| 13 | Low | `version` writes to process stdout, bypassing cobra's writer. |
| 14 | Low | The compose `log-generator`'s output never reaches Loki (promtail scrapes the wrong path). |
| 15 | Medium | `--query-filter` silently discards everything but the stream selector: `{namespace="prod"} \|= "error"` enforces only `namespace="prod"`, while the startup log echoes the full string back. |

Findings 4, 5 and 12 were re-verified against `7441901` and marked pre-existing
(`4cce58e`). Finding 15 was verified end to end against both binaries
(`8815ec6`).

## 6. Definition of done

The brief itself is not in this repository; this checklist is its §10 as
restated in the closing task, plus the per-commit green gates from
`docs/refactor-audit.md` §4.

| # | Item | Status | Evidence |
|---|---|---|---|
| 1 | `go build ./...` clean | done | Run on every commit; clean at HEAD. |
| 2 | `go vet ./...` clean | done | Clean at HEAD; now also a CI step (`24e2af3`). |
| 3 | `go test ./...` passes | done | 5 packages, all `ok`. |
| 4 | `go test -race ./...` passes | done | 5 packages, all `ok`. |
| 5 | Integration tests compile | done | `go test -tags=integration -c -o /dev/null ./internal/handlers`; CI's integration job builds and runs them. |
| 6 | `gofmt -l .` empty | done | Empty at HEAD; enforced by the CI build job. |
| 7 | `golangci-lint run ./...` clean under the new config | done | 0 issues, 26 linters, integration build tag included. |
| 8 | No production function over 60 lines | done | funlen 60/40, production: 0 hits (was 5). |
| 9 | No production function over cyclomatic 12 | done | `gocyclo -over 12`, production: 0 (was 4). |
| 10 | No production function over cognitive 15 | **not done** | One: `telemetry.ToolHandlerMiddleware` at 16. The body is straight-line; the score is the nesting penalty from the two closures its `mcpserver` signature requires. Extracting them would move code without removing a branch, so it carries a `//nolint:gocognit` with that reasoning instead. |
| 11 | No file over 500 lines | **not done** | Two test files exceed it: `internal/handlers/loki_integration_test.go` (1020) and `internal/handlers/format_test.go` (718). The integration file is a `//go:build integration` suite sharing one Docker Loki lifecycle; splitting it has real risk and no reader benefit. `format_test.go` is a table of expected renderings — data, not logic. **Production** files are all under 500: the largest is `internal/telemetry/telemetry.go` at 477, down from `internal/handlers/loki.go` at 769. |
| 12 | Locks removed or justified | done | 4 → 1. The survivor has ADR 0002 and a `//nolint:forbidigo` naming the guarded field; `forbidigo` blocks new ones. |
| 13 | Every goroutine has an owner and a guaranteed exit | done | One `go` statement in production, joined by a deferred receive in `serve`, with the exit argument written out. |
| 14 | Dependency direction documented and enforced | done | `docs/architecture.md`; `depguard` rules verified against `go list` output and sanity-checked by temporarily denying an import the code does have. |
| 15 | Docs true against the code | done | Every audit doc-drift item fixed; runnable `Example` tests execute the doc claims in CI. |
| 16 | Bugs reported, not silently fixed | done | 15 entries in `FINDINGS.md`, each with a reproduction; none fixed in code. |
| 17 | No breaking change | done | §4: tool schemas, `--help` and 19 differential tool calls byte-identical against the `7441901` binary. |
| 18 | Coverage improved | done | 29.9% → 87.3%; every package except `main` went from 0% to ≥79%. |
| 19 | Each commit independently green | done | 27 non-merge commits, each built and tested at the time it was made. |
