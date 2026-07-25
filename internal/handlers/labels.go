package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/mark3labs/mcp-go/mcp"
)

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
