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
		mcp.WithDescription("Run a LogQL query against Grafana Loki and return matching log entries. Results are post-processed (keyword filter, secret filtering, head/tail) and rendered in the requested format."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("LogQL log query string (for example: {app=\"api\"} |= \"error\")"),
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
			mcp.Description("Start of the query window. Accepts \"now\", negative durations like \"-1h\" or \"-30m\", RFC3339, or \"2006-01-02T15:04:05\" / \"2006-01-02 15:04:05\" / \"2006-01-02\" (parsed as UTC). Default: 1h ago."),
		),
		mcp.WithString("end",
			mcp.Description("End of the query window. Same formats as start. Default: now."),
		),
		mcp.WithString("direction",
			mcp.Description("Query direction sent to Loki: \"backward\" (newest first, default) or \"forward\" (oldest first)."),
			mcp.DefaultString("backward"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of log entries requested from Loki before local post-processing (default: 100). Use head/tail to limit after filtering."),
		),
		mcp.WithString("org",
			mcp.Description("Organization/tenant ID for the query, sent as X-Scope-OrgID (env: LOKI_ORG_ID)"),
		),
		mcp.WithString("format",
			mcp.Description("Output format: \"raw\" (one line per entry, default), \"json\" (Loki API-shaped streams response), or \"text\" (human-readable grouped listing)."),
			mcp.DefaultString("raw"),
		),
		mcp.WithNumber("head",
			mcp.Description("After filtering, keep only the N oldest matching entries (by timestamp) and render them newest-first. Cannot be used with tail."),
		),
		mcp.WithNumber("tail",
			mcp.Description("After filtering, keep only the N newest matching entries and render them newest-first. Cannot be used with head."),
		),
		mcp.WithString("filter",
			mcp.Description("Optional local substring filter applied to each log line before head/tail limiting."),
		),
		mcp.WithBoolean("filter_case_sensitive",
			mcp.Description("When true, the local substring filter is case-sensitive (default: false)."),
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
		mcp.WithDescription("List label names known to Grafana Loki within the given time range."),
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
			mcp.Description("Start of the query window. Accepts \"now\", negative durations like \"-1h\", RFC3339, or \"2006-01-02T15:04:05\" / \"2006-01-02 15:04:05\" / \"2006-01-02\" (parsed as UTC). Default: 1h ago."),
		),
		mcp.WithString("end",
			mcp.Description("End of the query window. Same formats as start. Default: now."),
		),
		mcp.WithString("org",
			mcp.Description("Organization/tenant ID for the query, sent as X-Scope-OrgID (env: LOKI_ORG_ID)"),
		),
		mcp.WithString("format",
			mcp.Description("Output format: \"raw\" (one label per line, default), \"json\", or \"text\" (numbered list)."),
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
		mcp.WithDescription("List values for a specific label in Grafana Loki within the given time range."),
		mcp.WithString("label",
			mcp.Required(),
			mcp.Description("Label name to retrieve values for (for example: \"app\", \"namespace\", \"job\")"),
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
			mcp.Description("Start of the query window. Accepts \"now\", negative durations like \"-1h\", RFC3339, or \"2006-01-02T15:04:05\" / \"2006-01-02 15:04:05\" / \"2006-01-02\" (parsed as UTC). Default: 1h ago."),
		),
		mcp.WithString("end",
			mcp.Description("End of the query window. Same formats as start. Default: now."),
		),
		mcp.WithString("org",
			mcp.Description("Organization/tenant ID for the query, sent as X-Scope-OrgID (env: LOKI_ORG_ID)"),
		),
		mcp.WithString("format",
			mcp.Description("Output format: \"raw\" (one value per line, default), \"json\", or \"text\" (numbered list)."),
			mcp.DefaultString("raw"),
		),
	)
}
