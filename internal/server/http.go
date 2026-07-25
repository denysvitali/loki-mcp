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

// HTTPConfig holds configuration for the HTTP server
type HTTPConfig struct {
	Host       string
	Port       string
	DisableSSE bool
}

const defaultShutdownTimeout = 5 * time.Second

// RunHTTP starts the MCP server in HTTP mode. It returns once the server has
// stopped, either because SIGINT/SIGTERM arrived or because it failed.
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
	// ctx is already done here (it is what woke the select), so shutdownCtx
	// inherits that cancellation and defaultShutdownTimeout is an upper bound
	// only. This matches the pre-refactor behaviour, which derived the
	// shutdown context from a context it had just cancelled.
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, defaultShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logging.Errorf("HTTP server shutdown error: %v", err)
		return err
	}

	logging.Info("HTTP server stopped gracefully")
	return nil
}
