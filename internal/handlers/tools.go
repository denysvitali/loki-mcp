package handlers

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/viper"
)

// QueryTool returns the loki_query tool schema, to be registered against
// (*Handlers).HandleQuery. `query` is the only required argument; every other
// argument is optional and falls back to the server's configuration.
//
// The schema is a snapshot, not a live view. The advertised default for `url`
// is read from viper at the moment QueryTool is called, so it reflects
// --url/LOKI_URL as resolved at registration time; changing them afterwards
// does not update an already-registered tool. Nothing else in the schema
// depends on configuration, and the configured query filter is deliberately
// invisible here — clients cannot see or opt out of it.
//
// QueryTool ignores h and never fails; it is pure with respect to Config.
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

// LabelNamesTool returns the loki_label_names tool schema, to be registered
// against (*Handlers).HandleLabelNames. It has no required arguments.
//
// The schema advertises no head, tail, filter or limit: label results are
// returned whole and are never post-processed or secret-scanned. As in
// QueryTool the `url` default is snapshotted from viper at call time, and
// LabelNamesTool ignores h.
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

// LabelValuesTool returns the loki_label_values tool schema, to be registered
// against (*Handlers).HandleLabelValues. `label` is the only required
// argument.
//
// Otherwise identical to LabelNamesTool: no post-processing arguments, the
// `url` default snapshotted from viper at call time, and h unused.
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
