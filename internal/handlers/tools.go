package handlers

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/viper"
)

// QueryTool returns the loki_query tool definition.
func (h *Handlers) QueryTool() mcp.Tool {
	lokiURL := viper.GetString("url")

	return mcp.NewTool("loki_query",
		mcp.WithDescription("Run a query against Grafana Loki"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("LogQL query string"),
		),
		mcp.WithString("url",
			mcp.Description("Loki server URL (env: LOKI_URL)"),
			mcp.DefaultString(lokiURL),
		),
		mcp.WithString("username",
			mcp.Description("Username for basic authentication (env: LOKI_USERNAME)"),
		),
		mcp.WithString("password",
			mcp.Description("Password for basic authentication (env: LOKI_PASSWORD)"),
		),
		mcp.WithString("token",
			mcp.Description("Bearer token for authentication (env: LOKI_TOKEN)"),
		),
		mcp.WithString("start",
			mcp.Description("Start time for the query (default: 1h ago)"),
		),
		mcp.WithString("end",
			mcp.Description("End time for the query (default: now)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of entries to return (default: 100)"),
		),
		mcp.WithString("org",
			mcp.Description("Organization ID for the query (env: LOKI_ORG_ID)"),
		),
		mcp.WithString("format",
			mcp.Description("Output format: raw, json, or text (default: raw)"),
			mcp.DefaultString("raw"),
		),
		mcp.WithNumber("head",
			mcp.Description("Return only the first N log entries (oldest). Cannot be used with tail."),
		),
		mcp.WithNumber("tail",
			mcp.Description("Return only the last N log entries (newest). Cannot be used with head."),
		),
		mcp.WithString("filter",
			mcp.Description("Filter log lines to only those containing this substring"),
		),
		mcp.WithBoolean("filter_case_sensitive",
			mcp.Description("When true, filter matching is case-sensitive (default: false)"),
		),
	)
}

// LabelNamesTool returns the loki_label_names tool definition.
func (h *Handlers) LabelNamesTool() mcp.Tool {
	lokiURL := viper.GetString("url")

	return mcp.NewTool("loki_label_names",
		mcp.WithDescription("Get all label names from Grafana Loki"),
		mcp.WithString("url",
			mcp.Description("Loki server URL (env: LOKI_URL)"),
			mcp.DefaultString(lokiURL),
		),
		mcp.WithString("username",
			mcp.Description("Username for basic authentication (env: LOKI_USERNAME)"),
		),
		mcp.WithString("password",
			mcp.Description("Password for basic authentication (env: LOKI_PASSWORD)"),
		),
		mcp.WithString("token",
			mcp.Description("Bearer token for authentication (env: LOKI_TOKEN)"),
		),
		mcp.WithString("start",
			mcp.Description("Start time for the query (default: 1h ago)"),
		),
		mcp.WithString("end",
			mcp.Description("End time for the query (default: now)"),
		),
		mcp.WithString("org",
			mcp.Description("Organization ID for the query (env: LOKI_ORG_ID)"),
		),
		mcp.WithString("format",
			mcp.Description("Output format: raw, json, or text (default: raw)"),
			mcp.DefaultString("raw"),
		),
	)
}

// LabelValuesTool returns the loki_label_values tool definition.
func (h *Handlers) LabelValuesTool() mcp.Tool {
	lokiURL := viper.GetString("url")

	return mcp.NewTool("loki_label_values",
		mcp.WithDescription("Get all values for a specific label from Grafana Loki"),
		mcp.WithString("label",
			mcp.Required(),
			mcp.Description("Label name to get values for"),
		),
		mcp.WithString("url",
			mcp.Description("Loki server URL (env: LOKI_URL)"),
			mcp.DefaultString(lokiURL),
		),
		mcp.WithString("username",
			mcp.Description("Username for basic authentication (env: LOKI_USERNAME)"),
		),
		mcp.WithString("password",
			mcp.Description("Password for basic authentication (env: LOKI_PASSWORD)"),
		),
		mcp.WithString("token",
			mcp.Description("Bearer token for authentication (env: LOKI_TOKEN)"),
		),
		mcp.WithString("start",
			mcp.Description("Start time for the query (default: 1h ago)"),
		),
		mcp.WithString("end",
			mcp.Description("End time for the query (default: now)"),
		),
		mcp.WithString("org",
			mcp.Description("Organization ID for the query (env: LOKI_ORG_ID)"),
		),
		mcp.WithString("format",
			mcp.Description("Output format: raw, json, or text (default: raw)"),
			mcp.DefaultString("raw"),
		),
	)
}
