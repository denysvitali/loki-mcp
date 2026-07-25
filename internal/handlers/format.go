package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

// Format is the output format requested by a tool call. It stays a plain
// string on the wire; LokiParams.Format holds the unvalidated client value and
// is converted here.
//
// Conversion is not validation. Format("yaml") is a perfectly good value of
// this type; only validate rejects it, and the formatters call validate late —
// after the empty-result and non-stream branches — so an unsupported format
// can still produce output rather than an error. See formatLokiResults.
type Format string

// The three supported output formats. Anything else is rejected by validate
// with an error naming all three. There is no default constant: an empty
// Format is invalid, and "raw" is substituted upstream by
// (*LokiParams).applyDefaults, not here.
const (
	// FormatRaw is one line per entry, "<timestamp> [<labels> ]<line>". It is
	// what a client gets when it asks for nothing.
	FormatRaw Format = "raw"
	// FormatJSON is the decoded Loki response re-marshalled with two-space
	// indentation. It is *not* Loki's wire format and does not round-trip
	// back through a Loki-aware parser (FINDINGS.md §6); it is also the only
	// format that surfaces the response's Warnings.
	FormatJSON Format = "json"
	// FormatText is a numbered, per-stream listing intended for a human
	// reader.
	FormatText Format = "text"
)

// validate returns nil when f is one of the three supported formats, and
// otherwise an error naming f and listing them.
func (f Format) validate() error {
	switch f {
	case FormatRaw, FormatJSON, FormatText:
		return nil
	default:
		return fmt.Errorf("unsupported format: %s. Supported formats: raw, json, text", string(f))
	}
}

// marshalIndentJSON renders v as the two-space indented JSON used by the json
// output format.
func marshalIndentJSON(v any) (string, error) {
	jsonBytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return string(jsonBytes), nil
}

// formatLokiResults formats the Loki query results into a readable string
func formatLokiResults(result *loghttp.QueryResponse, format string) (string, error) {
	f := Format(format)

	// Metric queries return a matrix or vector rather than streams. Note that
	// this branch, like the empty-streams one below, runs before format
	// validation, so an unsupported format still produces output here.
	streams, ok := result.Data.Result.(loghttp.Streams)
	if !ok {
		if f == FormatJSON {
			return marshalIndentJSON(result)
		}
		return fmt.Sprintf("Query returned %s result type", result.Data.ResultType), nil
	}

	if len(streams) == 0 {
		return renderEmptyStreams(result, f)
	}

	if err := f.validate(); err != nil {
		return "", err
	}

	switch f {
	case FormatJSON:
		return marshalIndentJSON(result)
	case FormatText:
		return renderText(streams), nil
	default: // FormatRaw
		return renderRaw(streams), nil
	}
}

// renderEmptyStreams renders a successful query that matched no logs. The json
// form only marshals the response when there are warnings to convey.
func renderEmptyStreams(result *loghttp.QueryResponse, f Format) (string, error) {
	if f != FormatJSON {
		return "No logs found matching the query", nil
	}
	if len(result.Warnings) > 0 {
		return marshalIndentJSON(result)
	}
	return "{\"message\": \"No logs found matching the query\"}", nil
}

// renderRaw writes one line per entry as "<timestamp> [<labels> ]<line>".
func renderRaw(streams loghttp.Streams) string {
	var sb strings.Builder
	for _, stream := range streams {
		labels := stream.Labels.String()
		if labels != "{}" {
			labels += " "
		} else {
			labels = ""
		}

		for _, entry := range stream.Entries {
			timestamp := entry.Timestamp.Format(time.RFC3339)
			fmt.Fprintf(&sb, "%s %s%s\n", timestamp, labels, entry.Line)
		}
	}
	return sb.String()
}

// renderText writes a numbered, per-stream listing of the entries.
func renderText(streams loghttp.Streams) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d streams:\n\n", len(streams))

	for i, stream := range streams {
		fmt.Fprintf(&sb, "Stream %s %d:\n", stream.Labels.String(), i+1)

		for _, entry := range stream.Entries {
			fmt.Fprintf(&sb, "[%s] %s\n", entry.Timestamp.Format(time.RFC3339), entry.Line)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatLokiLabelsResults formats the Loki labels results into a readable string
func formatLokiLabelsResults(result *loghttp.LabelResponse, format string) (string, error) {
	f := Format(format)

	// Runs before format validation, matching the query formatter.
	if len(result.Data) == 0 {
		if f == FormatJSON {
			return "{\"message\": \"No labels found\"}", nil
		}
		return "No labels found", nil
	}

	if err := f.validate(); err != nil {
		return "", err
	}

	switch f {
	case FormatJSON:
		return marshalIndentJSON(result)
	case FormatText:
		return renderNumberedList(fmt.Sprintf("Found %d labels:\n\n", len(result.Data)), result.Data), nil
	default: // FormatRaw
		return renderLines(result.Data), nil
	}
}

// formatLokiLabelValuesResults formats the Loki label values results into a readable string
func formatLokiLabelValuesResults(labelName string, result *loghttp.LabelResponse, format string) (string, error) {
	f := Format(format)

	// Runs before format validation, matching the query formatter.
	if len(result.Data) == 0 {
		if f == FormatJSON {
			return fmt.Sprintf("{\"message\": \"No values found for label '%s'\"}", labelName), nil
		}
		return fmt.Sprintf("No values found for label '%s'", labelName), nil
	}

	if err := f.validate(); err != nil {
		return "", err
	}

	switch f {
	case FormatJSON:
		return marshalIndentJSON(result)
	case FormatText:
		header := fmt.Sprintf("Found %d values for label '%s':\n\n", len(result.Data), labelName)
		return renderNumberedList(header, result.Data), nil
	default: // FormatRaw
		return renderLines(result.Data), nil
	}
}

// renderLines writes one item per line.
func renderLines(items []string) string {
	var sb strings.Builder
	for _, item := range items {
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	return sb.String()
}

// renderNumberedList writes header followed by a 1-based numbered list.
func renderNumberedList(header string, items []string) string {
	var sb strings.Builder
	sb.WriteString(header)

	for i, item := range items {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, item)
	}
	return sb.String()
}
