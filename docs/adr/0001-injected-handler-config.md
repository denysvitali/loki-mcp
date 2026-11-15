# 1. Inject handler configuration instead of package globals

- **Status:** Accepted
- **Date:** 2026-07-25

## Context

`internal/handlers` held its configuration in two mutable package-level
variables: `filterConfig` (the parsed LogQL stream selector) behind
`filterMu sync.RWMutex`, and `secretFilterConfig` (the betterleaks detector)
behind `secretFilterMu sync.RWMutex`. Both were written once at startup by
`Initialize*Config` and read on every tool call through `Get*Config`.

Two problems followed. Handler behaviour depended on hidden global state:
reading `HandleLokiQuery` told you nothing about whether a query filter was
active. And the globals leaked into the public surface — `ResetFilterConfig` and
`ResetSecretFilterConfig` were exported solely so tests could undo each other's
writes, and a test that forgot to reset silently changed the next one.

## Decision

Replace both globals with an immutable `handlers.Config` holding the parsed
matchers and the detector. `handlers.NewConfig(rawFilter, secretFilter)` builds
it once; `cmd/root.go` calls that from `buildHandlerConfig()` and passes the
value through `server.New` into `handlers.New(cfg)`. The tool builders and
handlers became methods on `*Handlers`, which owns the `Config` and never writes
to it. `filterMu`, `secretFilterMu` and all six `Initialize*`/`Get*`/`Reset*`
functions were deleted, with error wrapping kept byte-identical.

## Consequences

- Two of the codebase's four locks are gone. `Config` is read-only after
  construction, so every handler method is goroutine-safe without
  synchronisation, and the type says so.
- Handler behaviour is a function of its arguments; `h.cfg` is visible at the
  point of use.
- Tests construct the `Config` they want and need no reset hooks.
- **Honest limitation:** `cmd` keeps one package-level `handlerConfig
  handlers.Config`, written by `PersistentPreRunE` and read by the `stdio` and
  `http` `RunE`s. Cobra offers no channel from a persistent pre-run hook to a
  subcommand's `RunE` — the hook returns only an `error`, and `cmd.Context()`
  would trade one kind of hidden state for another. The global was pushed to the
  outermost layer, where it is written exactly once before any command runs,
  rather than eliminated.
