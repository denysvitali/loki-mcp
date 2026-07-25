package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/prometheus/prometheus/model/labels"
)

// labelFetcher queries Loki for a label response. filter is the configured query
// filter, or nil when queries are unrestricted.
type labelFetcher func(ctx context.Context, params *LokiParams, filter []*labels.Matcher, start, end time.Time) (*loghttp.LabelResponse, error)

// labelFormatter renders a label response using the client's requested format.
type labelFormatter func(params *LokiParams, result *loghttp.LabelResponse) (string, error)

// HandleLabelNames handles loki_label_names tool requests.
func (h *Handlers) HandleLabelNames(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return h.runLabelRequest(ctx, request, "labels query execution failed", fetchLabelNames,
		func(params *LokiParams, result *loghttp.LabelResponse) (string, error) {
			return formatLokiLabelsResults(result, params.Format)
		})
}

// HandleLabelValues handles loki_label_values tool requests.
func (h *Handlers) HandleLabelValues(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return h.runLabelRequest(ctx, request, "label values query execution failed", fetchLabelValues,
		func(params *LokiParams, result *loghttp.LabelResponse) (string, error) {
			return formatLokiLabelValuesResults(params.Label, result, params.Format)
		})
}

// runLabelRequest is the shared body of both label tools: bind arguments, resolve
// the time range, fetch and format. The tools differ only in the Loki call, the
// formatter, and the wording of the fetch failure.
func (h *Handlers) runLabelRequest(
	ctx context.Context,
	request mcp.CallToolRequest,
	fetchErrMsg string,
	fetch labelFetcher,
	format labelFormatter,
) (*mcp.CallToolResult, error) {
	var params LokiParams
	if err := request.BindArguments(&params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	params.applyDefaults()

	start, end, err := resolveTimeRange(&params)
	if err != nil {
		return nil, err
	}

	result, err := fetch(ctx, &params, h.cfg.QueryFilter, start, end)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fetchErrMsg, err)
	}

	formattedResult, err := format(&params, result)
	if err != nil {
		return nil, fmt.Errorf("failed to format results: %w", err)
	}

	return mcp.NewToolResultText(formattedResult), nil
}

// fetchLabelNames uses the HTTP client when a query filter is configured, because
// only it can pass the filter selector to Loki; otherwise it uses the SDK client.
func fetchLabelNames(ctx context.Context, params *LokiParams, filter []*labels.Matcher, start, end time.Time) (*loghttp.LabelResponse, error) {
	if len(filter) > 0 {
		return NewLokiHTTPClient(params).ListLabelNamesWithQuery(ctx, BuildFilterSelector(filter), start, end)
	}
	return newLokiClient(params).ListLabelNames(true, start, end)
}

// fetchLabelValues is fetchLabelNames for a single label's values.
func fetchLabelValues(ctx context.Context, params *LokiParams, filter []*labels.Matcher, start, end time.Time) (*loghttp.LabelResponse, error) {
	if len(filter) > 0 {
		return NewLokiHTTPClient(params).ListLabelValuesWithQuery(ctx, params.Label, BuildFilterSelector(filter), start, end)
	}
	return newLokiClient(params).ListLabelValues(params.Label, true, start, end)
}
