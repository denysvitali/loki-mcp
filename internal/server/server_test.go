package server

import (
	"io"
	"net"
	"net/http"
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

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	require.NoError(t, ln.Close())
	return port
}

func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not start listening", addr)
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
			port := freePort(t)
			addr := "127.0.0.1:" + port

			errCh := make(chan error, 1)
			go func() {
				errCh <- RunHTTP("test-version", HTTPConfig{
					Host:       "127.0.0.1",
					Port:       port,
					DisableSSE: tt.disableSSE,
				}, handlers.Config{})
			}()
			waitForServer(t, addr)

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
