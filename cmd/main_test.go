package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/kaiko-ai/loki-mcp/internal/logging"
)

// logs captures everything the cmd package writes through internal/logging.
// logrus has no hook-removal API, so the hook is installed once for the whole
// binary and tests drain it with take(). That makes every test that inspects
// log output inherently serial: none of them may call t.Parallel().
var logs = &logCapture{}

// TestMain installs the log capture hook before any test runs.
//
// It also creates cobra's completion command up front. Cobra normally creates
// it inside the first Execute, and the generated script is written to the
// writer the root command had *at that moment*, captured in a closure. Left
// alone, that would be whichever test happened to call Execute first, making
// `completion`'s output depend on test order; pinning it to io.Discard here
// makes it deterministic and keeps the script out of the test log.
func TestMain(m *testing.M) {
	logging.AddHook(logs)

	rootCmd.SetOut(io.Discard)
	rootCmd.InitDefaultCompletionCmd()
	rootCmd.SetOut(nil)

	os.Exit(m.Run())
}

type logCapture struct {
	mu      sync.Mutex
	records []string
}

// Levels captures every level; the global logger's own level still decides
// which entries are emitted at all (default: info).
func (c *logCapture) Levels() []logrus.Level { return logrus.AllLevels }

func (c *logCapture) Fire(e *logrus.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, e.Level.String()+": "+e.Message)
	return nil
}

// take returns the lines logged since the previous call and clears the buffer.
func (c *logCapture) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.records
	c.records = nil
	return out
}

// pkgViperState re-applies to the global viper exactly what this package does
// at load time: the package-level initialiser in root.go (env prefix,
// AutomaticEnv, key replacer) plus every binding made by the init() functions
// in root.go and http.go.
func pkgViperState() {
	viper.SetEnvPrefix("LOKI")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		_ = viper.BindPFlag(f.Name, f)
	})
	httpCmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		// --port is not bound to viper by the package: see resolvePort.
		if f.Name == "port" || f.Name == "help" {
			return
		}
		_ = viper.BindPFlag(f.Name, f)
	})
}

// isolateViper puts the process-wide viper back into its load-time state
// before and after the test, so overrides and Reset() calls cannot leak
// between tests. viper is global: tests using this must stay serial.
func isolateViper(t *testing.T) {
	t.Helper()
	reset := func() {
		viper.Reset()
		pkgViperState()
	}
	reset()
	t.Cleanup(reset)
}

// setFlag sets a flag as if it had been passed on the command line (which is
// what makes viper prefer it over the environment) and restores both the value
// and the Changed marker afterwards.
func setFlag(t *testing.T, fs *pflag.FlagSet, name, value string) {
	t.Helper()

	f := fs.Lookup(name)
	require.NotNilf(t, f, "flag %q is not registered", name)

	oldValue, oldChanged := f.Value.String(), f.Changed
	t.Cleanup(func() {
		require.NoError(t, f.Value.Set(oldValue))
		f.Changed = oldChanged
	})
	require.NoError(t, fs.Set(name, value))
}

// captureStdout redirects the real os.Stdout for the duration of fn. The
// version command prints with fmt.Println, which resolves os.Stdout at call
// time, so this is the only way to observe it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	os.Stdout = orig
	require.NoError(t, w.Close())
	out := <-done
	require.NoError(t, r.Close())

	return out
}

// lokiLabelsResponse is the smallest valid reply to the label-names request
// that PingLoki issues.
const lokiLabelsResponse = `{"status":"success","data":["job"]}`

// fakeLoki serves lokiLabelsResponse and records the headers of the last
// request, so tests can assert which viper keys pingLoki forwards.
func fakeLoki(t *testing.T) (url string, lastReq *http.Header) {
	t.Helper()

	var mu sync.Mutex
	seen := &http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*seen = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(lokiLabelsResponse))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, seen
}

// closedLokiAddr returns the URL of a server that has already been shut down,
// so connecting to it is refused immediately.
func closedLokiAddr(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	return url
}
