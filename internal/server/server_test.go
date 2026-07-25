package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
)

func TestNewRegistersExactlyThreeTools(t *testing.T) {
	s := New("test-version", handlers.Config{})

	tools := s.ListTools()
	require.Len(t, tools, 3)
	assert.Contains(t, tools, "loki_query")
	assert.Contains(t, tools, "loki_label_names")
	assert.Contains(t, tools, "loki_label_values")
}

// routePattern reports the ServeMux pattern that path resolves to, or "" when
// nothing is registered for it.
func routePattern(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	mux, ok := h.(*http.ServeMux)
	require.True(t, ok, "newMux must return an *http.ServeMux")
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
	return pattern
}

func TestNewMuxRouting(t *testing.T) {
	tests := []struct {
		name     string
		cfg      HTTPConfig
		patterns map[string]string
	}{
		{
			name: "sse enabled by default",
			cfg:  HTTPConfig{},
			patterns: map[string]string{
				"/stream":  "/stream",
				"/sse":     "/sse",
				"/mcp":     "/mcp",
				"/unknown": "",
			},
		},
		{
			name: "sse disabled",
			cfg:  HTTPConfig{DisableSSE: true},
			patterns: map[string]string{
				"/stream":  "/stream",
				"/sse":     "",
				"/mcp":     "",
				"/unknown": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newMux(New("test-version", handlers.Config{}), tt.cfg)
			for path, want := range tt.patterns {
				assert.Equal(t, want, routePattern(t, mux, path), "route for %s", path)
			}
		})
	}
}

// TestServeContextAlreadyDone covers the shutdown path without racing a
// signal: serve must shut the server down and join its goroutine even when
// ctx is done before ListenAndServe has had a chance to start.
func TestServeContextAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := &http.Server{
		Addr:              "127.0.0.1:0",
		Handler:           http.NewServeMux(),
		ReadHeaderTimeout: time.Second,
	}

	require.NoError(t, serve(ctx, srv, HTTPConfig{}))
}

// TestServeListenError covers the failure path: a listen error is wrapped and
// returned, and serve still joins its goroutine.
func TestServeListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           http.NewServeMux(),
		ReadHeaderTimeout: time.Second,
	}

	// context.Background never fires, so the only way out is the error path.
	err = serve(context.Background(), srv, HTTPConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP server error: ")
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	require.NoError(t, ln.Close())
	return port
}

// waitForServer reports whether addr accepts connections before the deadline,
// giving up early if the server goroutine has already failed.
func waitForServer(addr string, errCh <-chan error) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			return fmt.Errorf("server exited before listening: %w", err)
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("server at %s did not start listening", addr)
}

// startRunHTTP starts RunHTTP on a free port and returns once it is listening.
//
// freePort can only reserve a port by closing its listener, so another process
// may take it in between. That is a property of RunHTTP's string port
// parameter, not a bug worth changing production code for, so retry a few
// times instead.
func startRunHTTP(t *testing.T, disableSSE bool) (addr string, errCh chan error) {
	t.Helper()
	const attempts = 5
	for i := range attempts {
		port := freePort(t)
		addr = "127.0.0.1:" + port
		errCh = make(chan error, 1)
		go func() {
			errCh <- RunHTTP("test-version", HTTPConfig{
				Host:       "127.0.0.1",
				Port:       port,
				DisableSSE: disableSSE,
			}, handlers.Config{})
		}()
		if err := waitForServer(addr, errCh); err == nil {
			return addr, errCh
		} else if i == attempts-1 {
			t.Fatalf("RunHTTP never came up in %d attempts: %v", attempts, err)
		}
	}
	panic("unreachable")
}

// TestRunHTTP pins the observable behaviour of the current RunHTTP: endpoint
// wiring (with and without SSE), HTTP-level MCP error responses, and graceful
// shutdown on SIGINT.
func TestRunHTTP(t *testing.T) {
	tests := []struct {
		name       string
		disableSSE bool
	}{
		{"sse enabled", false},
		{"sse disabled", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, errCh := startRunHTTP(t, tt.disableSSE)

			// Bogus POST to /stream must produce an HTTP-level MCP error
			// response, not a panic or a hang.
			resp, err := http.Post("http://"+addr+"/stream", "application/json", strings.NewReader("{not json"))
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Contains(t, string(body), "error")

			// /sse is served by default and gone with DisableSSE.
			sseResp, err := http.Get("http://" + addr + "/sse")
			require.NoError(t, err)
			if tt.disableSSE {
				_ = sseResp.Body.Close()
				assert.Equal(t, http.StatusNotFound, sseResp.StatusCode)
			} else {
				assert.Equal(t, http.StatusOK, sseResp.StatusCode)
				assert.Contains(t, sseResp.Header.Get("Content-Type"), "text/event-stream")
				_ = sseResp.Body.Close()
			}

			// SIGINT triggers graceful shutdown and RunHTTP returns.
			//
			// The returned error is nil when every connection is already idle
			// by the time Shutdown runs. It is context.Canceled when one is
			// not: the shutdown context inherits the cancelled signal context,
			// so Shutdown gives up immediately instead of waiting out
			// defaultShutdownTimeout. That is pre-existing behaviour (it
			// predates the newMux/serve split) and it is what makes the
			// "sse enabled" case racy, so accept either outcome here rather
			// than pin a race.
			require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGINT))
			select {
			case err := <-errCh:
				if err != nil {
					require.ErrorIs(t, err, context.Canceled)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("RunHTTP did not return within 10s of SIGINT")
			}
		})
	}
}
