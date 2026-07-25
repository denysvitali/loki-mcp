package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/prometheus/prometheus/model/labels"
)

const unsupportedFormatErr = "unsupported format: xml. Supported formats: raw, json, text"

func streamsResponse() *loghttp.QueryResponse {
	return &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{"job": "test-job"},
					Entries: []loghttp.Entry{
						{Timestamp: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC), Line: "hello"},
					},
				},
			},
		},
	}
}

// TestFormatLokiResults_ExactOutput pins the byte-for-byte rendering of each
// supported format.
func TestFormatLokiResults_ExactOutput(t *testing.T) {
	t.Parallel()

	multi := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{"job": "a"},
					Entries: []loghttp.Entry{
						{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Line: "one"},
						{Timestamp: time.Date(2024, 1, 15, 10, 0, 1, 0, time.UTC), Line: "two"},
					},
				},
				{
					Labels: loghttp.LabelSet{},
					Entries: []loghttp.Entry{
						{Timestamp: time.Date(2024, 1, 15, 10, 0, 2, 0, time.UTC), Line: "three"},
					},
				},
			},
		},
	}

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "raw drops empty label sets and appends a space after labels",
			format: "raw",
			want: "2024-01-15T10:00:00Z {job=\"a\"} one\n" +
				"2024-01-15T10:00:01Z {job=\"a\"} two\n" +
				"2024-01-15T10:00:02Z three\n",
		},
		{
			name:   "text numbers streams after printing their labels",
			format: "text",
			want: "Found 2 streams:\n\n" +
				"Stream {job=\"a\"} 1:\n" +
				"[2024-01-15T10:00:00Z] one\n" +
				"[2024-01-15T10:00:01Z] two\n\n" +
				"Stream {} 2:\n" +
				"[2024-01-15T10:00:02Z] three\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := formatLokiResults(multi, tt.format)
			if err != nil {
				t.Fatalf("formatLokiResults(%q) unexpected error: %v", tt.format, err)
			}
			if got != tt.want {
				t.Errorf("formatLokiResults(%q) =\n%q\nwant\n%q", tt.format, got, tt.want)
			}
		})
	}
}

// TestFormatLokiResults_UnsupportedFormat pins the error text shared by all
// three formatters.
func TestFormatLokiResults_UnsupportedFormat(t *testing.T) {
	t.Parallel()

	labelResp := &loghttp.LabelResponse{Status: "success", Data: []string{"job"}}

	tests := []struct {
		name string
		call func() (string, error)
	}{
		{"query results", func() (string, error) { return formatLokiResults(streamsResponse(), "xml") }},
		{"label names", func() (string, error) { return formatLokiLabelsResults(labelResp, "xml") }},
		{"label values", func() (string, error) { return formatLokiLabelValuesResults("job", labelResp, "xml") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.call()
			if err == nil {
				t.Fatalf("expected error, got output %q", got)
			}
			if err.Error() != unsupportedFormatErr {
				t.Errorf("error = %q, want %q", err.Error(), unsupportedFormatErr)
			}
			if got != "" {
				t.Errorf("output = %q, want empty string on error", got)
			}
		})
	}
}

// TestFormatLokiResults_NonStreamResult pins the behaviour when Loki returns a
// matrix/vector instead of streams: json still marshals, every other format
// (including unsupported ones) short-circuits to the result-type message.
func TestFormatLokiResults_NonStreamResult(t *testing.T) {
	t.Parallel()

	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeMatrix,
			Result:     loghttp.Matrix{},
		},
	}

	for _, format := range []string{"raw", "text", "xml"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			got, err := formatLokiResults(result, format)
			if err != nil {
				t.Fatalf("formatLokiResults(%q) unexpected error: %v", format, err)
			}
			if want := "Query returned matrix result type"; got != want {
				t.Errorf("formatLokiResults(%q) = %q, want %q", format, got, want)
			}
		})
	}

	t.Run("json", func(t *testing.T) {
		t.Parallel()

		got, err := formatLokiResults(result, "json")
		if err != nil {
			t.Fatalf("formatLokiResults(json) unexpected error: %v", err)
		}
		if !strings.Contains(got, `"resultType": "matrix"`) {
			t.Errorf("expected marshalled matrix response, got:\n%s", got)
		}
	})
}

// TestFormatLokiResults_EmptyStreams pins the empty-result messages, including
// the json branch that only marshals when warnings are present.
func TestFormatLokiResults_EmptyStreams(t *testing.T) {
	t.Parallel()

	empty := func(warnings ...string) *loghttp.QueryResponse {
		return &loghttp.QueryResponse{
			Status:   "success",
			Warnings: warnings,
			Data: loghttp.QueryResponseData{
				ResultType: loghttp.ResultTypeStream,
				Result:     loghttp.Streams{},
			},
		}
	}

	for _, format := range []string{"raw", "text", "xml"} {
		t.Run("no warnings "+format, func(t *testing.T) {
			t.Parallel()

			got, err := formatLokiResults(empty(), format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := "No logs found matching the query"; got != want {
				t.Errorf("formatLokiResults(%q) = %q, want %q", format, got, want)
			}
		})
	}

	t.Run("no warnings json", func(t *testing.T) {
		t.Parallel()

		got, err := formatLokiResults(empty(), "json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := `{"message": "No logs found matching the query"}`; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("with warnings json marshals the response", func(t *testing.T) {
		t.Parallel()

		got, err := formatLokiResults(empty("1 line omitted due to detected secrets"), "json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, `"warnings"`) || !strings.Contains(got, "detected secrets") {
			t.Errorf("expected warnings in output, got:\n%s", got)
		}
	})

	// Inverted expectation: an empty result used to swallow the warnings too.
	t.Run("with warnings raw reports no logs and the warnings", func(t *testing.T) {
		t.Parallel()

		got, err := formatLokiResults(empty("1 line omitted due to detected secrets"), "raw")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "No logs found matching the query\n\n" +
			"Warnings:\n" +
			"  - loki-mcp: 1 line omitted due to detected secrets\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestFormatLokiResults_WarningsBlock pins the trailing warnings block that
// raw and text render. Before FINDINGS.md §12 both formats dropped
// result.Warnings on the floor, so an upstream Loki warning never reached a
// non-JSON client.
func TestFormatLokiResults_WarningsBlock(t *testing.T) {
	t.Parallel()

	withWarnings := func(warnings ...string) *loghttp.QueryResponse {
		r := streamsResponse()
		r.Warnings = warnings
		return r
	}

	tests := []struct {
		name     string
		format   string
		warnings []string
		want     string
	}{
		{
			name:   "raw without warnings is unchanged",
			format: "raw",
			want:   "2024-01-15T10:30:45Z {job=\"test-job\"} hello\n",
		},
		{
			name:     "raw appends one blank line then the block",
			format:   "raw",
			warnings: []string{"a genuine warning from Loki"},
			want: "2024-01-15T10:30:45Z {job=\"test-job\"} hello\n\n" +
				"Warnings:\n" +
				"  - a genuine warning from Loki\n",
		},
		{
			name:   "text without warnings is unchanged",
			format: "text",
			want:   "Found 1 streams:\n\nStream {job=\"test-job\"} 1:\n[2024-01-15T10:30:45Z] hello\n\n",
		},
		{
			name:     "text reuses the blank line it already ends with",
			format:   "text",
			warnings: []string{"a genuine warning from Loki", "and another"},
			want: "Found 1 streams:\n\nStream {job=\"test-job\"} 1:\n[2024-01-15T10:30:45Z] hello\n\n" +
				"Warnings:\n" +
				"  - a genuine warning from Loki\n" +
				"  - and another\n",
		},
		{
			name:     "the tool's own note is tagged, Loki's is not",
			format:   "raw",
			warnings: []string{"a genuine warning from Loki", "3 lines omitted due to detected secrets"},
			want: "2024-01-15T10:30:45Z {job=\"test-job\"} hello\n\n" +
				"Warnings:\n" +
				"  - a genuine warning from Loki\n" +
				"  - loki-mcp: 3 lines omitted due to detected secrets\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := formatLokiResults(withWarnings(tt.warnings...), tt.format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("formatLokiResults(%q) =\n%q\nwant\n%q", tt.format, got, tt.want)
			}
		})
	}
}

// TestTagInternalWarnings pins which warnings get the loki-mcp: prefix. Only
// the notes this server produces with formatSecretOmissionNote do; anything
// Loki sent passes through byte for byte, including near-misses.
func TestTagInternalWarnings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"1 line omitted due to detected secrets", "loki-mcp: 1 line omitted due to detected secrets"},
		{"12 lines omitted due to detected secrets", "loki-mcp: 12 lines omitted due to detected secrets"},
		{"a genuine warning from Loki", "a genuine warning from Loki"},
		{"", ""},
		// Near-misses: the count and the plural must both match the note.
		{"1 lines omitted due to detected secrets", "1 lines omitted due to detected secrets"},
		{"2 line omitted due to detected secrets", "2 line omitted due to detected secrets"},
		{"0 lines omitted due to detected secrets", "0 lines omitted due to detected secrets"},
		{"1 line omitted due to detected secrets (from Loki)", "1 line omitted due to detected secrets (from Loki)"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got := tagInternalWarnings([]string{tt.in})
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("tagInternalWarnings(%q) = %q, want [%q]", tt.in, got, tt.want)
			}
		})
	}

	t.Run("empty input yields nil so the JSON field is omitted", func(t *testing.T) {
		t.Parallel()

		if got := tagInternalWarnings(nil); got != nil {
			t.Errorf("tagInternalWarnings(nil) = %q, want nil", got)
		}
	})
}

// TestFormatLokiLabelsResults_ExactOutput pins the label-names rendering.
func TestFormatLokiLabelsResults_ExactOutput(t *testing.T) {
	t.Parallel()

	result := &loghttp.LabelResponse{Status: "success", Data: []string{"job", "level"}}

	tests := []struct {
		format string
		want   string
	}{
		{"raw", "job\nlevel\n"},
		{"text", "Found 2 labels:\n\n1. job\n2. level\n"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()

			got, err := formatLokiLabelsResults(result, tt.format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatLokiLabelsResults_Empty pins the empty-label messages.
func TestFormatLokiLabelsResults_Empty(t *testing.T) {
	t.Parallel()

	result := &loghttp.LabelResponse{Status: "success"}

	// The empty branch runs before format validation, so even "xml" succeeds.
	for _, format := range []string{"raw", "text", "xml"} {
		got, err := formatLokiLabelsResults(result, format)
		if err != nil {
			t.Fatalf("formatLokiLabelsResults(%q) unexpected error: %v", format, err)
		}
		if want := "No labels found"; got != want {
			t.Errorf("formatLokiLabelsResults(%q) = %q, want %q", format, got, want)
		}
	}

	got, err := formatLokiLabelsResults(result, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `{"message": "No labels found"}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFormatLokiLabelValuesResults_ExactOutput pins the label-values rendering.
func TestFormatLokiLabelValuesResults_ExactOutput(t *testing.T) {
	t.Parallel()

	result := &loghttp.LabelResponse{Status: "success", Data: []string{"api", "web"}}

	tests := []struct {
		format string
		want   string
	}{
		{"raw", "api\nweb\n"},
		{"text", "Found 2 values for label 'job':\n\n1. api\n2. web\n"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()

			got, err := formatLokiLabelValuesResults("job", result, tt.format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatLokiLabelValuesResults_Empty pins the empty-values messages.
func TestFormatLokiLabelValuesResults_Empty(t *testing.T) {
	t.Parallel()

	result := &loghttp.LabelResponse{Status: "success"}

	for _, format := range []string{"raw", "text", "xml"} {
		got, err := formatLokiLabelValuesResults("job", result, format)
		if err != nil {
			t.Fatalf("formatLokiLabelValuesResults(%q) unexpected error: %v", format, err)
		}
		if want := "No values found for label 'job'"; got != want {
			t.Errorf("formatLokiLabelValuesResults(%q) = %q, want %q", format, got, want)
		}
	}

	got, err := formatLokiLabelValuesResults("job", result, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `{"message": "No values found for label 'job'"}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFormatLokiResults_JSONIndent pins that json output is two-space indented.
func TestFormatLokiResults_JSONIndent(t *testing.T) {
	t.Parallel()

	got, err := formatLokiResults(streamsResponse(), "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "\n  \"status\": \"success\"") {
		t.Errorf("expected two-space indented JSON, got:\n%s", got)
	}
}

// TestFormatLokiResults_JSONWireShape pins the json output byte for byte. The
// shape is Loki's own query-range response: entries are ["<unix-nanos>",
// "<line>"] tuples, not Go structs, and the never-recomputed "stats" block is
// dropped.
func TestFormatLokiResults_JSONWireShape(t *testing.T) {
	t.Parallel()

	got, err := formatLokiResults(streamsResponse(), "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `{
  "status": "success",
  "data": {
    "resultType": "streams",
    "result": [
      {
        "stream": {
          "job": "test-job"
        },
        "values": [
          [
            "1705314645000000000",
            "hello"
          ]
        ]
      }
    ]
  }
}`
	if got != want {
		t.Errorf("formatLokiResults(json) =\n%s\nwant\n%s", got, want)
	}
}

// TestFormatLokiResults_JSONRoundTrip proves the json output parses back
// through the Loki client's own decoder with every stream, label, timestamp and
// line intact. Re-marshalling the loghttp structs directly does not: they
// define UnmarshalJSON but no MarshalJSON (FINDINGS.md §6).
func TestFormatLokiResults_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	metadata := labels.FromStrings("trace_id", "0242ac120002")
	parsed := labels.FromStrings("msg", "text")

	original := &loghttp.QueryResponse{
		Status:   "success",
		Warnings: []string{"a genuine warning from Loki"},
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{"job": "api", "level": "info"},
					Entries: []loghttp.Entry{
						{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Line: "one"},
						{
							Timestamp:          time.Date(2024, 1, 15, 10, 0, 1, 500, time.UTC),
							Line:               "two \"quoted\" and\nnewlined",
							StructuredMetadata: metadata,
							Parsed:             parsed,
						},
					},
				},
				{Labels: loghttp.LabelSet{"job": "web"}, Entries: []loghttp.Entry{}},
			},
		},
	}

	encoded, err := formatLokiResults(original, "json")
	if err != nil {
		t.Fatalf("formatLokiResults(json) unexpected error: %v", err)
	}

	var decoded loghttp.QueryResponse
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("round trip failed: %v\noutput was:\n%s", err, encoded)
	}

	assertRoundTripped(t, original, &decoded)
}

// assertRoundTripped compares a decoded response against the one that was
// encoded, field by field, so a mismatch names what was lost.
func assertRoundTripped(t *testing.T, want, got *loghttp.QueryResponse) {
	t.Helper()

	if got.Status != want.Status {
		t.Errorf("status = %q, want %q", got.Status, want.Status)
	}
	if got.Data.ResultType != want.Data.ResultType {
		t.Errorf("resultType = %q, want %q", got.Data.ResultType, want.Data.ResultType)
	}
	if len(got.Warnings) != len(want.Warnings) || (len(want.Warnings) > 0 && got.Warnings[0] != want.Warnings[0]) {
		t.Errorf("warnings = %q, want %q", got.Warnings, want.Warnings)
	}

	wantStreams := want.Data.Result.(loghttp.Streams)
	gotStreams, ok := got.Data.Result.(loghttp.Streams)
	if !ok {
		t.Fatalf("decoded result type = %T, want loghttp.Streams", got.Data.Result)
	}
	if len(gotStreams) != len(wantStreams) {
		t.Fatalf("decoded %d streams, want %d", len(gotStreams), len(wantStreams))
	}

	for i, wantStream := range wantStreams {
		gotStream := gotStreams[i]
		if gotStream.Labels.String() != wantStream.Labels.String() {
			t.Errorf("stream %d labels = %s, want %s", i, gotStream.Labels, wantStream.Labels)
		}
		if len(gotStream.Entries) != len(wantStream.Entries) {
			t.Errorf("stream %d has %d entries, want %d", i, len(gotStream.Entries), len(wantStream.Entries))
			continue
		}
		for j, wantEntry := range wantStream.Entries {
			assertEntryRoundTripped(t, i, j, wantEntry, gotStream.Entries[j])
		}
	}
}

// assertEntryRoundTripped compares one decoded entry against the encoded one.
func assertEntryRoundTripped(t *testing.T, stream, index int, want, got loghttp.Entry) {
	t.Helper()

	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("stream %d entry %d timestamp = %s, want %s", stream, index, got.Timestamp, want.Timestamp)
	}
	if got.Line != want.Line {
		t.Errorf("stream %d entry %d line = %q, want %q", stream, index, got.Line, want.Line)
	}
	if gotMeta, wantMeta := got.StructuredMetadata.String(), want.StructuredMetadata.String(); gotMeta != wantMeta {
		t.Errorf("stream %d entry %d structured metadata = %s, want %s", stream, index, gotMeta, wantMeta)
	}
	if gotParsed, wantParsed := got.Parsed.String(), want.Parsed.String(); gotParsed != wantParsed {
		t.Errorf("stream %d entry %d parsed labels = %s, want %s", stream, index, gotParsed, wantParsed)
	}
}

// TestFormatLokiResults_JSONEmptyStreamValues pins that a stream with no
// entries marshals as [] rather than the null the loghttp structs produced.
func TestFormatLokiResults_JSONEmptyStreamValues(t *testing.T) {
	t.Parallel()

	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result:     loghttp.Streams{{Labels: loghttp.LabelSet{"job": "api"}}},
		},
	}

	got, err := formatLokiResults(result, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, `"values": []`) {
		t.Errorf("expected an empty values array, got:\n%s", got)
	}
	if strings.Contains(got, "null") {
		t.Errorf("expected no nulls in the wire output, got:\n%s", got)
	}
}

// TestFormatLokiResults_TimestampParsing tests that timestamps from Loki are correctly formatted
// With the official client, timestamps are already parsed as time.Time, so this test
// verifies that formatting works correctly.
func TestFormatLokiResults_TimestampParsing(t *testing.T) {
	// Test case with known timestamp: 2024-01-15T10:30:45Z
	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)

	// Create a sample Loki result with the test timestamp
	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{
						"job":   "test-job",
						"level": "info",
					},
					Entries: []loghttp.Entry{
						{Timestamp: timestamp, Line: "Test log message"},
					},
				},
			},
		},
	}

	// Format the results
	output, err := formatLokiResults(result, "text")
	if err != nil {
		t.Fatalf("formatLokiResults failed: %v", err)
	}

	// Verify the output contains the correct timestamp
	if !strings.Contains(output, "2024-01-15T") {
		t.Errorf("Expected output to contain date '2024-01-15T', but got:\n%s", output)
	}

	// Verify it doesn't contain the year 2262 (the bug we fixed)
	if strings.Contains(output, "2262") {
		t.Errorf("Output contains year 2262, indicating timestamp parsing bug is present:\n%s", output)
	}

	// Verify it contains the expected log message
	if !strings.Contains(output, "Test log message") {
		t.Errorf("Expected output to contain 'Test log message', but got:\n%s", output)
	}

	// Verify it contains the stream information
	if !strings.Contains(output, "job") && !strings.Contains(output, "test-job") {
		t.Errorf("Expected output to contain stream info 'job' or 'test-job', but got:\n%s", output)
	}
}

// TestFormatLokiResults_MultipleTimestamps tests formatting of multiple log entries with different timestamps
func TestFormatLokiResults_MultipleTimestamps(t *testing.T) {
	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{
						"job": "test-job",
					},
					Entries: []loghttp.Entry{
						{Timestamp: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC), Line: "First log message"},
						{Timestamp: time.Date(2024, 1, 15, 10, 31, 0, 0, time.UTC), Line: "Second log message"},
						{Timestamp: time.Date(2024, 1, 15, 10, 31, 15, 0, time.UTC), Line: "Third log message"},
					},
				},
			},
		},
	}

	output, err := formatLokiResults(result, "text")
	if err != nil {
		t.Fatalf("formatLokiResults failed: %v", err)
	}

	// Check that all timestamps are in 2024, not 2262
	if strings.Contains(output, "2262") {
		t.Errorf("Output contains year 2262, indicating timestamp parsing bug:\n%s", output)
	}

	// All timestamps should be in 2024
	occurrences := strings.Count(output, "2024")
	if occurrences < 3 {
		t.Errorf("Expected at least 3 occurrences of '2024' in output, but found %d:\n%s", occurrences, output)
	}
}

// TestFormatLokiResults_EmptyResult tests handling of empty results
func TestFormatLokiResults_EmptyResult(t *testing.T) {
	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result:     loghttp.Streams{},
		},
	}

	output, err := formatLokiResults(result, "text")
	if err != nil {
		t.Fatalf("formatLokiResults failed: %v", err)
	}

	expected := "No logs found matching the query"
	if output != expected {
		t.Errorf("Expected output '%s', but got '%s'", expected, output)
	}
}

func TestFormatLokiResults_EmptyJSONWithWarnings(t *testing.T) {
	result := &loghttp.QueryResponse{
		Status:   "success",
		Warnings: []string{"1 line omitted due to detected secrets"},
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result:     loghttp.Streams{},
		},
	}

	output, err := formatLokiResults(result, "json")
	if err != nil {
		t.Fatalf("formatLokiResults failed: %v", err)
	}

	if !strings.Contains(output, `"warnings"`) {
		t.Errorf("Expected output to contain warnings, got:\n%s", output)
	}
	// Inverted expectation: the note now carries the loki-mcp: tag that tells
	// a JSON client it came from this server and not from Loki.
	if !strings.Contains(output, "loki-mcp: 1 line omitted due to detected secrets") {
		t.Errorf("Expected output to contain the tagged secret omission note, got:\n%s", output)
	}
}

// TestFormatLokiResults_RecentTimestamp tests with a very recent timestamp to ensure current dates work
func TestFormatLokiResults_RecentTimestamp(t *testing.T) {
	// Use current time
	now := time.Now().UTC()

	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{
						"job": "recent-test",
					},
					Entries: []loghttp.Entry{
						{Timestamp: now, Line: "Recent log message"},
					},
				},
			},
		},
	}

	output, err := formatLokiResults(result, "text")
	if err != nil {
		t.Fatalf("formatLokiResults failed: %v", err)
	}

	// Should contain current year, not 2262
	currentYear := now.Format("2006")
	if !strings.Contains(output, currentYear) {
		t.Errorf("Expected output to contain current year %s, but got:\n%s", currentYear, output)
	}

	if strings.Contains(output, "2262") {
		t.Errorf("Output contains year 2262, indicating timestamp parsing bug:\n%s", output)
	}
}

// TestFormatLokiResults_NoYear2262Bug is a regression test for the specific bug reported in issue #3
// This test ensures that timestamps never show year 2262 due to incorrect nanosecond conversion
func TestFormatLokiResults_NoYear2262Bug(t *testing.T) {
	// This test uses a variety of realistic timestamps
	testCases := []struct {
		name         string
		timestamp    time.Time
		expectedYear string
	}{
		{
			name:         "Current timestamp",
			timestamp:    time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
			expectedYear: "2024",
		},
		{
			name:         "Recent timestamp",
			timestamp:    time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC),
			expectedYear: "2023",
		},
		{
			name:         "Future timestamp",
			timestamp:    time.Date(2027, 1, 11, 2, 13, 20, 0, time.UTC),
			expectedYear: "2027",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := &loghttp.QueryResponse{
				Status: "success",
				Data: loghttp.QueryResponseData{
					ResultType: loghttp.ResultTypeStream,
					Result: loghttp.Streams{
						{
							Labels: loghttp.LabelSet{
								"job": "regression-test",
							},
							Entries: []loghttp.Entry{
								{Timestamp: tc.timestamp, Line: "Test log message"},
							},
						},
					},
				},
			}

			output, err := formatLokiResults(result, "text")
			if err != nil {
				t.Fatalf("formatLokiResults failed: %v", err)
			}

			// The main regression check: ensure we never see year 2262
			if strings.Contains(output, "2262") {
				t.Errorf("REGRESSION: Output contains year 2262, the original bug is present:\n%s", output)
			}

			// Verify we see the expected year instead
			if !strings.Contains(output, tc.expectedYear) {
				t.Errorf("Expected output to contain year %s, but got:\n%s", tc.expectedYear, output)
			}
		})
	}
}

// TestFormatLokiResults_RawFormat tests the raw output format
func TestFormatLokiResults_RawFormat(t *testing.T) {
	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)

	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{
						"job": "test-job",
					},
					Entries: []loghttp.Entry{
						{Timestamp: timestamp, Line: "Test log message"},
					},
				},
			},
		},
	}

	output, err := formatLokiResults(result, "raw")
	if err != nil {
		t.Fatalf("formatLokiResults failed: %v", err)
	}

	// Raw format should have timestamp, labels, and message
	if !strings.Contains(output, "2024-01-15T10:30:45Z") {
		t.Errorf("Expected output to contain timestamp '2024-01-15T10:30:45Z', but got:\n%s", output)
	}

	if !strings.Contains(output, "Test log message") {
		t.Errorf("Expected output to contain 'Test log message', but got:\n%s", output)
	}
}

// TestFormatLokiResults_JSONFormat tests the JSON output format
func TestFormatLokiResults_JSONFormat(t *testing.T) {
	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)

	result := &loghttp.QueryResponse{
		Status: "success",
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Labels: loghttp.LabelSet{
						"job": "test-job",
					},
					Entries: []loghttp.Entry{
						{Timestamp: timestamp, Line: "Test log message"},
					},
				},
			},
		},
	}

	output, err := formatLokiResults(result, "json")
	if err != nil {
		t.Fatalf("formatLokiResults failed: %v", err)
	}

	// JSON format should be valid JSON with status
	if !strings.Contains(output, `"status"`) {
		t.Errorf("Expected output to contain '\"status\"', but got:\n%s", output)
	}
}

// TestFormatLokiLabelsResults tests the labels formatting
func TestFormatLokiLabelsResults(t *testing.T) {
	result := &loghttp.LabelResponse{
		Status: "success",
		Data:   []string{"job", "level", "namespace"},
	}

	// Test raw format
	output, err := formatLokiLabelsResults(result, "raw")
	if err != nil {
		t.Fatalf("formatLokiLabelsResults failed: %v", err)
	}

	if !strings.Contains(output, "job\n") {
		t.Errorf("Expected output to contain 'job', but got:\n%s", output)
	}

	// Test text format
	output, err = formatLokiLabelsResults(result, "text")
	if err != nil {
		t.Fatalf("formatLokiLabelsResults failed: %v", err)
	}

	if !strings.Contains(output, "Found 3 labels") {
		t.Errorf("Expected output to contain 'Found 3 labels', but got:\n%s", output)
	}
}

// TestFormatLokiLabelValuesResults tests the label values formatting
func TestFormatLokiLabelValuesResults(t *testing.T) {
	result := &loghttp.LabelResponse{
		Status: "success",
		Data:   []string{"value1", "value2", "value3"},
	}

	// Test raw format
	output, err := formatLokiLabelValuesResults("testlabel", result, "raw")
	if err != nil {
		t.Fatalf("formatLokiLabelValuesResults failed: %v", err)
	}

	if !strings.Contains(output, "value1\n") {
		t.Errorf("Expected output to contain 'value1', but got:\n%s", output)
	}

	// Test text format
	output, err = formatLokiLabelValuesResults("testlabel", result, "text")
	if err != nil {
		t.Fatalf("formatLokiLabelValuesResults failed: %v", err)
	}

	if !strings.Contains(output, "Found 3 values for label 'testlabel'") {
		t.Errorf("Expected output to contain label count and name, but got:\n%s", output)
	}
}
