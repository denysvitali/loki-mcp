package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// TestLokiParams_applyDefaults pins the viper-backed fallback behaviour of
// applyDefaults. viper is a process-global, so these subtests must stay serial.
func TestLokiParams_applyDefaults(t *testing.T) {
	tests := []struct {
		name      string
		viperVals map[string]string
		in        LokiParams
		want      LokiParams
	}{
		{
			name:      "empty params take every viper value",
			viperVals: map[string]string{"url": "http://loki:3100", "username": "u", "password": "p", "token": "tok", "org-id": "org"},
			in:        LokiParams{},
			want: LokiParams{
				URL:      "http://loki:3100",
				Username: "u",
				Password: "p",
				Token:    "tok",
				Org:      "org",
				Format:   "raw",
			},
		},
		{
			name:      "explicit params win over viper",
			viperVals: map[string]string{"url": "http://loki:3100", "username": "u", "password": "p", "token": "tok", "org-id": "org"},
			in: LokiParams{
				URL:      "http://explicit:9999",
				Username: "eu",
				Password: "ep",
				Token:    "etok",
				Org:      "eorg",
				Format:   "json",
			},
			want: LokiParams{
				URL:      "http://explicit:9999",
				Username: "eu",
				Password: "ep",
				Token:    "etok",
				Org:      "eorg",
				Format:   "json",
			},
		},
		{
			name:      "no viper values leaves fields empty but defaults format",
			viperVals: nil,
			in:        LokiParams{},
			want:      LokiParams{Format: "raw"},
		},
		{
			name:      "format is only defaulted when empty",
			viperVals: nil,
			in:        LokiParams{Format: "text"},
			want:      LokiParams{Format: "text"},
		},
		{
			name:      "unrelated fields are untouched",
			viperVals: nil,
			in:        LokiParams{Query: `{job="api"}`, Label: "job", Limit: 5, Head: 2, Filter: "err", FilterCaseSensitive: true},
			want:      LokiParams{Query: `{job="api"}`, Label: "job", Limit: 5, Head: 2, Filter: "err", FilterCaseSensitive: true, Format: "raw"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			for k, v := range tt.viperVals {
				viper.Set(k, v)
			}

			got := tt.in
			got.applyDefaults()

			if got != tt.want {
				t.Errorf("applyDefaults() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestNewLokiClient pins the mapping from LokiParams to the SDK client.
func TestNewLokiClient(t *testing.T) {
	t.Parallel()

	params := &LokiParams{
		URL:      "http://loki:3100",
		Username: "u",
		Password: "p",
		Token:    "tok",
		Org:      "org",
	}

	c := newLokiClient(params)

	if c.Address != params.URL {
		t.Errorf("Address = %q, want %q", c.Address, params.URL)
	}
	if c.Username != params.Username {
		t.Errorf("Username = %q, want %q", c.Username, params.Username)
	}
	if c.Password != params.Password {
		t.Errorf("Password = %q, want %q", c.Password, params.Password)
	}
	if c.BearerToken != params.Token {
		t.Errorf("BearerToken = %q, want %q", c.BearerToken, params.Token)
	}
	if c.OrgID != params.Org {
		t.Errorf("OrgID = %q, want %q", c.OrgID, params.Org)
	}
}

func TestPingLokiTimeout(t *testing.T) {
	for _, bodyStarted := range []bool{false, true} {
		t.Run(fmt.Sprintf("body_started=%t", bodyStarted), func(t *testing.T) {
			canceled := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if bodyStarted {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"status":"success","data":[`))
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(canceled)
			}))
			defer srv.Close()
			started := time.Now()
			err := pingLokiWithTimeout(&LokiParams{URL: srv.URL}, 100*time.Millisecond)
			if err == nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected wrapped deadline error, got %v", err)
			}
			if !strings.Contains(err.Error(), "failed to reach Loki at "+srv.URL) {
				t.Fatalf("missing startup error context: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("ping exceeded bounded timeout: %s", elapsed)
			}
			select {
			case <-canceled:
			case <-time.After(2 * time.Second):
				t.Fatal("request was not canceled")
			}
		})
	}
}

func TestPingLokiRequest(t *testing.T) {
	for _, tc := range []struct {
		name, username, password, token, authorization string
		status                                         int
	}{
		{name: "empty labels", status: http.StatusOK},
		{name: "basic auth", username: "user", password: "pass", authorization: "Basic dXNlcjpwYXNz", status: http.StatusOK},
		{name: "bearer auth", token: "token", authorization: "Bearer token", status: http.StatusOK},
		{name: "rejected auth", token: "token", authorization: "Bearer token", status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/loki/api/v1/labels" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != tc.authorization {
					t.Errorf("authorization = %q, want %q", got, tc.authorization)
				}
				if r.Header.Get("X-Scope-OrgID") != "tenant" {
					t.Error("missing tenant header")
				}
				if r.URL.Query().Get("start") == "" || r.URL.Query().Get("end") == "" {
					t.Error("missing time range")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
			}))
			defer srv.Close()
			err := PingLoki(&LokiParams{URL: srv.URL, Username: tc.username, Password: tc.password, Token: tc.token, Org: "tenant"})
			if (err != nil) != (tc.status != http.StatusOK) {
				t.Fatalf("PingLoki returned %v for status %d", err, tc.status)
			}
		})
	}
}
