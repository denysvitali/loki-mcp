# 2. The telemetry shutdown registry keeps a mutex

- **Status:** Accepted
- **Date:** 2026-07-25

## Context

`internal/telemetry` had two pieces of shared mutable state: the four metric
instruments used on the tool-call hot path, behind `instrumentMu sync.RWMutex`,
and the list of provider shutdown functions, behind `shutdownMu sync.Mutex`.
The goal was to remove locks where a cheaper, clearer mechanism exists — not to
remove locks reflexively.

The two cases are not alike. The instruments are a read-mostly snapshot: written
during `Initialize` (twice — once with the no-op global meter, once after the
real meter provider is installed) and read on *every* tool call. The shutdown
registry is the opposite: written twice per process, read once, at exit.

## Decision

Convert the instruments to `atomic.Pointer[instruments]`. Bundling all four into
one struct means a reader sees either the pre-provider snapshot or the
post-provider one, never a mixture, and the hot path costs one atomic load
instead of an `RLock`/`RUnlock` pair.

**Keep `shutdownMu sync.Mutex` for the shutdown registry**, with its contract
documented on the declaration:

- It guards exactly one field, `shutdowns []func(context.Context) error`.
  Nothing else is read or written under it.
- Ordering: it is a leaf. No other lock is acquired while it is held, and it is
  never acquired while another lock is held.
- No I/O runs under it. `Shutdown` locks, copies the slice header out, sets
  `shutdowns = nil`, unlocks, and only then invokes the functions in reverse
  registration order. `Initialize` locks only to append.

## Consequences

- Both critical sections are a slice append and a pointer swap, with contention
  that is nonexistent in practice: the process has exactly one telemetry stack.
- The rejected alternative — an owner goroutine holding the registry behind a
  channel — would add a goroutine, its lifecycle and its own shutdown-ordering
  problem, to serialise a registry touched twice per process.
- The asymmetry is deliberate and explained in the source: hot path gets an
  atomic, cold path keeps the mutex that suits it.
