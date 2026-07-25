package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// newFakeLabelLoki serves body for every request and records path + query.
func newFakeLabelLoki(t *testing.T, body string) (*httptest.Server, *capturedRequest) {
	t.Helper()

	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		captured.query = r.URL.Query()
		captured.auth = r.Header.Get("Authorization")
		captured.orgID = r.Header.Get("X-Scope-OrgID")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func toolRequest(name string, args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}}
}

// TestHandleLokiLabelNames pins the unfiltered path: the SDK client is used and
// no query parameter is sent.
func TestHandleLokiLabelNames(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "default raw", format: "", want: "job\nlevel\n"},
		{name: "raw", format: "raw", want: "job\nlevel\n"},
		{name: "text", format: "text", want: "Found 2 labels:\n\n1. job\n2. level\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, captured := newFakeLabelLoki(t, labelResponseBody)
			args := map[string]any{"url": srv.URL}
			if tt.format != "" {
				args["format"] = tt.format
			}

			res, err := newTestHandlers(t).HandleLabelNames(context.Background(), toolRequest("loki_label_names", args))
			if err != nil {
				t.Fatalf("HandleLokiLabelNames returned error: %v", err)
			}
			if got := resultText(t, res); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
			if captured.path != "/loki/api/v1/labels" {
				t.Errorf("path = %q, want /loki/api/v1/labels", captured.path)
			}
			if _, ok := captured.query["query"]; ok {
				t.Errorf("unfiltered request must not send a query parameter, got %q", captured.query.Get("query"))
			}
		})
	}

	t.Run("json", func(t *testing.T) {
		srv, _ := newFakeLabelLoki(t, labelResponseBody)
		args := map[string]any{"url": srv.URL, "format": "json"}

		res, err := newTestHandlers(t).HandleLabelNames(context.Background(), toolRequest("loki_label_names", args))
		if err != nil {
			t.Fatalf("HandleLokiLabelNames returned error: %v", err)
		}
		if got := resultText(t, res); !strings.HasPrefix(got, "{\n  \"status\": \"success\",") {
			t.Errorf("expected indented JSON, got:\n%s", got)
		}
	})

	t.Run("unsupported format", func(t *testing.T) {
		srv, _ := newFakeLabelLoki(t, labelResponseBody)
		args := map[string]any{"url": srv.URL, "format": "xml"}

		_, err := newTestHandlers(t).HandleLabelNames(context.Background(), toolRequest("loki_label_names", args))
		if err == nil {
			t.Fatal("expected error")
		}
		if want := "failed to format results: " + unsupportedFormatErr; err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})
}

// TestHandleLokiLabelValues pins the unfiltered label-values path.
func TestHandleLokiLabelValues(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "default raw", format: "", want: "job\nlevel\n"},
		{name: "raw", format: "raw", want: "job\nlevel\n"},
		{name: "text", format: "text", want: "Found 2 values for label 'app':\n\n1. job\n2. level\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, captured := newFakeLabelLoki(t, labelResponseBody)
			args := map[string]any{"url": srv.URL, "label": "app"}
			if tt.format != "" {
				args["format"] = tt.format
			}

			res, err := newTestHandlers(t).HandleLabelValues(context.Background(), toolRequest("loki_label_values", args))
			if err != nil {
				t.Fatalf("HandleLokiLabelValues returned error: %v", err)
			}
			if got := resultText(t, res); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
			if captured.path != "/loki/api/v1/label/app/values" {
				t.Errorf("path = %q, want /loki/api/v1/label/app/values", captured.path)
			}
		})
	}

	t.Run("unsupported format", func(t *testing.T) {
		srv, _ := newFakeLabelLoki(t, labelResponseBody)
		args := map[string]any{"url": srv.URL, "label": "app", "format": "xml"}

		_, err := newTestHandlers(t).HandleLabelValues(context.Background(), toolRequest("loki_label_values", args))
		if err == nil {
			t.Fatal("expected error")
		}
		if want := "failed to format results: " + unsupportedFormatErr; err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})
}

// TestHandleLokiLabels_TimeRangeErrors pins the shared start/end error strings
// of both label handlers.
func TestHandleLokiLabels_TimeRangeErrors(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)
	toolHandlers := map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error){
		"loki_label_names":  h.HandleLabelNames,
		"loki_label_values": h.HandleLabelValues,
	}

	cases := []struct {
		field string
		want  string
	}{
		{field: "start", want: "invalid start time: unsupported time format: nope"},
		{field: "end", want: "invalid end time: unsupported time format: nope"},
	}

	for name, handler := range toolHandlers {
		for _, tc := range cases {
			t.Run(name+"/"+tc.field, func(t *testing.T) {
				t.Parallel()

				args := map[string]any{"label": "app", tc.field: "nope"}

				_, err := handler(context.Background(), toolRequest(name, args))
				if err == nil {
					t.Fatal("expected error")
				}
				if err.Error() != tc.want {
					t.Errorf("error = %q, want %q", err.Error(), tc.want)
				}
			})
		}
	}
}

// TestHandleLokiLabels_RequestErrors pins the per-handler error wrappers.
func TestHandleLokiLabels_RequestErrors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	t.Run("label names", func(t *testing.T) {
		t.Parallel()

		_, err := newTestHandlers(t).HandleLabelNames(context.Background(),
			toolRequest("loki_label_names", map[string]any{"url": srv.URL}))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.HasPrefix(err.Error(), "labels query execution failed: ") {
			t.Errorf("error = %q, want a labels query failure", err.Error())
		}
	})

	t.Run("label values", func(t *testing.T) {
		t.Parallel()

		_, err := newTestHandlers(t).HandleLabelValues(context.Background(),
			toolRequest("loki_label_values", map[string]any{"url": srv.URL, "label": "app"}))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.HasPrefix(err.Error(), "label values query execution failed: ") {
			t.Errorf("error = %q, want a label values query failure", err.Error())
		}
	})
}

// TestHandleLokiLabels_BindErrors pins the argument-binding error prefix.
func TestHandleLokiLabels_BindErrors(t *testing.T) {
	t.Parallel()

	_, err := newTestHandlers(t).HandleLabelValues(context.Background(),
		toolRequest("loki_label_values", map[string]any{"label": float64(1)}))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "invalid arguments: ") {
		t.Errorf("error = %q, want an argument binding failure", err.Error())
	}
}

// TestHandleLokiLabels_WithQueryFilter pins that a configured filter switches
// both handlers to the HTTP client and sends the filter as a query parameter.
func TestHandleLokiLabels_WithQueryFilter(t *testing.T) {
	t.Parallel()

	h := New(newTestConfig(t, `{namespace="prod"}`, false))

	tests := []struct {
		name     string
		call     func(srv *httptest.Server) (*mcp.CallToolResult, error)
		wantPath string
	}{
		{
			name: "label names",
			call: func(srv *httptest.Server) (*mcp.CallToolResult, error) {
				return h.HandleLabelNames(context.Background(),
					toolRequest("loki_label_names", map[string]any{"url": srv.URL}))
			},
			wantPath: "/loki/api/v1/labels",
		},
		{
			name: "label values",
			call: func(srv *httptest.Server) (*mcp.CallToolResult, error) {
				return h.HandleLabelValues(context.Background(),
					toolRequest("loki_label_values", map[string]any{"url": srv.URL, "label": "app"}))
			},
			wantPath: "/loki/api/v1/label/app/values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, captured := newFakeLabelLoki(t, labelResponseBody)

			res, err := tt.call(srv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := resultText(t, res); got != "job\nlevel\n" {
				t.Errorf("output = %q, want %q", got, "job\nlevel\n")
			}
			if captured.path != tt.wantPath {
				t.Errorf("path = %q, want %q", captured.path, tt.wantPath)
			}
			if got, want := captured.query.Get("query"), `{namespace="prod"}`; got != want {
				t.Errorf("query = %q, want %q", got, want)
			}
			assertNanoTimestamps(t, captured.query)
		})
	}
}

// assertNanoTimestamps checks that start/end are UnixNano integers.
func assertNanoTimestamps(t *testing.T, q url.Values) {
	t.Helper()

	for _, key := range []string{"start", "end"} {
		v := q.Get(key)
		if len(v) != 19 {
			t.Errorf("%s = %q, want a 19-digit UnixNano timestamp", key, v)
		}
	}
}
