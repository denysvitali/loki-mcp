package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/grafana/loki/v3/pkg/logcli/client"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/viper"

	"github.com/kaiko-ai/loki-mcp/internal/telemetry"
)

// PingLoki verifies connectivity and authentication to Loki by making a
// lightweight ListLabelNames call via the SDK client.
func PingLoki(params *LokiParams) error {
	lokiClient := newLokiClient(params)
	now := time.Now()
	_, err := lokiClient.ListLabelNames(true, now.Add(-time.Minute), now)
	if err != nil {
		return fmt.Errorf("failed to reach Loki at %s: %w", params.URL, err)
	}
	return nil
}

// Default values
const (
	DefaultHTTPTimeout = 30 * time.Second
	DefaultQueryLimit  = 100
	DefaultLookback    = 1 * time.Hour
)

// LokiParams contains common parameters for Loki tool requests
type LokiParams struct {
	URL                 string  `json:"url"`
	Username            string  `json:"username"`
	Password            string  `json:"password"`
	Token               string  `json:"token"`
	Org                 string  `json:"org"`
	Start               string  `json:"start"`
	End                 string  `json:"end"`
	Format              string  `json:"format"`
	Limit               float64 `json:"limit"`                 // JSON numbers deserialize as float64
	Query               string  `json:"query"`                 // for loki_query
	Label               string  `json:"label"`                 // for loki_label_values
	Head                float64 `json:"head"`                  // Return only the first N entries (oldest)
	Tail                float64 `json:"tail"`                  // Return only the last N entries (newest)
	Filter              string  `json:"filter"`                // Filter log lines by substring
	FilterCaseSensitive bool    `json:"filter_case_sensitive"` // Case sensitivity for filter (default: false)
}

// applyDefaults fills in missing values from viper-bound config
func (p *LokiParams) applyDefaults() {
	if p.URL == "" {
		p.URL = viper.GetString("url")
	}
	if p.Username == "" {
		p.Username = viper.GetString("username")
	}
	if p.Password == "" {
		p.Password = viper.GetString("password")
	}
	if p.Token == "" {
		p.Token = viper.GetString("token")
	}
	if p.Org == "" {
		p.Org = viper.GetString("org-id")
	}
	if p.Format == "" {
		p.Format = "raw"
	}
}

// newLokiClient creates a Loki client from the given parameters
func newLokiClient(params *LokiParams) *client.DefaultClient {
	return &client.DefaultClient{
		Address:     params.URL,
		Username:    params.Username,
		Password:    params.Password,
		BearerToken: params.Token,
		OrgID:       params.Org,
	}
}

// NewLokiQueryTool creates and returns a tool for querying Grafana Loki
func NewLokiQueryTool() mcp.Tool {
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

// HandleLokiQuery handles Loki query tool requests
func HandleLokiQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params LokiParams
	if err := request.BindArguments(&params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	params.applyDefaults()

	// Validate head/tail mutual exclusivity
	if params.Head > 0 && params.Tail > 0 {
		return nil, fmt.Errorf("head and tail parameters are mutually exclusive")
	}

	// Apply query filter if configured
	if cfg := GetFilterConfig(); cfg != nil {
		mergedQuery, err := MergeQueryWithFilter(params.Query, cfg.Matchers)
		if err != nil {
			return nil, fmt.Errorf("failed to apply query filter: %w", err)
		}
		params.Query = mergedQuery
	}

	// Set defaults for optional time parameters
	start := time.Now().Add(-DefaultLookback)
	end := time.Now()
	limit := DefaultQueryLimit

	// Override defaults if parameters are provided
	if params.Start != "" {
		startTime, err := parseTime(params.Start)
		if err != nil {
			return nil, fmt.Errorf("invalid start time: %w", err)
		}
		start = startTime
	}

	if params.End != "" {
		endTime, err := parseTime(params.End)
		if err != nil {
			return nil, fmt.Errorf("invalid end time: %w", err)
		}
		end = endTime
	}

	if params.Limit > 0 {
		limit = int(params.Limit)
	}

	// Create Loki client and execute query
	lokiClient := newLokiClient(&params)

	_, span := telemetry.StartSpan(ctx, "loki.query_range")
	result, err := lokiClient.QueryRange(
		params.Query,
		limit,
		start,
		end,
		logproto.BACKWARD, // newest first
		0,                 // step (0 = auto)
		0,                 // interval (0 = auto)
		true,              // quiet (no progress output)
	)
	span.End()
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}

	var secretSummary SecretFilterSummary

	// Apply post-processing filters if any are specified. Secret filtering runs
	// before formatting so raw, text, and JSON output all use the sanitized data.
	if params.Filter != "" || params.Head > 0 || params.Tail > 0 || secretFilteringEnabled() {
		streams, ok := result.Data.Result.(loghttp.Streams)
		if ok && len(streams) > 0 {
			// Collect and sort all entries by timestamp
			entries := collectAndSortEntries(streams)

			// Apply keyword filter
			if params.Filter != "" {
				entries = filterEntries(entries, params.Filter, params.FilterCaseSensitive)
			}

			// Omit any lines that contain secrets before output limiting.
			entries, secretSummary = filterSecretEntries(ctx, entries)
			if secretSummary.LinesOmitted > 0 {
				telemetry.RecordSecretFiltering(ctx, secretSummary.Findings, secretSummary.LinesOmitted)
				result.Warnings = append(result.Warnings, formatSecretOmissionNote(secretSummary.LinesOmitted))
			}

			// Apply head/tail
			if params.Head > 0 || params.Tail > 0 {
				entries = applyHeadTail(entries, int(params.Head), int(params.Tail))
			}

			// Convert back to streams format
			result.Data.Result = entriesToStreams(entries)
		}
	}

	// Format results
	formattedResult, err := formatLokiResults(result, params.Format)
	if err != nil {
		return nil, fmt.Errorf("failed to format results: %w", err)
	}
	if secretSummary.LinesOmitted > 0 && params.Format != "json" {
		formattedResult += "\n" + formatSecretOmissionNote(secretSummary.LinesOmitted)
	}

	return mcp.NewToolResultText(formattedResult), nil
}

func secretFilteringEnabled() bool {
	cfg := GetSecretFilterConfig()
	return cfg != nil && cfg.Enabled && cfg.detector != nil
}

// parseTime parses a time string in various formats
func parseTime(timeStr string) (time.Time, error) {
	// Handle "now" keyword
	if timeStr == "now" {
		return time.Now(), nil
	}

	// Handle relative time strings like "-1h", "-30m"
	if len(timeStr) > 0 && timeStr[0] == '-' {
		duration, err := time.ParseDuration(timeStr)
		if err == nil {
			return time.Now().Add(duration), nil
		}
	}

	// Try parsing as RFC3339
	t, err := time.Parse(time.RFC3339, timeStr)
	if err == nil {
		return t, nil
	}

	// Try other common formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		t, err := time.Parse(format, timeStr)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported time format: %s", timeStr)
}

// logEntry represents a single log entry with its associated labels for sorting and filtering
type logEntry struct {
	Timestamp time.Time
	Line      string
	Labels    loghttp.LabelSet
}

// collectAndSortEntries merges entries from all streams and sorts them by timestamp (oldest first)
func collectAndSortEntries(streams loghttp.Streams) []logEntry {
	var entries []logEntry
	for _, stream := range streams {
		for _, entry := range stream.Entries {
			entries = append(entries, logEntry{
				Timestamp: entry.Timestamp,
				Line:      entry.Line,
				Labels:    stream.Labels,
			})
		}
	}

	// Sort by timestamp ascending (oldest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	return entries
}

// filterEntries filters log entries by substring match
func filterEntries(entries []logEntry, filter string, caseSensitive bool) []logEntry {
	var filtered []logEntry
	searchFilter := filter
	if !caseSensitive {
		searchFilter = strings.ToLower(filter)
	}

	for _, entry := range entries {
		line := entry.Line
		if !caseSensitive {
			line = strings.ToLower(line)
		}
		if strings.Contains(line, searchFilter) {
			filtered = append(filtered, entry)
		}
	}

	return filtered
}

// applyHeadTail returns the first N entries (head) or last N entries (tail)
func applyHeadTail(entries []logEntry, head, tail int) []logEntry {
	if len(entries) == 0 {
		return entries
	}

	if head > 0 {
		if head >= len(entries) {
			return entries
		}
		return entries[:head]
	}

	if tail > 0 {
		if tail >= len(entries) {
			return entries
		}
		return entries[len(entries)-tail:]
	}

	return entries
}

// entriesToStreams converts a slice of logEntry back to loghttp.Streams format
// Entries are grouped by their labels
func entriesToStreams(entries []logEntry) loghttp.Streams {
	if len(entries) == 0 {
		return loghttp.Streams{}
	}

	// Group entries by labels
	streamMap := make(map[string]*loghttp.Stream)
	for _, entry := range entries {
		key := entry.Labels.String()
		if stream, exists := streamMap[key]; exists {
			stream.Entries = append(stream.Entries, loghttp.Entry{
				Timestamp: entry.Timestamp,
				Line:      entry.Line,
			})
		} else {
			streamMap[key] = &loghttp.Stream{
				Labels: entry.Labels,
				Entries: []loghttp.Entry{
					{
						Timestamp: entry.Timestamp,
						Line:      entry.Line,
					},
				},
			}
		}
	}

	// Convert map to slice
	var streams loghttp.Streams
	for _, stream := range streamMap {
		streams = append(streams, *stream)
	}

	return streams
}

// formatLokiResults formats the Loki query results into a readable string
func formatLokiResults(result *loghttp.QueryResponse, format string) (string, error) {
	// Extract streams from the result
	streams, ok := result.Data.Result.(loghttp.Streams)
	if !ok {
		// Handle case where result is not streams (could be matrix/vector for metric queries)
		switch format {
		case "json":
			jsonBytes, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return "", fmt.Errorf("failed to marshal JSON: %w", err)
			}
			return string(jsonBytes), nil
		default:
			return fmt.Sprintf("Query returned %s result type", result.Data.ResultType), nil
		}
	}

	if len(streams) == 0 {
		switch format {
		case "json":
			if len(result.Warnings) > 0 {
				jsonBytes, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return "", fmt.Errorf("failed to marshal JSON: %w", err)
				}
				return string(jsonBytes), nil
			}
			return "{\"message\": \"No logs found matching the query\"}", nil
		default:
			return "No logs found matching the query", nil
		}
	}

	switch format {
	case "json":
		// Return raw JSON response
		jsonBytes, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal JSON: %w", err)
		}
		return string(jsonBytes), nil

	case "raw":
		// Return raw log lines with timestamps and labels in simple format
		var sb strings.Builder
		for _, stream := range streams {
			// Build labels string
			labels := stream.Labels.String()
			if labels != "{}" {
				labels = labels + " "
			} else {
				labels = ""
			}

			for _, entry := range stream.Entries {
				timestamp := entry.Timestamp.Format(time.RFC3339)
				fmt.Fprintf(&sb, "%s %s%s\n", timestamp, labels, entry.Line)
			}
		}
		return sb.String(), nil

	case "text":
		// Return formatted text with timestamps and stream info (original behavior)
		var sb strings.Builder
		fmt.Fprintf(&sb, "Found %d streams:\n\n", len(streams))

		for i, stream := range streams {
			// Format stream labels
			fmt.Fprintf(&sb, "Stream %s %d:\n", stream.Labels.String(), i+1)

			// Format log entries
			for _, entry := range stream.Entries {
				fmt.Fprintf(&sb, "[%s] %s\n", entry.Timestamp.Format(time.RFC3339), entry.Line)
			}
			sb.WriteString("\n")
		}
		return sb.String(), nil

	default:
		return "", fmt.Errorf("unsupported format: %s. Supported formats: raw, json, text", format)
	}
}

// NewLokiLabelNamesTool creates and returns a tool for getting all label names from Grafana Loki
func NewLokiLabelNamesTool() mcp.Tool {
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

// NewLokiLabelValuesTool creates and returns a tool for getting values for a specific label from Grafana Loki
func NewLokiLabelValuesTool() mcp.Tool {
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

// HandleLokiLabelNames handles Loki label names tool requests
func HandleLokiLabelNames(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params LokiParams
	if err := request.BindArguments(&params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	params.applyDefaults()

	// Set defaults for optional time parameters
	start := time.Now().Add(-DefaultLookback)
	end := time.Now()

	// Override defaults if parameters are provided
	if params.Start != "" {
		startTime, err := parseTime(params.Start)
		if err != nil {
			return nil, fmt.Errorf("invalid start time: %w", err)
		}
		start = startTime
	}

	if params.End != "" {
		endTime, err := parseTime(params.End)
		if err != nil {
			return nil, fmt.Errorf("invalid end time: %w", err)
		}
		end = endTime
	}

	var result *loghttp.LabelResponse
	var err error

	// Use HTTP client with query filter if configured
	if cfg := GetFilterConfig(); cfg != nil {
		httpClient := NewLokiHTTPClient(&params)
		filterQuery := BuildFilterSelector(cfg.Matchers)
		result, err = httpClient.ListLabelNamesWithQuery(filterQuery, start, end)
	} else {
		lokiClient := newLokiClient(&params)
		result, err = lokiClient.ListLabelNames(true, start, end)
	}
	if err != nil {
		return nil, fmt.Errorf("labels query execution failed: %w", err)
	}

	// Format results
	formattedResult, err := formatLokiLabelsResults(result, params.Format)
	if err != nil {
		return nil, fmt.Errorf("failed to format results: %w", err)
	}

	return mcp.NewToolResultText(formattedResult), nil
}

// HandleLokiLabelValues handles Loki label values tool requests
func HandleLokiLabelValues(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params LokiParams
	if err := request.BindArguments(&params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	params.applyDefaults()

	// Set defaults for optional time parameters
	start := time.Now().Add(-DefaultLookback)
	end := time.Now()

	// Override defaults if parameters are provided
	if params.Start != "" {
		startTime, err := parseTime(params.Start)
		if err != nil {
			return nil, fmt.Errorf("invalid start time: %w", err)
		}
		start = startTime
	}

	if params.End != "" {
		endTime, err := parseTime(params.End)
		if err != nil {
			return nil, fmt.Errorf("invalid end time: %w", err)
		}
		end = endTime
	}

	var result *loghttp.LabelResponse
	var err error

	// Use HTTP client with query filter if configured
	if cfg := GetFilterConfig(); cfg != nil {
		httpClient := NewLokiHTTPClient(&params)
		filterQuery := BuildFilterSelector(cfg.Matchers)
		result, err = httpClient.ListLabelValuesWithQuery(params.Label, filterQuery, start, end)
	} else {
		lokiClient := newLokiClient(&params)
		result, err = lokiClient.ListLabelValues(params.Label, true, start, end)
	}
	if err != nil {
		return nil, fmt.Errorf("label values query execution failed: %w", err)
	}

	// Format results
	formattedResult, err := formatLokiLabelValuesResults(params.Label, result, params.Format)
	if err != nil {
		return nil, fmt.Errorf("failed to format results: %w", err)
	}

	return mcp.NewToolResultText(formattedResult), nil
}

// formatLokiLabelsResults formats the Loki labels results into a readable string
func formatLokiLabelsResults(result *loghttp.LabelResponse, format string) (string, error) {
	if len(result.Data) == 0 {
		switch format {
		case "json":
			return "{\"message\": \"No labels found\"}", nil
		default:
			return "No labels found", nil
		}
	}

	switch format {
	case "json":
		// Return raw JSON response
		jsonBytes, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal JSON: %w", err)
		}
		return string(jsonBytes), nil

	case "raw":
		// Return raw label names only, one per line
		var sb strings.Builder
		for _, label := range result.Data {
			sb.WriteString(label)
			sb.WriteString("\n")
		}
		return sb.String(), nil

	case "text":
		// Return formatted text with numbering (original behavior)
		var sb strings.Builder
		fmt.Fprintf(&sb, "Found %d labels:\n\n", len(result.Data))

		for i, label := range result.Data {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, label)
		}
		return sb.String(), nil

	default:
		return "", fmt.Errorf("unsupported format: %s. Supported formats: raw, json, text", format)
	}
}

// formatLokiLabelValuesResults formats the Loki label values results into a readable string
func formatLokiLabelValuesResults(labelName string, result *loghttp.LabelResponse, format string) (string, error) {
	if len(result.Data) == 0 {
		switch format {
		case "json":
			return fmt.Sprintf("{\"message\": \"No values found for label '%s'\"}", labelName), nil
		default:
			return fmt.Sprintf("No values found for label '%s'", labelName), nil
		}
	}

	switch format {
	case "json":
		// Return raw JSON response
		jsonBytes, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal JSON: %w", err)
		}
		return string(jsonBytes), nil

	case "raw":
		// Return raw label values only, one per line
		var sb strings.Builder
		for _, value := range result.Data {
			sb.WriteString(value)
			sb.WriteString("\n")
		}
		return sb.String(), nil

	case "text":
		// Return formatted text with numbering (original behavior)
		var sb strings.Builder
		fmt.Fprintf(&sb, "Found %d values for label '%s':\n\n", len(result.Data), labelName)

		for i, value := range result.Data {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, value)
		}
		return sb.String(), nil

	default:
		return "", fmt.Errorf("unsupported format: %s. Supported formats: raw, json, text", format)
	}
}
