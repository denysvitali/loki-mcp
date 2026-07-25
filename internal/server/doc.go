// Package server builds the MCP server and runs it over one of the two
// supported transports.
//
// # What belongs here
//
//   - server.go — New, which constructs the mcp-go server, installs the
//     telemetry middleware and registers the three Loki tools.
//   - stdio.go — RunStdio, plus the shared telemetry flush.
//   - http.go — HTTPConfig, RunHTTP, and its two helpers: newMux (routing)
//     and serve (listen, wait for a signal, shut down).
//
// # What does not belong here
//
// Tool implementations and anything that knows what a LogQL query is: that is
// internal/handlers, which this package only wires up. Flag and environment
// parsing is cmd's job; both Run* functions take an already-built
// handlers.Config and never read configuration themselves.
//
// # Concurrency and goroutine ownership
//
// The package contains exactly one `go` statement, in serve. serve owns that
// goroutine — it runs srv.ListenAndServe — and always joins it before
// returning, via a deferred receive on listenDone. Both exit paths are
// guaranteed: on shutdown srv.Shutdown closes the listeners so ListenAndServe
// returns http.ErrServerClosed, and on failure ListenAndServe has already
// returned before its error is observed on a buffered channel. main_test.go
// enforces this with goleak.VerifyTestMain over the whole package.
//
// RunHTTP additionally holds the goroutine signal.NotifyContext starts; the
// deferred stop() releases it. RunStdio starts no goroutine of its own.
//
// New holds no state, so the *server.MCPServer it returns may be served by one
// transport at a time from any goroutine; the handlers it registers are safe
// for concurrent calls (see internal/handlers).
//
// # Shutdown is a bounded graceful drain
//
// serve roots its shutdown context at context.Background(), not at the
// already-done signal context, so defaultShutdownTimeout (5s) is a real budget:
// in-flight requests are drained rather than dropped, and a signalled shutdown
// that finishes inside the budget makes RunHTTP return nil. A drain that
// outlives the budget — the obvious case being a client that holds /sse open
// across the signal, since a streaming connection never goes idle — returns
// context.DeadlineExceeded after the full 5s. That is the intended trade.
package server
