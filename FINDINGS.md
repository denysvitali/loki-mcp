# Findings — bugs found during the refactor

Bugs found while auditing and refactoring `loki-mcp` are **reported here, never
silently fixed**. Nothing in this document has been changed in code. Each entry
names a fix but leaves it unimplemented.

**Line references** are against commit `633f7dc` (`refactor: telemetry
instruments behind atomic.Pointer`), the state of this worktree when the
findings were reproduced. Files under parallel refactoring have moved since —
in particular `internal/server/http.go` was later split into `newMux` / `serve`
/ a thin `RunHTTP`, and `cmd/` gained tests. Where that matters the entry says
so and quotes the code inline, so it stays readable against either state.

**Environment.** Go 1.26.4, linux/amd64. Binary built with
`go build -o /tmp/lokimcp-findings .` from this worktree. `docker`,
`docker-compose` and `make` are **not installed** here, so every Docker- and
compose-related claim is marked *verified by reading the config, not executed*.
Everything else was executed.

| # | Title | Severity | Reproduced |
|---|---|---|---|
| 1 | Bare invocation starts nothing | High | yes |
| 2 | `go install …@latest` fails: `replace` directive in `go.mod` | High | yes |
| 3 | `version` (and `help`, `completion`) require a reachable Loki | Medium | yes |
| 4 | Secret filter silently reverses log line order | Medium | yes |
| 5 | Secret filter makes stream order nondeterministic | Medium | yes |
| 6 | `format: "json"` is not Loki API JSON and cannot round-trip | Medium | yes |
| 7 | HTTP graceful-shutdown grace period is dead code | Medium | yes |
| 8 | `LOKI_PORT` silently overrides the documented `PORT` | Medium | yes |
| 9 | No LICENSE file, in the tree or in history | Medium | yes |
| 10 | Double path-escaping of label names | Low | yes |
| 11 | Concurrency hazard in the secret detector dependency | Low (upstream) | yes |
| 12 | Secret-filter note is reported two different ways | Low | yes |
| 13 | `version` writes to process stdout, bypassing cobra's writer | Low | yes |
| 14 | Compose `log-generator` output never reaches Loki | Low | by reading only |

---

## 1. Bare invocation starts nothing

**Severity:** High
**Location:** `cmd/root.go:54-80` (`rootCmd` has no `Run`/`RunE`); consequences at
`Dockerfile:28`, `docker-compose.yml:2-17`, `README.md:229,252,282`

### What happens

`loki-mcp` with no subcommand prints the help text and exits `0`. No MCP server
of any kind starts, and the exit status reports success.

### Why

`rootCmd` is declared with `Use`, `Short`, `Long` and `PersistentPreRunE` but no
`Run` or `RunE`. Cobra treats a root command without a run function as a pure
command group: it prints usage and returns `nil`, so `Execute()` never reaches
its `os.Exit(1)`.

### Reproduction

```bash
go build -o /tmp/lokimcp-findings .
/tmp/lokimcp-findings; echo "EXIT=$?"
```

### Expected vs actual

- **Expected:** either an MCP server starts (stdio being the obvious default),
  or the command fails with a nonzero status telling the user to pick a mode.
- **Actual:** help text on stdout, `EXIT=0`.

### Consequence chain

- `Dockerfile:28` is `ENTRYPOINT ["./loki-mcp"]` with no `CMD`, so
  `docker run loki-mcp-server` runs the bare binary: the container prints help
  and exits `0` immediately.
- `docker-compose.yml:2-17` defines `loki-mcp-server` with `ports: ["8080:8080"]`
  and `depends_on: loki: {condition: service_healthy}`. Compose waits for a
  healthy Loki, publishes 8080, then starts a container that exits before
  binding anything. Nothing ever listens on 8080.
- Every MCP client config in `README.md` (Claude Code line 229, Claude Desktop
  line 252, Cursor line 282) used `"args": []`, i.e. this same bare invocation.
  A client launching it sees the process exit at once with help text on stdout,
  which is also a protocol violation for the stdio transport.

*The Docker/compose half of this chain is verified by reading `Dockerfile` and
`docker-compose.yml`; docker is not installed here. The binary behaviour that
causes it was executed.*

### Suggested fix (not implemented)

Give `rootCmd` a `RunE` that runs stdio (or a `Run` that prints help and exits
nonzero), and add `CMD ["http"]` to the Dockerfile so the compose service has
something to run.

---

## 2. `go install github.com/kaiko-ai/loki-mcp@latest` fails

**Severity:** High (the advertised primary install method has never worked)
**Location:** `go.mod:344`

```
replace github.com/hashicorp/memberlist => github.com/grafana/memberlist v0.3.1-0.20251126142931-6f9f62ab6f86
```

### What happens

The install command printed at `README.md:12` and `CLAUDE.md:109` fails; no
binary is produced.

### Why

`go install pkg@version` refuses to build a module whose `go.mod` contains
`replace` directives, because those would make the module build differently from
how it builds as a dependency. The directive is present in the published
`v1.5.0` as well, so no released version installs this way.

### Reproduction

```bash
export GOPATH=/tmp/gopath-install GOBIN=/tmp/gopath-install/bin
mkdir -p "$GOBIN"
go install github.com/kaiko-ai/loki-mcp@latest; echo "EXIT=$?"
```

### Expected vs actual

- **Expected:** a `loki-mcp` binary in `$GOBIN`, exit 0.
- **Actual:**

```
go: downloading github.com/kaiko-ai/loki-mcp v1.5.0
go: github.com/kaiko-ai/loki-mcp@latest (in github.com/kaiko-ai/loki-mcp@v1.5.0):
	The go.mod file for the module providing named packages contains one or
	more replace directives. It must not contain directives that would cause
	it to be interpreted differently than if it were the main module.
EXIT=1
```

### Suggested fix (not implemented)

Drop the `replace` (pin `github.com/grafana/memberlist` through a normal
`require` on its own module path if the fork is still needed), or stop
advertising `go install` and point users at the release binaries and image.

---

## 3. `version` — and `help` and `completion` — require a reachable Loki

**Severity:** Medium
**Location:** `cmd/root.go:63-79` (`PersistentPreRunE`), `cmd/root.go:131-145` (`pingLoki`)

### What happens

`loki-mcp version` makes a live Loki `ListLabelNames` call before printing
anything, and exits 1 with `loki connectivity check failed: …` plus a full usage
dump when Loki is down. **Nothing at all is written to stdout** in that case.
`loki-mcp help` and `loki-mcp completion bash` fail identically, which breaks
shell-completion installation on any machine without Loki.

### Why

`PersistentPreRunE` on the root command ends in `return pingLoki()`, and cobra
runs the root's persistent pre-run for **every runnable** subcommand. Note the
asymmetry: the root command itself has no `Run`, so cobra skips the hook
entirely for a bare invocation (finding 1) — the one invocation where a
connectivity check would plausibly be wanted is the one that skips it.

### Reproduction

```bash
LOKI_URL=http://127.0.0.1:1 /tmp/lokimcp-findings version;                     echo "version EXIT=$?"
LOKI_URL=http://127.0.0.1:1 /tmp/lokimcp-findings version 2>/dev/null;         echo "  (stdout above is empty)"
LOKI_URL=http://127.0.0.1:1 /tmp/lokimcp-findings help >/dev/null;             echo "help EXIT=$?"
LOKI_URL=http://127.0.0.1:1 /tmp/lokimcp-findings completion bash >/dev/null;  echo "completion EXIT=$?"
LOKI_URL=http://127.0.0.1:1 /tmp/lokimcp-findings --help >/dev/null;           echo "--help EXIT=$?"
```

Also pinned end to end on the merged branch by `cmd/root_test.go`
(`TestVersionRequiresLoki`).

### Expected vs actual

- **Expected:** `loki-mcp version` prints `loki-mcp <version>` and exits 0
  regardless of Loki; same for `help` and `completion`.
- **Actual:** `version EXIT=1`, `help EXIT=1`, `completion EXIT=1`, and empty
  stdout for `version`. (`--help` as a *flag* exits 0, because cobra
  short-circuits before the hook.) The failure also prints the whole usage
  block, because `SilenceUsage` is not set.

### Suggested fix (not implemented)

Move `pingLoki()` out of the root `PersistentPreRunE` into the `stdio` and
`http` `RunE`s, and set `SilenceUsage: true` on `rootCmd`.

---

## 4. The secret filter silently reverses log line order

**Severity:** Medium
**Location:** `internal/handlers/query.go:103` (post-processing gate),
`internal/handlers/entries.go:71-73` (ascending sort)

### What happens

Turning the secret filter on — which is the **default** — flips returned log
lines from newest-first to oldest-first. A flag whose documented purpose is
redaction changes result ordering.

### Why

`runQueryRange` asks Loki for `logproto.BACKWARD` ("newest first",
`query.go:87`). `applyPostProcessing` returns early only when there is no
keyword filter, no head/tail **and** `h.cfg.Detector == nil`. With a detector
present, `collectAndSortEntries` flattens all streams and re-sorts them
`Timestamp.Before` — ascending — discarding the direction the query asked for.

Pre-existing at baseline `7441901`: `HandleLokiQuery` gated post-processing on
the identical condition (`params.Filter != "" || params.Head > 0 ||
params.Tail > 0 || secretFilteringEnabled()`) and called the same
`collectAndSortEntries`. The refactor renamed the code, it did not introduce
the behaviour.

### Reproduction

```bash
cat > /tmp/desc.py <<'PYEOF'
import json, http.server
# Loki with direction=BACKWARD returns values newest-first.
Q = {"status":"success","data":{"resultType":"streams","result":[
  {"stream":{"job":"api"},"values":[
     ["1705312803000000000","fourth (newest)"],
     ["1705312802000000000","third"],
     ["1705312801000000000","second"],
     ["1705312800000000000","first (oldest)"]]}],"stats":{}}}
L = {"status":"success","data":["job"]}
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        raw = json.dumps(Q if "query_range" in self.path else L).encode()
        self.send_response(200); self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def log_message(self,*a): pass
http.server.HTTPServer(("127.0.0.1",3201),H).serve_forever()
PYEOF
python3 /tmp/desc.py & sleep 1

runq() {
  printf '%s\n' \
   '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}' \
   '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
   '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"loki_query","arguments":{"query":"{job=\"api\"}","format":"raw","start":"2024-01-15T09:00:00Z","end":"2024-01-15T11:00:00Z"}}}' \
  | LOKI_URL=http://127.0.0.1:3201 /tmp/lokimcp-findings "$@" stdio 2>/dev/null \
  | tail -1 | python3 -c 'import sys,json; print(json.load(sys.stdin)["result"]["content"][0]["text"])'
}
echo "=== secret-filter ON (default) ==="; runq
echo "=== secret-filter OFF ==="; runq --secret-filter=false
```

### Expected vs actual

- **Expected:** identical ordering with the filter on and off; only
  secret-bearing lines differ.
- **Actual:**

```
=== secret-filter ON (default) ===
2024-01-15T10:00:00Z {job="api"} first (oldest)
2024-01-15T10:00:01Z {job="api"} second
2024-01-15T10:00:02Z {job="api"} third
2024-01-15T10:00:03Z {job="api"} fourth (newest)

=== secret-filter OFF ===
2024-01-15T10:00:03Z {job="api"} fourth (newest)
2024-01-15T10:00:02Z {job="api"} third
2024-01-15T10:00:01Z {job="api"} second
2024-01-15T10:00:00Z {job="api"} first (oldest)
```

### Suggested fix (not implemented)

Sort descending in `collectAndSortEntries` to match the `BACKWARD` direction
(inverting `applyHeadTail` accordingly), or derive the sort direction from the
query direction instead of hard-coding it.

---

## 5. The secret filter makes stream order nondeterministic

**Severity:** Medium
**Location:** `internal/handlers/entries.go:151-155` (`entriesToStreams`)

### What happens

With the secret filter on (the default), two identical `loki_query` calls that
return more than one stream can return the streams in different orders. Tool
output is not reproducible.

### Why

`entriesToStreams` regroups entries into a `map[string]*loghttp.Stream` and then
builds the result slice by ranging over that map. Go randomises map iteration
order. Because finding 4's post-processing branch is entered on every query by
default, the randomisation reaches ordinary users.

Pre-existing at baseline `7441901`: `entriesToStreams` had the same map-ranging
implementation and the same default-on gate. The refactor moved it from
`loki.go` to `entries.go` without changing it.

### Reproduction

```bash
cat > /tmp/multi.py <<'PYEOF'
import json, http.server
def s(job, ts, line): return {"stream":{"job":job},"values":[[ts,line]]}
Q = {"status":"success","data":{"resultType":"streams","result":[
  s("aaa","1705312800000000000","line from aaa"), s("bbb","1705312801000000000","line from bbb"),
  s("ccc","1705312802000000000","line from ccc"), s("ddd","1705312803000000000","line from ddd"),
  s("eee","1705312804000000000","line from eee")],"stats":{}}}
L = {"status":"success","data":["job"]}
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        raw = json.dumps(Q if "query_range" in self.path else L).encode()
        self.send_response(200); self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def log_message(self,*a): pass
http.server.HTTPServer(("127.0.0.1",3200),H).serve_forever()
PYEOF
python3 /tmp/multi.py & sleep 1

run() {
  printf '%s\n' \
   '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}' \
   '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
   '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"loki_query","arguments":{"query":"{job=~\".+\"}","format":"raw","start":"2024-01-15T09:00:00Z","end":"2024-01-15T11:00:00Z"}}}' \
  | LOKI_URL=http://127.0.0.1:3200 /tmp/lokimcp-findings "$@" stdio 2>/dev/null \
  | tail -1 | python3 -c 'import sys,json; print(repr(json.load(sys.stdin)["result"]["content"][0]["text"]))'
}
echo "=== ON  ==="; for i in 1 2 3 4 5; do run; done
echo "=== OFF ==="; for i in 1 2 3 4 5; do run --secret-filter=false; done
```

### Expected vs actual

- **Expected:** five identical outputs in both blocks.
- **Actual:** the `OFF` block is stable; in the `ON` block runs disagree — runs
  2 and 3 emitted `{job="eee"}` first while runs 1, 4 and 5 emitted
  `{job="aaa"}` first.

### Suggested fix (not implemented)

Sort the streams by label-set string before returning from `entriesToStreams`,
or keep an insertion-ordered key slice alongside the map.

---

## 6. `format: "json"` is not Loki API JSON and cannot be parsed back

**Severity:** Medium
**Location:** `internal/handlers/format.go:36` (`marshalIndentJSON`), applied to
`*loghttp.QueryResponse` at `format.go:53,68`

### What happens

The `json` output format re-marshals the decoded `loghttp.QueryResponse` Go
struct. Entries come out as objects with Go field names —
`{"Timestamp":"2024-01-15T10:00:00Z","Line":"…","StructuredMetadata":{},"Parsed":{}}` —
instead of Loki's wire format `["1705312800000000000","…"]`. Feeding the output
back into any Loki-aware JSON parser fails.

### Why

`loghttp.Entry` (Loki v3.7.3, `pkg/loghttp/entry.go:20-27`) defines
`UnmarshalJSON` but **no** `MarshalJSON`, and has no struct tags. The asymmetry
means decode-then-encode does not round-trip. `loghttp.Stream` has the same
asymmetry, so an empty `values` array decodes to `nil` and re-encodes as
`"values":null`.

### Reproduction

In a scratch module whose `go.mod`/`go.sum` are copied from this repo:

```go
const lokiWire = `{"status":"success","data":{"resultType":"streams","result":[` +
	`{"stream":{"job":"api"},"values":[["1705312800000000000","harmless line"]]}],"stats":{}}}`

func TestQueryResponseRoundTrip(t *testing.T) {
	var in loghttp.QueryResponse
	if err := json.Unmarshal([]byte(lokiWire), &in); err != nil { t.Fatal(err) }
	out, _ := json.Marshal(&in)
	t.Logf("re-marshalled: %s", out)
	var back loghttp.QueryResponse
	if err := json.Unmarshal(out, &back); err != nil { t.Fatalf("ROUND TRIP FAILED: %v", err) }
}
```

```bash
mkdir -p /tmp/detrace && cp go.mod go.sum /tmp/detrace/
cd /tmp/detrace && go mod edit -module=detrace
# write the test above as roundtrip_test.go
go test -run TestQueryResponseRoundTrip -count=1 -v .
```

### Expected vs actual

- **Expected:** `format: "json"` emits the Loki query-range response shape, so
  the output is consumable by anything that speaks the Loki API.
- **Actual:**

```
re-marshalled: …"values":[{"Timestamp":"2024-01-15T10:00:00Z","Line":"harmless line",
               "StructuredMetadata":{},"Parsed":{}}]…
ROUND TRIP FAILED: Value looks like Number/Boolean/None, but can't find its end: ',' or '}' symbol
```

  The same shape is visible end to end via `format: "json"` on a real
  `loki_query` call (see the reproduction in finding 12), along with a
  ~180-line zero-valued `stats` block that is never recomputed after filtering
  (`totalEntriesReturned: 0` even when entries were returned).

### Suggested fix (not implemented)

Marshal a small purpose-built DTO — or the original response body when no
post-processing happened — rather than the `loghttp` structs.

---

## 7. HTTP graceful-shutdown grace period is dead code

**Severity:** Medium
**Location:** `internal/server/http.go:87-96` at `633f7dc`; the same two lines
survive the later `newMux`/`serve`/`RunHTTP` split, so check the shutdown
context's parent wherever `defaultShutdownTimeout` is now used.

```go
cancel()                                                                         // cancels ctx
shutdownCtx, shutdownCancel := context.WithTimeout(ctx, defaultShutdownTimeout)  // derived from the cancelled ctx
defer shutdownCancel()
if err := httpServer.Shutdown(shutdownCtx); err != nil { … return err }
```

### What happens

On `SIGINT`/`SIGTERM`, if any connection is still in flight (an open `/sse`
stream, a slow `/stream` request), `http.Server.Shutdown` returns
`context.Canceled` immediately. In-flight requests are dropped instead of being
given the 5-second grace period, `RunHTTP` returns `context.Canceled` instead of
`nil`, and the process exits with status **1** plus a cobra usage dump on a
perfectly ordinary termination signal.

### Why

`cancel()` cancels `ctx`; the next line derives `shutdownCtx` from that
already-cancelled parent, so `shutdownCtx` is done the moment it is created.
`Shutdown` closes the listeners, then checks `closeIdleConns()`: with no active
connections that returns true and `Shutdown` returns `nil` — which is why the
bug is invisible on an idle server — but with an active connection it falls
through to `case <-ctx.Done(): return ctx.Err()`.

Pre-existing at baseline `7441901`; the old code had the same
`cancel()` / `context.WithTimeout(ctx, …)` adjacency.

### Reproduction

End-to-end, with one in-flight SSE connection:

```bash
#!/bin/bash
LOKI_URL=http://127.0.0.1:3199 /tmp/lokimcp-findings http --port 8111 --host 127.0.0.1 > /tmp/httpsrv.log 2>&1 &
SRV=$!; sleep 2
curl -sN --max-time 30 http://127.0.0.1:8111/sse > /dev/null 2>&1 &   # in-flight connection
CURL=$!; sleep 2
START=$(date +%s%N); kill -TERM $SRV; wait $SRV; EXIT=$?; END=$(date +%s%N)
kill $CURL 2>/dev/null
echo "SERVER_EXIT=$EXIT ELAPSED_MS=$(( (END-START)/1000000 ))"
tail -3 /tmp/httpsrv.log
```

(any reachable Loki satisfies the startup ping; `3199` above is a stub server.)

Under load in the unit tests, the same race surfaces as flakes:

```bash
for i in $(seq 1 6); do go test -race -count=10 -run TestRunHTTP ./internal/server/ & done; wait
```

— roughly 33 failures per 180 iterations, each logging
`HTTP server shutdown error: context canceled`.

### Expected vs actual

- **Expected:** up to `defaultShutdownTimeout` (5 s) of drain, then
  `HTTP server stopped gracefully`, `RunHTTP` returns `nil`, exit 0.
- **Actual:**

```
SERVER_EXIT=1 ELAPSED_MS=5
level=info  msg="Shutting down HTTP server..."
level=error msg="HTTP server shutdown error: context canceled"
level=error msg="HTTP server error: context canceled"
Error: context canceled
```

  With **no** in-flight connection the identical script yields `SERVER_EXIT=0`
  and `HTTP server stopped gracefully`, which is why this survived review.

### Suggested fix (not implemented)

Derive the shutdown context from `context.Background()`:
`context.WithTimeout(context.Background(), defaultShutdownTimeout)`.

*Not applied because it changes observable behaviour* (return value and exit
status on signal), which this refactor forbids. The characterization test on the
merged branch therefore tolerates **both** `nil` and `context.Canceled` on that
path, with a comment pointing here. The planned `signal.NotifyContext` +
`errgroup` rewrite must make the parent-context choice deliberately rather than
fixing or preserving this by accident.

---

## 8. `LOKI_PORT` silently overrides the documented `PORT`

**Severity:** Medium
**Location:** `cmd/http.go:44-47`

```go
// PORT (without LOKI_ prefix) is the conventional env var for HTTP port.
httpCmd.Flags().StringP("port", "p", "8080", "HTTP server port [env: PORT]")
_ = viper.BindPFlag("port", httpCmd.Flags().Lookup("port"))
_ = viper.BindEnv("port", "PORT")
```

### What happens

The flag's usage string promises `[env: PORT]`, and the comment says `PORT` is
the intended variable. But an undocumented `LOKI_PORT` takes precedence: with
both set, the server binds the `LOKI_PORT` value and never mentions `PORT`.

### Why

`viper.AutomaticEnv()` is enabled process-wide with prefix `LOKI` and a
`-`→`_` key replacer (`cmd/root.go:46-51`). Viper's resolution order consults
the automatic-env lookup — which resolves key `port` to `LOKI_PORT` — **before**
any explicit `BindEnv` alias. So the generic prefix mechanism shadows the
deliberate, documented binding.

### Reproduction

```bash
run() { /tmp/lokimcp-findings http --host 127.0.0.1 > /tmp/p.log 2>&1 & local S=$!
        sleep 2; grep -o 'Starting MCP HTTP server on http://127.0.0.1:[0-9]*' /tmp/p.log
        kill -TERM $S 2>/dev/null; wait $S 2>/dev/null; }
export LOKI_URL=http://127.0.0.1:3199
( export LOKI_PORT=7777 PORT=9999; run )   # both
( export PORT=9999; unset LOKI_PORT; run ) # documented one only
( export LOKI_PORT=7777; unset PORT; run ) # undocumented one only
( unset LOKI_PORT PORT; run )              # neither
```

Also pinned on the merged branch by `cmd/http_test.go`.

### Expected vs actual

- **Expected:** `PORT` wins, or at minimum `LOKI_PORT` is documented and the
  precedence is stated.
- **Actual:**

```
LOKI_PORT=7777 PORT=9999  -> Starting MCP HTTP server on http://127.0.0.1:7777
PORT=9999 only            -> Starting MCP HTTP server on http://127.0.0.1:9999
LOKI_PORT=7777 only       -> Starting MCP HTTP server on http://127.0.0.1:7777
neither                   -> Starting MCP HTTP server on http://127.0.0.1:8080
```

  A deployment that sets both — or that sets `LOKI_PORT` assuming it is ignored
  — binds the wrong port with no warning.

### Suggested fix (not implemented)

Read the port with an explicit precedence (`os.LookupEnv("PORT")` checked before
`viper.GetString("port")`), or register the port flag through `envFlag` and
document `LOKI_PORT` as the real name.

---

## 9. No LICENSE file exists, in the tree or in history

**Severity:** Medium (legal/packaging, not code)
**Location:** repository root; claim at `README.md:296`

### What happens

`README.md` states "This project is licensed under the MIT License - see the
LICENSE file for details." There is no such file, and there never was.
Downstream packagers, `go install` consumers and license scanners see an
unlicensed repository.

### Why

The file was never added, and nothing in the build or CI checks for one.

### Reproduction

```bash
ls LICENSE* COPYING 2>/dev/null || echo "none in tree"
git log --all --diff-filter=A --name-only -- LICENSE LICENSE.md LICENSE.txt COPYING
grep -n -i licen README.md
```

### Expected vs actual

- **Expected:** an MIT `LICENSE` file at the repository root.
- **Actual:** `none in tree`; the `git log --all` query prints nothing (no
  LICENSE has ever been added on any ref); the README claim is the only
  statement of licensing anywhere in the project.

### Suggested fix (not implemented)

Add the MIT license text as `LICENSE` with the correct copyright holder and year.

---

## 10. Double path-escaping of label names

**Severity:** Low
**Location:** `internal/handlers/loki_http.go:46` and `:58`

```go
path := fmt.Sprintf("/loki/api/v1/label/%s/values", url.PathEscape(labelName))  // :46
…
u.Path = path                                                                    // :58
```

### What happens

A label name containing characters that need escaping is escaped twice. Label
`a/b` goes out on the wire as `…/label/a%252Fb/values`; Loki decodes that once
and looks up a label literally named `a%2Fb`.

### Why

`url.URL.Path` holds the **decoded** path. `u.String()` calls `EscapedPath()`,
which percent-encodes it again, so the manual `url.PathEscape` on line 46 is one
escape too many. The field for a pre-escaped path is `u.RawPath` (with `u.Path`
holding the decoded form).

### Reproduction

```bash
mkdir -p /tmp/escrepro && cd /tmp/escrepro && go mod init escrepro >/dev/null
cat > main.go <<'GOEOF'
package main

import (
	"fmt"
	"net/url"
)

func main() {
	u, _ := url.Parse("http://loki:3100")
	u.Path = fmt.Sprintf("/loki/api/v1/label/%s/values", url.PathEscape("a/b"))
	fmt.Println("u.Path      =", u.Path)
	fmt.Println("on the wire =", u.String())
}
GOEOF
go run .
```

The behaviour is currently pinned by a characterization test:

```bash
go test ./internal/handlers -run TestLokiHTTPClient_Paths -v
```

### Expected vs actual

- **Expected:** `http://loki:3100/loki/api/v1/label/a%2Fb/values`
- **Actual:** `http://loki:3100/loki/api/v1/label/a%252Fb/values`
  (`u.Path` is `/loki/api/v1/label/a%2Fb/values`). The test subcase
  `label_values_double-escapes_the_label_name` asserts the server-decoded path
  is `/loki/api/v1/label/we%2Fird%20name/values` — the still-escaped form — and
  passes.

Practical impact is small: Loki label names are `[a-zA-Z_][a-zA-Z0-9_]*` in
practice, so nothing needing escaping normally reaches this path.

### Suggested fix (not implemented)

Drop the `url.PathEscape` call and let `u.String()` escape the raw label name
exactly once; update the characterization test in the same commit.

---

## 11. Concurrency hazard in the secret detector dependency

**Severity:** Low, **upstream** (`github.com/betterleaks/betterleaks@v1.6.1`)
**Location:** `betterleaks/detect/detect.go:280-291` →
`github.com/pkoukk/tiktoken-go@v0.1.8/tiktoken.go:13-15`; reached from
`internal/handlers/secret_filter.go:33`

### What happens

Two `detect.Detector` instances scanning at the same time trip the Go race
detector with a write/write race on a process-global variable inside
`tiktoken-go`.

### Why

`(*Detector).Tokenizer()` is guarded by a **per-detector** `sync.Once`, but the
body it runs calls the **process-global** `tiktoken.SetBpeLoader(&TiktokenLoader{})`,
a plain unsynchronised assignment to the package variable `bpeLoader`. Two
detectors each run their own `Once` exactly once and both write the same global.
The tokenizer is reached lazily during rule evaluation
(`failsTokenEfficiency`), so the first *scan* triggers it, not construction.

### Reproduction

In a scratch module whose `go.mod`/`go.sum` are copied from this repo:

```go
func TestTwoDetectorsRace(t *testing.T) {
	const line = `aws_secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := detect.NewDetectorDefaultConfig()
			if err != nil { t.Error(err); return }
			d.SkipFindingAppend = true
			for j := 0; j < 20; j++ { _ = d.DetectString(line) }
		}()
	}
	wg.Wait()
}
```

```bash
mkdir -p /tmp/detrace && cp go.mod go.sum /tmp/detrace/
cd /tmp/detrace && go mod edit -module=detrace
# write the test above as detector_race_test.go
go test -race -run TestTwoDetectorsRace -count=1 .
```

### Expected vs actual

- **Expected:** a clean run under `-race`.
- **Actual:**

```
WARNING: DATA RACE
Write at 0x000001baef00 by goroutine 11:
  github.com/pkoukk/tiktoken-go.SetBpeLoader()
      .../tiktoken-go@v0.1.8/tiktoken.go:14
  github.com/betterleaks/betterleaks/detect.(*Detector).Tokenizer.func1()
      .../betterleaks@v1.6.1/detect/detect.go:282
  sync.(*Once).doSlow()
  … (*Detector).DetectString() …
Previous write at 0x000001baef00 by goroutine 10:  [same stack]
--- FAIL: TestTwoDetectorsRace  race detected during execution of test
```

### Impact

**Not reachable in production.** Exactly one `handlers.Config`, therefore one
`*detect.Detector`, is built per process (`cmd/root.go:106-128`), and a single
detector is safe because its `sync.Once` serialises the global write. The hazard
only constrains **tests**: any test that builds a detector must not call
`t.Parallel()`. That constraint is already documented on `newTestConfig`
(`internal/handlers/secret_filter_test.go:15-18`) and is why
`TestFilterSecretEntries_OmitsMatchingLines` and
`TestFilterSecretEntries_CancelledContext` run serially while their
detector-free sibling runs parallel.

### Suggested fix (not implemented)

Report upstream — `tiktoken.SetBpeLoader` should be mutex-guarded, or betterleaks
should set the loader from a package-level `sync.Once`. Locally, keep the
no-`t.Parallel()` rule documented on `newTestConfig`.

---

## 12. The secret-filter note is reported two different ways

**Severity:** Low / cosmetic (with one non-cosmetic side effect)
**Location:** `internal/handlers/query.go:49-51` and `:117`;
`internal/handlers/format.go:88-121` (`renderRaw`/`renderText` ignore `Warnings`)

### What happens

The same fact — "N lines were withheld" — reaches the client through two
unrelated channels depending on `format`, and neither channel is a superset of
the other:

- **raw / text:** `formattedResult += "\n" + formatSecretOmissionNote(n)`
  appends a bare sentence after the payload. `renderRaw` already ends with
  `\n`, so the result is a blank line and then the note; a client cannot
  distinguish it from a log line.
- **json:** the note is `append`ed to `result.Warnings` (`query.go:117`) and
  marshalled inside the response object. The string trailer is explicitly
  suppressed by the `Format(params.Format) != FormatJSON` guard.

The non-cosmetic side effect: `renderRaw` and `renderText` never render
`result.Warnings` at all, so **genuine Loki warnings are silently dropped for
raw and text clients**, while for JSON clients the injected note is
indistinguishable from a real Loki warning in the same array.

### Why

Two independent additions. `applyPostProcessing` mutates the response object
(the JSON path), while `HandleQuery` separately post-processes the rendered
string (the text path), because the text renderers have nowhere to put a
warning.

### Reproduction

```bash
cat > /tmp/fakeloki.py <<'PYEOF'
import json, http.server
Q = {"status":"success","data":{"resultType":"streams","result":[
      {"stream":{"job":"api"},"values":[
        ["1705312800000000000","harmless line"],
        ["1705312801000000000","api_key = \"aZ9xY8wV7uT6sR5qP4nM3kL2jH1gF0dS\""]]}],
     "stats":{}},
     "warnings":["a genuine warning from Loki"]}
L = {"status":"success","data":["job","level"]}
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        raw = json.dumps(Q if "query_range" in self.path else L).encode()
        self.send_response(200); self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def log_message(self,*a): pass
http.server.HTTPServer(("127.0.0.1",3199),H).serve_forever()
PYEOF
python3 /tmp/fakeloki.py & sleep 1

call() {
  printf '%s\n' \
   '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}' \
   '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
   "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"loki_query\",\"arguments\":{\"query\":\"{job=\\\"api\\\"}\",\"format\":\"$1\",\"start\":\"2024-01-15T09:00:00Z\",\"end\":\"2024-01-15T11:00:00Z\"}}}" \
  | LOKI_URL=http://127.0.0.1:3199 /tmp/lokimcp-findings stdio 2>/dev/null \
  | tail -1 | python3 -c 'import sys,json; print(json.load(sys.stdin)["result"]["content"][0]["text"])'
}
echo "=== raw ==="; call raw
echo "=== json ==="; call json | head -12
```

The in-tree characterization test covers the same split:
`go test ./internal/handlers -run TestHandleLokiQuery_SecretFiltering -v`.

### Expected vs actual

- **Expected:** one consistent channel for the note, and Loki's own warnings
  surfaced in every format.
- **Actual:**

```
=== raw ===
2024-01-15T10:00:00Z {job="api"} harmless line
                                          <- blank line from the "\n" + note join
1 line omitted due to detected secrets
                                          <- "a genuine warning from Loki" never shown

=== json ===
{
  "status": "success",
  "warnings": [
    "a genuine warning from Loki",
    "1 line omitted due to detected secrets"    <- indistinguishable from Loki's own
  ],
  …
```

### Suggested fix (not implemented)

Render `result.Warnings` in `renderRaw`/`renderText` as a single trailing
`Warnings:` block, and tag the injected note (a distinct field, or a
`loki-mcp: ` prefix) so a JSON client can tell it apart from an upstream warning.

---

## 13. `version` writes to the process stdout, bypassing cobra's writer

**Severity:** Low
**Location:** `cmd/version.go:14-16`

```go
Run: func(cmd *cobra.Command, args []string) {
	fmt.Println("loki-mcp", Version)
},
```

### What happens

The version string goes straight to `os.Stdout` via `fmt.Println`. Cobra's
output writer is ignored, so `rootCmd.SetOut(buf)` cannot capture it and an
embedding caller cannot redirect it. Every other command output in the tree
(usage, errors) does honour the writer, so the behaviour is inconsistent.

### Why

`fmt.Println` writes to the package-level `os.Stdout`; the idiomatic form is
`fmt.Fprintln(cmd.OutOrStdout(), …)`, which resolves to whatever writer was
installed with `SetOut` and falls back to `os.Stdout`.

### Reproduction

```bash
# stdout only: the version text appears even though a test could not capture it
LOKI_URL=http://127.0.0.1:3199 /tmp/lokimcp-findings version 2>/dev/null
```

Pinned on the merged branch by `cmd/version_test.go`
(`TestVersionWritesToProcessStdout`), which asserts that a buffer installed with
`SetOut` stays empty while the real stdout receives the text.

### Expected vs actual

- **Expected:** `SetOut(buf)` captures `loki-mcp <version>`.
- **Actual:** `buf` is empty; the text lands on file descriptor 1 regardless.

### Suggested fix (not implemented)

`fmt.Fprintln(cmd.OutOrStdout(), "loki-mcp", Version)`.

---

## 14. The compose `log-generator` output never reaches Loki

**Severity:** Low
**Location:** `docker-compose.yml:61-74` (`log-generator`),
`docker/promtail/config.yml:11-18`
**Reproduced:** *verified by reading the two config files only — docker,
docker-compose and make are not installed in this environment.*

### What happens

The compose stack ships a `log-generator` container that prints an `INFO:` line
every five seconds (and an occasional `ERROR:`), evidently as sample data for
the Grafana/Loki walkthrough. None of it ever arrives in Loki, so a user who
brings the stack up and opens Grafana sees nothing from it.

### Why

`docker/promtail/config.yml` has exactly one scrape config:

```yaml
scrape_configs:
  - job_name: system
    static_configs:
      - targets: [localhost]
        labels:
          job: varlogs
          __path__: /var/log/*log
```

That collects host files matching `/var/log/*log`. `log-generator` writes to its
own **stdout**, which Docker stores under
`/var/lib/docker/containers/<id>/<id>-json.log` — a path promtail is neither
pointed at nor mounted for. The compose file does mount `/var/run/docker.sock`
into promtail, but the config declares no `docker_sd_configs`, so the socket is
unused.

### Expected vs actual

- **Expected:** a `{job="varlogs"}` (or container-derived) stream in Grafana
  containing the generator's `INFO:`/`ERROR:` lines.
- **Actual:** only whatever the host happens to have in `/var/log/*log`; the
  generator's output is unreachable.

The other documented path is unaffected: `./docker/insert-loki-logs.sh` `POST`s
straight to `/loki/api/v1/push` and does not involve promtail.

### Suggested fix (not implemented)

Add a `docker_sd_configs` scrape job to `docker/promtail/config.yml` (the socket
is already mounted), or drop the `log-generator` service and rely on
`insert-loki-logs.sh`.
