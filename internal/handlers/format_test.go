package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
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

	t.Run("with warnings raw still reports no logs", func(t *testing.T) {
		t.Parallel()

		got, err := formatLokiResults(empty("1 line omitted due to detected secrets"), "raw")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "No logs found matching the query"; got != want {
			t.Errorf("got %q, want %q", got, want)
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
