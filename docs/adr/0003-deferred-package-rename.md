# 3. Renaming `internal/handlers` to `internal/loki` is deferred

- **Status:** Accepted
- **Date:** 2026-07-25

## Context

`internal/handlers` is named after a layer, not a domain. That was defensible
when it held MCP tool handlers and little else. After the split it contains
`params.go` (binding, defaults, time ranges), `tools.go` (tool schemas),
`query.go` and `labels.go` (the only true handlers), `entries.go` (sorting,
keyword filter, head/tail), `format.go` (the `Format` type and three
renderers), `filter.go` (LogQL AST parsing and matcher injection),
`loki_http.go` (an HTTP client for Loki's label endpoints) and
`secret_filter.go` / `config.go` (secret scanning and configuration).

LogQL parsing, secret scanning, HTTP access and formatting are not "handlers".
Every one of them is *Loki*. `internal/loki` would describe the contents, and
the layer distinction would be carried by file names, where it belongs.

## Decision

Do not rename in this pass. Record the intent here instead.

The blocking reason is concrete: a parallel branch is mid-migration to a
different MCP SDK, and its changes land in exactly this package. A rename
rewrites the import path in every file plus every import site, so Git would see
the migration's edits and this rename as changes to different files. The merge
would become a manual re-application of the SDK migration on top of moved
files — pure cost, no behavioural benefit, and a real chance of silently
dropping an edit.

## Consequences

- The package keeps an under-descriptive name for one more cycle. The package
  doc should state what belongs here and what does not, so the gap is at least
  documented at the point of confusion.
- Once the SDK migration has merged, the rename can be a **single commit
  containing nothing but `git mv internal/handlers internal/loki` and the
  mechanical import-path and `package` clause updates**. No logic change may
  ride along; it must be reviewable from the diffstat.
- Every identifier involved is under `internal/`, so this is not a breaking
  change for any consumer. Tool names, schemas, flags and environment variables
  are unaffected — deferring costs nothing but the readability of a directory
  name.
