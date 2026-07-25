package server

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/telemetry"
)

// New creates a new MCP server with all Loki tools registered
func New(version string, cfg handlers.Config) *server.MCPServer {
	s := server.NewMCPServer(
		"Loki MCP Server",
		version,
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
		server.WithToolHandlerMiddleware(telemetry.ToolHandlerMiddleware()),
	)

	h := handlers.New(cfg)

	// Add Loki query tool
	s.AddTool(h.QueryTool(), h.HandleQuery)

	// Add Loki label names tool
	s.AddTool(h.LabelNamesTool(), h.HandleLabelNames)

	// Add Loki label values tool
	s.AddTool(h.LabelValuesTool(), h.HandleLabelValues)

	return s
}
