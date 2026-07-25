package server

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/logging"
	"github.com/kaiko-ai/loki-mcp/internal/telemetry"
)

// RunStdio serves the MCP tools over stdin and stdout and blocks until the
// client closes stdin, a shutdown signal arrives, or the transport fails. It
// returns nil on a clean end of input and the transport's error otherwise, and
// flushes telemetry on every path.
//
// Signal handling is mcp-go's, not this package's: server.ServeStdio installs
// its own SIGINT/SIGTERM handler, cancels the read context and returns, which
// is what lets the deferred telemetry flush run. RunStdio therefore starts no
// goroutine itself, and there is no other way to stop it — it takes no context
// and exposes no handle.
//
// stdout belongs to the JSON-RPC stream for the lifetime of this call. Nothing
// may write there: internal/logging keeps every record on stderr and
// telemetry's console exporters do the same, and a single stray line on stdout
// corrupts the protocol and disconnects the client.
func RunStdio(version string, cfg handlers.Config) error {
	logging.Info("Starting MCP server in stdio mode")
	defer shutdownTelemetry()

	s := New(version, cfg)

	if err := server.ServeStdio(s); err != nil {
		return err
	}

	return nil
}

func shutdownTelemetry() {
	// 5s is a best-effort flush window: long enough for a healthy collector,
	// short enough not to hang CLI shutdown on an unreachable one.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := telemetry.Shutdown(ctx); err != nil {
		logging.Errorf("OpenTelemetry shutdown error: %v", err)
	}
}
