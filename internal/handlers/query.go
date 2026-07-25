package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/kaiko-ai/loki-mcp/internal/telemetry"
)

// HandleLokiQuery handles Loki query tool requests
func HandleLokiQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params LokiParams
	if err := request.BindArguments(&params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	params.applyDefaults()

	if params.Head > 0 && params.Tail > 0 {
		return nil, fmt.Errorf("head and tail parameters are mutually exclusive")
	}

	if err := applyQueryFilter(&params); err != nil {
		return nil, err
	}

	start, end, err := resolveTimeRange(&params)
	if err != nil {
		return nil, err
	}

	result, err := runQueryRange(ctx, &params, start, end)
	if err != nil {
		return nil, err
	}

	// Secret filtering runs before formatting so raw, text and JSON output all
	// use the sanitized data.
	secretSummary := applyPostProcessing(ctx, result, &params)

	formattedResult, err := formatLokiResults(result, params.Format)
	if err != nil {
		return nil, fmt.Errorf("failed to format results: %w", err)
	}
	if secretSummary.LinesOmitted > 0 && Format(params.Format) != FormatJSON {
		formattedResult += "\n" + formatSecretOmissionNote(secretSummary.LinesOmitted)
	}

	return mcp.NewToolResultText(formattedResult), nil
}

// applyQueryFilter ANDs the configured stream selector into the client query.
// It is a no-op when no filter is configured.
func applyQueryFilter(params *LokiParams) error {
	cfg := GetFilterConfig()
	if cfg == nil {
		return nil
	}

	mergedQuery, err := MergeQueryWithFilter(params.Query, cfg.Matchers)
	if err != nil {
		return fmt.Errorf("failed to apply query filter: %w", err)
	}
	params.Query = mergedQuery
	return nil
}

// runQueryRange executes the range query against Loki inside a span.
func runQueryRange(ctx context.Context, params *LokiParams, start, end time.Time) (*loghttp.QueryResponse, error) {
	limit := DefaultQueryLimit
	if params.Limit > 0 {
		// JSON numbers arrive as float64; fractional limits truncate.
		limit = int(params.Limit)
	}

	lokiClient := newLokiClient(params)

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
	return result, nil
}

// applyPostProcessing rewrites result in place with the filtered, secret-scanned
// and head/tail-limited entries, and returns a summary of what was omitted.
// Non-stream and empty results are left untouched.
func applyPostProcessing(ctx context.Context, result *loghttp.QueryResponse, params *LokiParams) SecretFilterSummary {
	if params.Filter == "" && params.Head <= 0 && params.Tail <= 0 && !secretFilteringEnabled() {
		return SecretFilterSummary{}
	}

	streams, ok := result.Data.Result.(loghttp.Streams)
	if !ok || len(streams) == 0 {
		return SecretFilterSummary{}
	}

	processed, summary := postProcessStreams(ctx, streams, params)
	result.Data.Result = processed

	if summary.LinesOmitted > 0 {
		telemetry.RecordSecretFiltering(ctx, summary.Findings, summary.LinesOmitted)
		result.Warnings = append(result.Warnings, formatSecretOmissionNote(summary.LinesOmitted))
	}

	return summary
}

// postProcessStreams flattens streams into a time-ordered entry list, applies
// the keyword filter, drops lines containing secrets, applies head/tail and
// regroups the survivors back into streams.
func postProcessStreams(ctx context.Context, streams loghttp.Streams, params *LokiParams) (loghttp.Streams, SecretFilterSummary) {
	entries := collectAndSortEntries(streams)

	if params.Filter != "" {
		entries = filterEntries(entries, params.Filter, params.FilterCaseSensitive)
	}

	// Omit any lines that contain secrets before output limiting.
	entries, summary := filterSecretEntries(ctx, entries)

	if params.Head > 0 || params.Tail > 0 {
		entries = applyHeadTail(entries, int(params.Head), int(params.Tail))
	}

	return entriesToStreams(entries), summary
}

func secretFilteringEnabled() bool {
	cfg := GetSecretFilterConfig()
	return cfg != nil && cfg.Enabled && cfg.detector != nil
}
