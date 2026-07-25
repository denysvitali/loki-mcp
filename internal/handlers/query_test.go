package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// queryRangeBody is a canned /loki/api/v1/query_range response with three
// entries spread over two streams.
const queryRangeBody = `{
  "status": "success",
  "data": {
    "resultType": "streams",
    "result": [
      {
        "stream": {"job": "api"},
        "values": [
          ["1705312800000000000", "alpha ERROR one"],
          ["1705312801000000000", "beta two"]
        ]
      },
      {
        "stream": {"job": "web"},
        "values": [
          ["1705312802000000000", "gamma error three"]
        ]
      }
    ],
    "stats": {}
  }
}`

// newFakeQueryLoki serves queryRangeBody on /loki/api/v1/query_range and
// records the query parameters it was called with.
func newFakeQueryLoki(t *testing.T, body string) (*httptest.Server, *url.Values) {
	t.Helper()

	captured := &url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func queryRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "loki_query", Arguments: args},
	}
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()

	if res == nil {
		t.Fatal("nil tool result")
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected exactly one content block, got %d", len(res.Content))
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return text.Text
}

// TestHandleLokiQuery_Formats pins the end-to-end output of each format against
// a canned Loki response.
func TestHandleLokiQuery_Formats(t *testing.T) {
	srv, _ := newFakeQueryLoki(t, queryRangeBody)

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "default format is raw",
			format: "",
			want: "2024-01-15T10:00:00Z {job=\"api\"} alpha ERROR one\n" +
				"2024-01-15T10:00:01Z {job=\"api\"} beta two\n" +
				"2024-01-15T10:00:02Z {job=\"web\"} gamma error three\n",
		},
		{
			name:   "explicit raw",
			format: "raw",
			want: "2024-01-15T10:00:00Z {job=\"api\"} alpha ERROR one\n" +
				"2024-01-15T10:00:01Z {job=\"api\"} beta two\n" +
				"2024-01-15T10:00:02Z {job=\"web\"} gamma error three\n",
		},
		{
			name:   "text",
			format: "text",
			want: "Found 2 streams:\n\n" +
				"Stream {job=\"api\"} 1:\n" +
				"[2024-01-15T10:00:00Z] alpha ERROR one\n" +
				"[2024-01-15T10:00:01Z] beta two\n\n" +
				"Stream {job=\"web\"} 2:\n" +
				"[2024-01-15T10:00:02Z] gamma error three\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"query": `{job=~".+"}`, "url": srv.URL}
			if tt.format != "" {
				args["format"] = tt.format
			}

			res, err := HandleLokiQuery(context.Background(), queryRequest(args))
			if err != nil {
				t.Fatalf("HandleLokiQuery returned error: %v", err)
			}
			if got := resultText(t, res); got != tt.want {
				t.Errorf("output =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}

	t.Run("json", func(t *testing.T) {
		args := map[string]any{"query": `{job=~".+"}`, "url": srv.URL, "format": "json"}

		res, err := HandleLokiQuery(context.Background(), queryRequest(args))
		if err != nil {
			t.Fatalf("HandleLokiQuery returned error: %v", err)
		}
		got := resultText(t, res)
		if !strings.HasPrefix(got, "{\n  \"status\": \"success\",") {
			t.Errorf("expected indented JSON response, got:\n%s", got)
		}
		if !strings.Contains(got, "alpha ERROR one") {
			t.Errorf("expected log lines in JSON output, got:\n%s", got)
		}
	})

	t.Run("unsupported format", func(t *testing.T) {
		args := map[string]any{"query": `{job=~".+"}`, "url": srv.URL, "format": "xml"}

		_, err := HandleLokiQuery(context.Background(), queryRequest(args))
		if err == nil {
			t.Fatal("expected error")
		}
		want := "failed to format results: " + unsupportedFormatErr
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})
}

// TestHandleLokiQuery_RequestParams pins what the handler sends to Loki.
func TestHandleLokiQuery_RequestParams(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		wantQuery string
		wantLimit string
	}{
		{
			name:      "defaults",
			args:      map[string]any{"query": `{job="api"}`},
			wantQuery: `{job="api"}`,
			wantLimit: "100",
		},
		{
			name:      "explicit limit",
			args:      map[string]any{"query": `{job="api"}`, "limit": float64(7)},
			wantQuery: `{job="api"}`,
			wantLimit: "7",
		},
		{
			// JSON numbers are float64 and are truncated, not rounded.
			name:      "fractional limit truncates",
			args:      map[string]any{"query": `{job="api"}`, "limit": 2.7},
			wantQuery: `{job="api"}`,
			wantLimit: "2",
		},
		{
			name:      "zero limit falls back to the default",
			args:      map[string]any{"query": `{job="api"}`, "limit": float64(0)},
			wantQuery: `{job="api"}`,
			wantLimit: "100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, captured := newFakeQueryLoki(t, queryRangeBody)
			args := map[string]any{"url": srv.URL}
			for k, v := range tt.args {
				args[k] = v
			}

			if _, err := HandleLokiQuery(context.Background(), queryRequest(args)); err != nil {
				t.Fatalf("HandleLokiQuery returned error: %v", err)
			}

			if got := captured.Get("query"); got != tt.wantQuery {
				t.Errorf("query = %q, want %q", got, tt.wantQuery)
			}
			if got := captured.Get("limit"); got != tt.wantLimit {
				t.Errorf("limit = %q, want %q", got, tt.wantLimit)
			}
			if got := captured.Get("direction"); got != "BACKWARD" {
				t.Errorf("direction = %q, want BACKWARD", got)
			}
		})
	}
}

// TestHandleLokiQuery_TimeRange pins start/end handling and their error strings.
func TestHandleLokiQuery_TimeRange(t *testing.T) {
	t.Run("explicit start and end are forwarded", func(t *testing.T) {
		srv, captured := newFakeQueryLoki(t, queryRangeBody)
		args := map[string]any{
			"query": `{job="api"}`,
			"url":   srv.URL,
			"start": "2024-01-15T09:00:00Z",
			"end":   "2024-01-15T10:00:00Z",
		}

		if _, err := HandleLokiQuery(context.Background(), queryRequest(args)); err != nil {
			t.Fatalf("HandleLokiQuery returned error: %v", err)
		}

		if got, want := captured.Get("start"), fmt.Sprintf("%d", testStart.UnixNano()); got != want {
			t.Errorf("start = %q, want %q", got, want)
		}
		if got, want := captured.Get("end"), fmt.Sprintf("%d", testEnd.UnixNano()); got != want {
			t.Errorf("end = %q, want %q", got, want)
		}
	})

	errTests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "invalid start",
			args: map[string]any{"query": `{job="api"}`, "start": "nope"},
			want: "invalid start time: unsupported time format: nope",
		},
		{
			name: "invalid end",
			args: map[string]any{"query": `{job="api"}`, "end": "nope"},
			want: "invalid end time: unsupported time format: nope",
		},
	}

	for _, tt := range errTests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newFakeQueryLoki(t, queryRangeBody)
			args := map[string]any{"url": srv.URL}
			for k, v := range tt.args {
				args[k] = v
			}

			_, err := HandleLokiQuery(context.Background(), queryRequest(args))
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.want {
				t.Errorf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

// TestHandleLokiQuery_PostProcessing pins the keyword filter and head/tail
// behaviour, including the re-grouping that happens on the way back to streams.
func TestHandleLokiQuery_PostProcessing(t *testing.T) {
	tests := []struct {
		name  string
		args  map[string]any
		want  []string
		lines int
	}{
		{
			name:  "case-insensitive filter matches both cases",
			args:  map[string]any{"filter": "error"},
			want:  []string{"alpha ERROR one", "gamma error three"},
			lines: 2,
		},
		{
			name:  "case-sensitive filter",
			args:  map[string]any{"filter": "ERROR", "filter_case_sensitive": true},
			want:  []string{"alpha ERROR one"},
			lines: 1,
		},
		{
			name:  "head returns the oldest entries",
			args:  map[string]any{"head": float64(2)},
			want:  []string{"alpha ERROR one", "beta two"},
			lines: 2,
		},
		{
			name:  "tail returns the newest entries",
			args:  map[string]any{"tail": float64(1)},
			want:  []string{"gamma error three"},
			lines: 1,
		},
		{
			name:  "filter and head combine",
			args:  map[string]any{"filter": "error", "head": float64(1)},
			want:  []string{"alpha ERROR one"},
			lines: 1,
		},
		{
			name:  "filter matching nothing yields the empty message",
			args:  map[string]any{"filter": "no-such-line"},
			want:  []string{"No logs found matching the query"},
			lines: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newFakeQueryLoki(t, queryRangeBody)
			args := map[string]any{"query": `{job=~".+"}`, "url": srv.URL}
			for k, v := range tt.args {
				args[k] = v
			}

			res, err := HandleLokiQuery(context.Background(), queryRequest(args))
			if err != nil {
				t.Fatalf("HandleLokiQuery returned error: %v", err)
			}

			got := resultText(t, res)
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if len(lines) != tt.lines {
				t.Fatalf("expected %d output lines, got %d:\n%s", tt.lines, len(lines), got)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("expected output to contain %q, got:\n%s", want, got)
				}
			}
		})
	}
}

// TestHandleLokiQuery_HeadTailRejected pins the mutual-exclusion error.
func TestHandleLokiQuery_HeadTailRejected(t *testing.T) {
	t.Parallel()

	args := map[string]any{"query": `{job="test"}`, "head": float64(5), "tail": float64(5)}

	_, err := HandleLokiQuery(context.Background(), queryRequest(args))
	if err == nil {
		t.Fatal("expected error")
	}
	if want := "head and tail parameters are mutually exclusive"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestHandleLokiQuery_BindError pins the argument-binding error prefix.
func TestHandleLokiQuery_BindError(t *testing.T) {
	t.Parallel()

	args := map[string]any{"query": float64(42)}

	_, err := HandleLokiQuery(context.Background(), queryRequest(args))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "invalid arguments: ") {
		t.Errorf("error = %q, want an argument binding failure", err.Error())
	}
}

// TestHandleLokiQuery_QueryExecutionError pins the wrapper around SDK failures.
func TestHandleLokiQuery_QueryExecutionError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "parse error", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	args := map[string]any{"query": `{job="api"}`, "url": srv.URL}

	_, err := HandleLokiQuery(context.Background(), queryRequest(args))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "query execution failed: ") {
		t.Errorf("error = %q, want a query execution failure", err.Error())
	}
}

// TestHandleLokiQuery_QueryFilterMerge pins that a configured query filter is
// ANDed into the client's stream selector before the query is sent.
// It mutates package-global filter state, so it must stay serial.
func TestHandleLokiQuery_QueryFilterMerge(t *testing.T) {
	ResetFilterConfig()
	t.Cleanup(ResetFilterConfig)

	if err := InitializeFilterConfig(`{namespace="prod"}`); err != nil {
		t.Fatalf("InitializeFilterConfig failed: %v", err)
	}

	t.Run("merged into the client query", func(t *testing.T) {
		srv, captured := newFakeQueryLoki(t, queryRangeBody)
		args := map[string]any{"query": `{job="api"}`, "url": srv.URL}

		if _, err := HandleLokiQuery(context.Background(), queryRequest(args)); err != nil {
			t.Fatalf("HandleLokiQuery returned error: %v", err)
		}

		if got, want := captured.Get("query"), `{namespace="prod", job="api"}`; got != want {
			t.Errorf("query = %q, want %q", got, want)
		}
	})

	t.Run("unparseable client query is rejected", func(t *testing.T) {
		srv, _ := newFakeQueryLoki(t, queryRangeBody)
		args := map[string]any{"query": "not a logql query", "url": srv.URL}

		_, err := HandleLokiQuery(context.Background(), queryRequest(args))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.HasPrefix(err.Error(), "failed to apply query filter: ") {
			t.Errorf("error = %q, want a query filter failure", err.Error())
		}
	})
}

// TestHandleLokiQuery_SecretFiltering pins that lines containing secrets are
// omitted and that the note is appended for non-JSON formats only.
// It mutates package-global secret filter state, so it must stay serial.
func TestHandleLokiQuery_SecretFiltering(t *testing.T) {
	body := `{
  "status": "success",
  "data": {
    "resultType": "streams",
    "result": [
      {
        "stream": {"job": "api"},
        "values": [
          ["1705312800000000000", "harmless line"],
          ["1705312801000000000", "api_key = \"` + testGenericAPIKey + `\""]
        ]
      }
    ],
    "stats": {}
  }
}`

	if err := InitializeSecretFilterConfig(true); err != nil {
		t.Fatalf("InitializeSecretFilterConfig failed: %v", err)
	}
	t.Cleanup(ResetSecretFilterConfig)

	t.Run("raw appends the omission note", func(t *testing.T) {
		srv, _ := newFakeQueryLoki(t, body)
		args := map[string]any{"query": `{job="api"}`, "url": srv.URL}

		res, err := HandleLokiQuery(context.Background(), queryRequest(args))
		if err != nil {
			t.Fatalf("HandleLokiQuery returned error: %v", err)
		}

		want := "2024-01-15T10:00:00Z {job=\"api\"} harmless line\n" +
			"\n1 line omitted due to detected secrets"
		if got := resultText(t, res); got != want {
			t.Errorf("output = %q, want %q", got, want)
		}
	})

	t.Run("json carries the note in warnings instead", func(t *testing.T) {
		srv, _ := newFakeQueryLoki(t, body)
		args := map[string]any{"query": `{job="api"}`, "url": srv.URL, "format": "json"}

		res, err := HandleLokiQuery(context.Background(), queryRequest(args))
		if err != nil {
			t.Fatalf("HandleLokiQuery returned error: %v", err)
		}

		got := resultText(t, res)
		if !strings.Contains(got, "1 line omitted due to detected secrets") {
			t.Errorf("expected omission note in warnings, got:\n%s", got)
		}
		if strings.Contains(got, testGenericAPIKey) {
			t.Errorf("secret leaked into JSON output:\n%s", got)
		}
		if strings.HasSuffix(got, "detected secrets") {
			t.Errorf("json output must not have the note appended after the payload:\n%s", got)
		}
	})
}
