package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

// capturedRequest records what the fake Loki server saw.
type capturedRequest struct {
	path  string
	query url.Values
	auth  string
	orgID string
}

// newFakeLoki serves body with status for every request and records the last one.
func newFakeLoki(t *testing.T, status int, body string) (*httptest.Server, *capturedRequest) {
	t.Helper()

	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		captured.query = r.URL.Query()
		captured.auth = r.Header.Get("Authorization")
		captured.orgID = r.Header.Get("X-Scope-OrgID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

const labelResponseBody = `{"status":"success","data":["job","level"]}`

var (
	testStart = time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	testEnd   = time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
)

// TestLokiHTTPClient_Paths pins the URL each exported method targets, including
// path escaping of the label name.
func TestLokiHTTPClient_Paths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		call     func(c *LokiHTTPClient) (*loghttp.LabelResponse, error)
		wantPath string
	}{
		{
			name: "label names",
			call: func(c *LokiHTTPClient) (*loghttp.LabelResponse, error) {
				return c.ListLabelNamesWithQuery(context.Background(), "", testStart, testEnd)
			},
			wantPath: "/loki/api/v1/labels",
		},
		{
			name: "label values",
			call: func(c *LokiHTTPClient) (*loghttp.LabelResponse, error) {
				return c.ListLabelValuesWithQuery(context.Background(), "job", "", testStart, testEnd)
			},
			wantPath: "/loki/api/v1/label/job/values",
		},
		{
			// Characterization only: the label name is escaped with
			// url.PathEscape and then assigned to url.URL.Path, which escapes
			// it a second time on the wire. The server therefore decodes the
			// singly-escaped form rather than the literal label name.
			name: "label values double-escapes the label name",
			call: func(c *LokiHTTPClient) (*loghttp.LabelResponse, error) {
				return c.ListLabelValuesWithQuery(context.Background(), "we/ird name", "", testStart, testEnd)
			},
			wantPath: "/loki/api/v1/label/we%2Fird%20name/values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, captured := newFakeLoki(t, http.StatusOK, labelResponseBody)
			c := NewLokiHTTPClient(&LokiParams{URL: srv.URL})

			resp, err := tt.call(c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Data) != 2 || resp.Data[0] != "job" {
				t.Errorf("unexpected decoded response: %+v", resp.Data)
			}
			// captured.path is the server-side decoded path.
			if captured.path != tt.wantPath {
				t.Errorf("path = %q, want %q", captured.path, tt.wantPath)
			}
		})
	}
}

// TestLokiHTTPClient_QueryParams pins start/end as UnixNano and the optional
// query parameter.
func TestLokiHTTPClient_QueryParams(t *testing.T) {
	t.Parallel()

	t.Run("with query", func(t *testing.T) {
		t.Parallel()

		srv, captured := newFakeLoki(t, http.StatusOK, labelResponseBody)
		c := NewLokiHTTPClient(&LokiParams{URL: srv.URL})

		if _, err := c.ListLabelNamesWithQuery(context.Background(), `{namespace="prod"}`, testStart, testEnd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got, want := captured.query.Get("start"), strconv.FormatInt(testStart.UnixNano(), 10); got != want {
			t.Errorf("start = %q, want %q", got, want)
		}
		if got, want := captured.query.Get("end"), strconv.FormatInt(testEnd.UnixNano(), 10); got != want {
			t.Errorf("end = %q, want %q", got, want)
		}
		if got, want := captured.query.Get("query"), `{namespace="prod"}`; got != want {
			t.Errorf("query = %q, want %q", got, want)
		}
	})

	t.Run("empty query is omitted", func(t *testing.T) {
		t.Parallel()

		srv, captured := newFakeLoki(t, http.StatusOK, labelResponseBody)
		c := NewLokiHTTPClient(&LokiParams{URL: srv.URL})

		if _, err := c.ListLabelValuesWithQuery(context.Background(), "job", "", testStart, testEnd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := captured.query["query"]; ok {
			t.Errorf("expected no query parameter, got %q", captured.query.Get("query"))
		}
	})
}

// TestLokiHTTPClient_AuthHeaders pins basic auth, bearer token and org ID.
func TestLokiHTTPClient_AuthHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		params    LokiParams
		wantAuth  string
		wantOrgID string
	}{
		{
			name:     "basic auth",
			params:   LokiParams{Username: "user", Password: "pass"},
			wantAuth: "Basic dXNlcjpwYXNz",
		},
		{
			name:     "bearer token",
			params:   LokiParams{Token: "tok"},
			wantAuth: "Bearer tok",
		},
		{
			// The bearer token is set after basic auth, so it overwrites it.
			name:     "bearer token wins over basic auth",
			params:   LokiParams{Username: "user", Password: "pass", Token: "tok"},
			wantAuth: "Bearer tok",
		},
		{
			name:   "username without password is ignored",
			params: LokiParams{Username: "user"},
		},
		{
			name:      "org id",
			params:    LokiParams{Org: "tenant-1"},
			wantOrgID: "tenant-1",
		},
		{
			name: "no credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, captured := newFakeLoki(t, http.StatusOK, labelResponseBody)
			params := tt.params
			params.URL = srv.URL
			c := NewLokiHTTPClient(&params)

			if _, err := c.ListLabelNamesWithQuery(context.Background(), "", testStart, testEnd); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if captured.auth != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", captured.auth, tt.wantAuth)
			}
			if captured.orgID != tt.wantOrgID {
				t.Errorf("X-Scope-OrgID = %q, want %q", captured.orgID, tt.wantOrgID)
			}
		})
	}
}

// TestLokiHTTPClient_Errors pins the failure paths and their message prefixes.
func TestLokiHTTPClient_Errors(t *testing.T) {
	t.Parallel()

	t.Run("non-200 includes the body", func(t *testing.T) {
		t.Parallel()

		srv, _ := newFakeLoki(t, http.StatusBadRequest, "bad selector")
		c := NewLokiHTTPClient(&LokiParams{URL: srv.URL})

		_, err := c.ListLabelNamesWithQuery(context.Background(), "", testStart, testEnd)
		if err == nil {
			t.Fatal("expected error")
		}
		if want := "unexpected status 400: bad selector"; err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("non-200 on label values", func(t *testing.T) {
		t.Parallel()

		srv, _ := newFakeLoki(t, http.StatusInternalServerError, "boom")
		c := NewLokiHTTPClient(&LokiParams{URL: srv.URL})

		_, err := c.ListLabelValuesWithQuery(context.Background(), "job", "", testStart, testEnd)
		if err == nil {
			t.Fatal("expected error")
		}
		if want := "unexpected status 500: boom"; err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()

		srv, _ := newFakeLoki(t, http.StatusOK, "{not json")
		c := NewLokiHTTPClient(&LokiParams{URL: srv.URL})

		_, err := c.ListLabelNamesWithQuery(context.Background(), "", testStart, testEnd)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.HasPrefix(err.Error(), "failed to decode response: ") {
			t.Errorf("error = %q, want a decode failure", err.Error())
		}
	})

	t.Run("invalid base URL", func(t *testing.T) {
		t.Parallel()

		c := NewLokiHTTPClient(&LokiParams{URL: "http://%zz"})

		_, err := c.ListLabelNamesWithQuery(context.Background(), "", testStart, testEnd)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.HasPrefix(err.Error(), "invalid base URL: ") {
			t.Errorf("error = %q, want an invalid base URL failure", err.Error())
		}
	})

	t.Run("unreachable server", func(t *testing.T) {
		t.Parallel()

		srv, _ := newFakeLoki(t, http.StatusOK, labelResponseBody)
		addr := srv.URL
		srv.Close()

		c := NewLokiHTTPClient(&LokiParams{URL: addr})

		_, err := c.ListLabelValuesWithQuery(context.Background(), "job", "", testStart, testEnd)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.HasPrefix(err.Error(), "request failed: ") {
			t.Errorf("error = %q, want a request failure", err.Error())
		}
	})
}
