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
