package server

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/logging"
	"github.com/kaiko-ai/loki-mcp/internal/telemetry"
)

// RunStdio starts the MCP server in stdio mode
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := telemetry.Shutdown(ctx); err != nil {
		logging.Errorf("OpenTelemetry shutdown error: %v", err)
	}
}
