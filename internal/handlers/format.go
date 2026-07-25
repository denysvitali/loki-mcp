package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

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
