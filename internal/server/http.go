package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/logging"
)

// HTTPConfig is the listening configuration for RunHTTP. The zero value is
// accepted but binds ":" — every interface on a kernel-chosen port — so cmd
// always supplies both fields.
type HTTPConfig struct {
	// Host is the bind address, joined to Port as "Host:Port" with no
	// validation. "" binds all interfaces; cmd defaults it to "0.0.0.0".
	Host string
	// Port is the port as a string, because it arrives from a flag and is
	// never arithmetic. "" or "0" binds a kernel-chosen ephemeral port, but
	// nothing reports which one back, so it is only useful when the caller
	// does not need to connect.
	Port string
	// DisableSSE drops the legacy /sse and /mcp routes, leaving only /stream.
	// Requests to the dropped paths then 404. It does not affect /stream.
	DisableSSE bool
}

const defaultShutdownTimeout = 5 * time.Second

// RunHTTP serves the MCP tools over HTTP and blocks until SIGINT or SIGTERM
// arrives, or until the listener fails. There is no other way to stop it: it
// takes no context and exposes no handle, so a caller that needs to stop it
// must signal the process.
//
// It returns nil on a clean signalled shutdown and an error otherwise: the
// wrapped listen failure if the server could not start or died, or whatever
// http.Server.Shutdown reported. RunHTTP always flushes telemetry before
// returning, on every path including the error ones.
//
// Shutdown is a real graceful drain: in-flight requests get up to
// defaultShutdownTimeout (5s) to finish before the process gives up, and a
// signalled shutdown that completes inside that budget returns nil. Only a
// drain that genuinely outlives the budget returns an error, and it is
// context.DeadlineExceeded.
//
// The deferred calls run last-in-first-out: stop() detaches the signal
// handler (and ends the goroutine signal.NotifyContext owns) before
// shutdownTelemetry() flushes telemetry, so telemetry shutdown stays last.
func RunHTTP(version string, cfg HTTPConfig, handlerCfg handlers.Config) error {
	defer shutdownTelemetry()

	s := New(version, handlerCfg)

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Handler: newMux(s, cfg),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return serve(ctx, httpServer, cfg)
}

// newMux wires the HTTP endpoints for s: the legacy SSE endpoints unless
// cfg.DisableSSE, plus the Streamable HTTP endpoint.
func newMux(s *server.MCPServer, cfg HTTPConfig) http.Handler {
	mux := http.NewServeMux()

	// Register legacy SSE endpoints unless disabled
	if !cfg.DisableSSE {
		sseServer := server.NewSSEServer(s,
			server.WithSSEEndpoint("/sse"),
			server.WithMessageEndpoint("/mcp"),
		)
		mux.Handle("/sse", sseServer)
		mux.Handle("/mcp", sseServer)
	}

	// Register Streamable HTTP endpoint
	streamableServer := server.NewStreamableHTTPServer(s)
	mux.Handle("/stream", streamableServer)

	return mux
}

// serve runs srv until ctx is done (shutdown signal) or the server fails,
// then shuts it down gracefully.
//
// Goroutine ownership: serve owns exactly one goroutine, the one running
// srv.ListenAndServe, and always joins it before returning (the deferred
// receive on listenDone). That goroutine is guaranteed to exit because:
//   - shutdown path: srv.Shutdown closes the listeners, which makes
//     ListenAndServe return http.ErrServerClosed. Shutdown closes the
//     listeners even when it later reports a deadline error, so the join
//     holds on the Shutdown-error path too.
//   - error path: ListenAndServe has already returned by the time its error
//     is observed, and errChan is buffered, so the send never blocks.
func serve(ctx context.Context, srv *http.Server, cfg HTTPConfig) error {
	errChan := make(chan error, 1)
	listenDone := make(chan struct{})

	go func() {
		defer close(listenDone)

		logging.Infof("Starting MCP HTTP server on http://%s", srv.Addr)
		if !cfg.DisableSSE {
			logging.Infof("SSE Endpoint (legacy): http://%s/sse", srv.Addr)
			logging.Infof("SSE Message Endpoint: http://%s/mcp", srv.Addr)
		}
		logging.Infof("Streamable HTTP Endpoint: http://%s/stream", srv.Addr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()
	defer func() { <-listenDone }()

	// Wait for shutdown signal or server error
	select {
	case <-ctx.Done():
		logging.Info("Shutting down HTTP server...")
	case err := <-errChan:
		return fmt.Errorf("HTTP server error: %w", err)
	}

	// Gracefully shutdown HTTP server.
	//
	// The parent is context.Background(), not ctx: ctx is already done here
	// (being done is what woke the select), so deriving from it would hand
	// Shutdown a context that is cancelled the instant it is created and the
	// grace period would never apply. Rooting the timeout at Background is
	// what makes defaultShutdownTimeout a real budget rather than dead code.
	//
	// Consequence for long-lived streams: a client holding /sse open across
	// the signal never lets its connection go idle, so Shutdown waits out the
	// full 5s and then returns context.DeadlineExceeded. That is the intended
	// trade — bounded drain, then give up — not a bug.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer shutdownCancel()

	//nolint:contextcheck // not inheriting ctx is the whole point: ctx is done, so a derived shutdown context would expire before Shutdown ever polled it (FINDINGS.md §7).
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logging.Errorf("HTTP server shutdown error: %v", err)
		return err
	}

	logging.Info("HTTP server stopped gracefully")
	return nil
}
