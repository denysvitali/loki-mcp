package server

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/telemetry"
)

// New returns an MCP server with exactly the three Loki tools registered —
// loki_query, loki_label_names and loki_label_values — every one of them bound
// to a single handler set built from cfg and wrapped in the telemetry
// middleware, so every tool call is traced and counted.
//
// version is advertised to clients as the server version and is otherwise
// unused; the zero handlers.Config is accepted and means no query filter and
// no secret filtering.
//
// New only builds: it opens no listener, starts no goroutine and contacts
// neither Loki nor any collector, so it is safe to call in a test. The
// returned server is not yet serving — pass it to a transport, or use RunStdio
// or RunHTTP, which call New themselves.
func New(version string, cfg handlers.Config) *server.MCPServer {
	s := server.NewMCPServer(
		"Loki MCP Server",
		version,
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
		server.WithToolHandlerMiddleware(telemetry.ToolHandlerMiddleware()),
	)

	h := handlers.New(cfg)

	s.AddTool(h.QueryTool(), h.HandleQuery)
	s.AddTool(h.LabelNamesTool(), h.HandleLabelNames)
	s.AddTool(h.LabelValuesTool(), h.HandleLabelValues)

	return s
}
