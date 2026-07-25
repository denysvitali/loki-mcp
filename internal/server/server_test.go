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
	_, pattern := mux.Handler(httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
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
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
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
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	require.NoError(t, ln.Close())
	return port
}

// waitForServer reports whether addr accepts connections before the deadline,
// giving up early if the server goroutine has already failed.
func waitForServer(ctx context.Context, addr string, errCh <-chan error) error {
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			return fmt.Errorf("server exited before listening: %w", err)
		default:
		}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
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
		if err := waitForServer(t.Context(), addr, errCh); err == nil {
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
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				"http://"+addr+"/stream", strings.NewReader("{not json"))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Contains(t, string(body), "error")

			// /sse is served by default and gone with DisableSSE.
			sseReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
				"http://"+addr+"/sse", nil)
			require.NoError(t, err)
			sseResp, err := http.DefaultClient.Do(sseReq)
			require.NoError(t, err)
			if tt.disableSSE {
				_ = sseResp.Body.Close()
				assert.Equal(t, http.StatusNotFound, sseResp.StatusCode)
			} else {
				assert.Equal(t, http.StatusOK, sseResp.StatusCode)
				assert.Contains(t, sseResp.Header.Get("Content-Type"), "text/event-stream")
				_ = sseResp.Body.Close()
			}

			// SIGINT triggers graceful shutdown and RunHTTP returns nil.
			require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGINT))
			select {
			case err := <-errCh:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("RunHTTP did not return within 10s of SIGINT")
			}
		})
	}
}

// TestRunHTTPDrainsInFlightConnection pins the grace period from the side that
// must succeed: a connection that is still active when the signal arrives is
// drained rather than dropped, and RunHTTP still returns nil.
//
// The /sse stream is opened first and released only after the signal has been
// delivered, so the server is provably non-quiescent at the moment Shutdown
// starts — the case where Shutdown consults its context instead of returning
// straight away. It then goes idle well inside defaultShutdownTimeout, so the
// drain completes and the return value is nil.
//
// A shutdown context derived from the signal context would fail this: that
// context is already cancelled when the select wakes, so Shutdown would return
// context.Canceled immediately and the grace period would never apply. Rooting
// it at context.Background is what this test is guarding.
func TestRunHTTPDrainsInFlightConnection(t *testing.T) {
	addr, errCh := startRunHTTP(t, false)

	// t.Context() is cancelled during cleanup, which would close the stream
	// for us; this request has to outlive the signal, so root it elsewhere.
	sseReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+addr+"/sse", nil)
	require.NoError(t, err)
	//nolint:bodyclose // deliberately held open past the signal; the goroutine below closes it.
	sseResp, err := http.DefaultClient.Do(sseReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, sseResp.StatusCode)

	// Signal first, release the connection after: the ordering is what makes
	// the in-flight connection deterministic rather than a race.
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGINT))

	closerDone := make(chan struct{})
	go func() {
		defer close(closerDone)
		time.Sleep(150 * time.Millisecond)
		_ = sseResp.Body.Close()
	}()
	defer func() { <-closerDone }()

	select {
	case err := <-errCh:
		require.NoError(t, err, "shutdown must drain the in-flight connection and return nil")
	case <-time.After(10 * time.Second):
		t.Fatal("RunHTTP did not return within 10s of SIGINT")
	}
}

// TestServeShutdownDeadlineExceeded pins the other side: a request that
// outlives defaultShutdownTimeout is not waited for indefinitely. serve gives
// up after the budget and returns context.DeadlineExceeded unwrapped, having
// logged "HTTP server shutdown error: ...".
//
// This is the same thing a client holding /sse open across the signal sees,
// exercised through serve so the blocking is explicit. It costs a real 5s
// because defaultShutdownTimeout is a constant, and shortening it for the test
// would mean adding a configuration knob nothing in production sets.
func TestServeShutdownDeadlineExceeded(t *testing.T) {
	release := make(chan struct{})
	handlerReturned := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/block", func(w http.ResponseWriter, _ *http.Request) {
		defer close(handlerReturned)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	})

	addr := "127.0.0.1:" + freePort(t)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() { serveErr <- serve(ctx, srv, HTTPConfig{}) }()
	require.NoError(t, waitForServer(t.Context(), addr, serveErr))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+addr+"/block", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The handler never returns on its own, so the drain cannot complete.
	cancel()

	select {
	case err := <-serveErr:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not return after the shutdown deadline")
	}

	// Let everything unwind so goleak stays happy.
	close(release)
	<-handlerReturned
	_ = resp.Body.Close()
	require.NoError(t, srv.Close())
}
