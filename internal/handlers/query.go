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

// HandleQuery serves a loki_query tool call: it binds the request arguments
// into LokiParams, ANDs the configured query filter into the client's LogQL,
// runs a range query, post-processes the entries and renders them in the
// requested format.
//
// Every failure is returned as a Go error with a nil result, never as an
// mcp.CallToolResult with IsError set; mcp-go converts it. The failures are:
// unbindable arguments, head and tail both positive (they are mutually
// exclusive), an unmergeable or empty query when a filter is configured, an
// unparseable start or end, the Loki call itself, and an unsupported format.
//
// What the configuration changes:
//
//   - Empty query filter (Config.QueryFilter nil or empty): the client's query
//     string is sent to Loki byte for byte, without even being parsed. A
//     non-empty filter forces a parse, so with a filter configured a
//     syntactically invalid query fails here rather than at Loki, and an empty
//     query is rejected outright.
//   - Nil detector (secret filtering off): no line is scanned. If the call
//     also passes no filter, head or tail, post-processing is skipped
//     entirely and the response keeps Loki's own newest-first ordering and
//     stream grouping. Otherwise — with a detector, or with any of filter,
//     head or tail — entries are flattened, re-sorted and regrouped, but the
//     sort is newest-first too and the regrouped streams are ordered by their
//     label-set string, so post-processing does not change the ordering a
//     client sees.
//
// When lines are withheld the count is reported twice over, by two channels
// that do not overlap: appended to the rendered text for raw and text, and
// added to the response's Warnings array for json. Neither format shows the
// other's copy, and raw and text drop Loki's genuine warnings entirely
// (FINDINGS.md §12).
//
// ctx bounds the Loki request and is polled by the secret scan; a cancelled
// scan yields the lines checked so far rather than an error.
func (h *Handlers) HandleQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params LokiParams
	if err := request.BindArguments(&params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	params.applyDefaults()

	if params.Head > 0 && params.Tail > 0 {
		return nil, fmt.Errorf("head and tail parameters are mutually exclusive")
	}

	if err := h.applyQueryFilter(&params); err != nil {
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
	secretSummary := h.applyPostProcessing(ctx, result, &params)

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
func (h *Handlers) applyQueryFilter(params *LokiParams) error {
	if len(h.cfg.QueryFilter) == 0 {
		return nil
	}

	mergedQuery, err := MergeQueryWithFilter(params.Query, h.cfg.QueryFilter)
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
func (h *Handlers) applyPostProcessing(ctx context.Context, result *loghttp.QueryResponse, params *LokiParams) SecretFilterSummary {
	if params.Filter == "" && params.Head <= 0 && params.Tail <= 0 && h.cfg.Detector == nil {
		return SecretFilterSummary{}
	}

	streams, ok := result.Data.Result.(loghttp.Streams)
	if !ok || len(streams) == 0 {
		return SecretFilterSummary{}
	}

	processed, summary := h.postProcessStreams(ctx, streams, params)
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
func (h *Handlers) postProcessStreams(ctx context.Context, streams loghttp.Streams, params *LokiParams) (loghttp.Streams, SecretFilterSummary) {
	entries := collectAndSortEntries(streams)

	if params.Filter != "" {
		entries = filterEntries(entries, params.Filter, params.FilterCaseSensitive)
	}

	// Omit any lines that contain secrets before output limiting.
	entries, summary := filterSecretEntries(ctx, h.cfg.Detector, entries)

	if params.Head > 0 || params.Tail > 0 {
		entries = applyHeadTail(entries, int(params.Head), int(params.Tail))
	}

	return entriesToStreams(entries), summary
}
