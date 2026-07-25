package handlers

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	// FormatJSON is Loki's own query-range wire format, two-space indented:
	// the response is re-encoded from a dedicated DTO rather than from the
	// loghttp structs (which decode but do not encode), so the output parses
	// back through loghttp.QueryResponse.UnmarshalJSON. Metric queries
	// (matrix/vector/scalar) are the exception — they still marshal the
	// loghttp structs and do not round-trip.
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

// lokiStreamsResponse is the JSON shape Loki's query-range endpoint returns for
// a streams result. It exists because loghttp.QueryResponse, loghttp.Stream and
// loghttp.Entry define UnmarshalJSON but no MarshalJSON and carry no struct
// tags: re-encoding them yields Go-shaped JSON that no Loki-aware parser
// accepts. Marshalling this DTO instead round-trips through
// loghttp.QueryResponse.UnmarshalJSON.
//
// Loki's "stats" block is deliberately omitted: post-processing (secret
// filtering, head/tail, the keyword filter) invalidates it and it is never
// recomputed, so emitting the decoded zero value would be a lie. The field is
// optional on the wire.
type lokiStreamsResponse struct {
	Status   string          `json:"status"`
	Warnings []string        `json:"warnings,omitempty"`
	Data     lokiStreamsData `json:"data"`
}

type lokiStreamsData struct {
	ResultType string           `json:"resultType"`
	Result     []lokiWireStream `json:"result"`
}

// lokiWireStream is one element of the "result" array: the stream's label set
// and its entries as [timestamp, line] tuples.
type lokiWireStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]any           `json:"values"`
}

// newLokiStreamsResponse builds the wire DTO for a streams result. streams is
// passed separately because the caller has already type-asserted it out of
// result.Data.Result.
func newLokiStreamsResponse(result *loghttp.QueryResponse, streams loghttp.Streams) lokiStreamsResponse {
	resultType := string(result.Data.ResultType)
	if resultType == "" {
		resultType = loghttp.ResultTypeStream
	}

	values := make([]lokiWireStream, 0, len(streams))
	for _, stream := range streams {
		values = append(values, newLokiWireStream(stream))
	}

	return lokiStreamsResponse{
		Status:   result.Status,
		Warnings: tagInternalWarnings(result.Warnings),
		Data: lokiStreamsData{
			ResultType: resultType,
			Result:     values,
		},
	}
}

// newLokiWireStream converts one decoded stream. Both maps and slices are
// allocated rather than aliased-when-nil so that an empty stream marshals as
// {} and [] instead of null, matching Loki.
func newLokiWireStream(stream loghttp.Stream) lokiWireStream {
	labelSet := make(map[string]string, len(stream.Labels))
	for name, value := range stream.Labels {
		labelSet[name] = value
	}

	entries := make([][]any, 0, len(stream.Entries))
	for _, entry := range stream.Entries {
		entries = append(entries, newLokiWireEntry(entry))
	}

	return lokiWireStream{Stream: labelSet, Values: entries}
}

// newLokiWireEntry renders one entry as ["<unix-nanos>", "<line>"], with Loki's
// optional third "categorized labels" element appended when the entry carries
// structured metadata or parsed labels.
func newLokiWireEntry(entry loghttp.Entry) []any {
	value := []any{strconv.FormatInt(entry.Timestamp.UnixNano(), 10), entry.Line}

	categorized := make(map[string]map[string]string, 2)
	if entry.StructuredMetadata.Len() > 0 {
		categorized["structuredMetadata"] = entry.StructuredMetadata.Map()
	}
	if entry.Parsed.Len() > 0 {
		categorized["parsed"] = entry.Parsed.Map()
	}
	if len(categorized) > 0 {
		value = append(value, categorized)
	}
	return value
}

// internalWarningPrefix marks a warning this server injected, so that a client
// can tell it apart from one Loki itself returned. Loki never emits it.
const internalWarningPrefix = "loki-mcp: "

// tagInternalWarnings prefixes the warnings this server injected with
// internalWarningPrefix and passes Loki's own through untouched. It returns nil
// for an empty input so the JSON DTO omits the field.
func tagInternalWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return nil
	}

	tagged := make([]string, len(warnings))
	for i, warning := range warnings {
		if isSecretOmissionNote(warning) {
			tagged[i] = internalWarningPrefix + warning
			continue
		}
		tagged[i] = warning
	}
	return tagged
}

// isSecretOmissionNote reports whether warning is a note this server produced
// with formatSecretOmissionNote. It reconstructs the note from the leading
// count rather than pattern-matching, so it cannot drift from the wording.
func isSecretOmissionNote(warning string) bool {
	count, _, ok := strings.Cut(warning, " ")
	if !ok {
		return false
	}
	lines, err := strconv.Atoi(count)
	if err != nil || lines <= 0 {
		return false
	}
	return warning == formatSecretOmissionNote(lines)
}

// appendWarnings writes the trailing warnings block shared by the raw and text
// renderers, separated from the payload above it by exactly one blank line. It
// is a no-op when there is nothing to report.
func appendWarnings(sb *strings.Builder, warnings []string) {
	if len(warnings) == 0 {
		return
	}

	if !strings.HasSuffix(sb.String(), "\n") {
		sb.WriteString("\n")
	}
	if !strings.HasSuffix(sb.String(), "\n\n") {
		sb.WriteString("\n")
	}

	sb.WriteString("Warnings:\n")
	for _, warning := range warnings {
		fmt.Fprintf(sb, "  - %s\n", warning)
	}
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

	warnings := tagInternalWarnings(result.Warnings)

	switch f {
	case FormatJSON:
		return marshalIndentJSON(newLokiStreamsResponse(result, streams))
	case FormatText:
		return renderText(streams, warnings), nil
	default: // FormatRaw
		return renderRaw(streams, warnings), nil
	}
}

// renderEmptyStreams renders a successful query that matched no logs. The json
// form only marshals the response when there are warnings to convey; the other
// formats append the same trailing warnings block the renderers use, so a
// warning is never lost just because the result was empty.
func renderEmptyStreams(result *loghttp.QueryResponse, f Format) (string, error) {
	const message = "No logs found matching the query"

	if f != FormatJSON {
		var sb strings.Builder
		sb.WriteString(message)
		appendWarnings(&sb, tagInternalWarnings(result.Warnings))
		return sb.String(), nil
	}
	if len(result.Warnings) > 0 {
		return marshalIndentJSON(newLokiStreamsResponse(result, nil))
	}
	return "{\"message\": \"" + message + "\"}", nil
}

// renderRaw writes one line per entry as "<timestamp> [<labels> ]<line>",
// followed by the warnings block when the response carries warnings.
func renderRaw(streams loghttp.Streams, warnings []string) string {
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
	appendWarnings(&sb, warnings)
	return sb.String()
}

// renderText writes a numbered, per-stream listing of the entries, followed by
// the warnings block when the response carries warnings.
func renderText(streams loghttp.Streams, warnings []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d streams:\n\n", len(streams))

	for i, stream := range streams {
		fmt.Fprintf(&sb, "Stream %s %d:\n", stream.Labels.String(), i+1)

		for _, entry := range stream.Entries {
			fmt.Fprintf(&sb, "[%s] %s\n", entry.Timestamp.Format(time.RFC3339), entry.Line)
		}
		sb.WriteString("\n")
	}
	appendWarnings(&sb, warnings)
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
