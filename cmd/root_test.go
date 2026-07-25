package cmd

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/logging"
)

// cobraBuiltins are the commands cobra injects on its own the first time
// Execute runs; they are not registered by this package.
var cobraBuiltins = map[string]bool{"help": true, "completion": true}

// TestSubcommandsRegistered pins which subcommands exist and their Use lines.
func TestSubcommandsRegistered(t *testing.T) {
	want := map[string]string{
		"stdio":   "stdio",
		"http":    "http",
		"version": "version",
	}

	got := map[string]string{}
	for _, c := range rootCmd.Commands() {
		if cobraBuiltins[c.Name()] {
			continue
		}
		got[c.Name()] = c.Use
	}
	assert.Equal(t, want, got)

	assert.Equal(t, "loki-mcp", rootCmd.Use)
	assert.Equal(t, "MCP server for Grafana Loki", rootCmd.Short)
}

// TestPersistentPreRunEAppliesToEverySubcommand pins that no subcommand
// overrides the root hook, so all of them - including version - run the full
// setup chain. See TestVersionRequiresLoki for the consequence.
func TestPersistentPreRunEAppliesToEverySubcommand(t *testing.T) {
	require.NotNil(t, rootCmd.PersistentPreRunE)

	for _, c := range rootCmd.Commands() {
		t.Run(c.Name(), func(t *testing.T) {
			assert.Nil(t, c.PersistentPreRunE, "would shadow the root hook")
			assert.Nil(t, c.PreRunE)
		})
	}
}

// TestInitLogging pins that the logger is configured from the resolved flags
// and that an unparseable level is reported verbatim from logrus.
func TestInitLogging(t *testing.T) {
	isolateViper(t)
	// initLogging mutates the process-wide logger; put it back so later tests
	// still see info-level entries.
	t.Cleanup(func() { require.NoError(t, logging.Init("info", "text")) })

	viper.Set("log-level", "debug")
	viper.Set("log-format", "json")
	require.NoError(t, initLogging())

	viper.Set("log-level", "nope")
	err := initLogging()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `not a valid logrus Level: "nope"`)
}

// TestBuildHandlerConfig pins how viper's secret-filter and query-filter keys
// turn into a handlers.Config, and the log line each decision emits.
func TestBuildHandlerConfig(t *testing.T) {
	t.Run("secret filter on, no query filter", func(t *testing.T) {
		isolateViper(t)
		viper.Set("secret-filter", true)
		logs.take()

		cfg, err := buildHandlerConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.Detector, "detector is built when filtering is on")
		assert.Nil(t, cfg.QueryFilter)
		assert.Equal(t, []string{"info: Secret filtering enabled"}, logs.take())
	})

	t.Run("secret filter off warns", func(t *testing.T) {
		isolateViper(t)
		viper.Set("secret-filter", false)
		logs.take()

		cfg, err := buildHandlerConfig()
		require.NoError(t, err)
		assert.Nil(t, cfg.Detector)
		assert.Equal(t, []string{"warning: Secret filtering disabled"}, logs.take())
	})

	t.Run("query filter is parsed and logged", func(t *testing.T) {
		isolateViper(t)
		viper.Set("secret-filter", true)
		viper.Set("query-filter", `{namespace="prod"}`)
		logs.take()

		cfg, err := buildHandlerConfig()
		require.NoError(t, err)
		require.Len(t, cfg.QueryFilter, 1)
		assert.Equal(t, "namespace", cfg.QueryFilter[0].Name)
		assert.Equal(t, "prod", cfg.QueryFilter[0].Value)
		assert.Equal(t, []string{
			"info: Secret filtering enabled",
			`info: Query filter configured: {namespace="prod"}`,
		}, logs.take())
	})

	t.Run("invalid query filter propagates and returns a zero config", func(t *testing.T) {
		isolateViper(t)
		viper.Set("secret-filter", true)
		viper.Set("query-filter", "{not a selector")
		logs.take()

		cfg, err := buildHandlerConfig()
		require.Error(t, err)
		assert.Equal(t, handlers.Config{}, cfg)
		// The secret-filter decision is deliberately logged before the query
		// filter is validated, so it survives the failure; the query-filter
		// line never appears.
		assert.Equal(t, []string{"info: Secret filtering enabled"}, logs.take())
	})
}

// TestPingLoki pins which viper keys reach Loki and how failures are wrapped.
func TestPingLoki(t *testing.T) {
	t.Run("basic auth and org id are forwarded", func(t *testing.T) {
		isolateViper(t)
		url, header := fakeLoki(t)
		viper.Set("url", url)
		viper.Set("username", "user")
		viper.Set("password", "pass")
		viper.Set("org-id", "tenant-a")
		logs.take()

		require.NoError(t, pingLoki())

		assert.Equal(t, "tenant-a", header.Get("X-Scope-OrgID"))
		user, pass, ok := (&http.Request{Header: *header}).BasicAuth()
		require.True(t, ok, "expected a basic auth header")
		assert.Equal(t, "user", user)
		assert.Equal(t, "pass", pass)
		assert.Equal(t, []string{"info: Loki reachable at " + url}, logs.take())
	})

	t.Run("bearer token is forwarded", func(t *testing.T) {
		isolateViper(t)
		url, header := fakeLoki(t)
		viper.Set("url", url)
		viper.Set("token", "s3cret")

		require.NoError(t, pingLoki())
		assert.Equal(t, "Bearer s3cret", header.Get("Authorization"))
	})

	t.Run("unreachable Loki is wrapped", func(t *testing.T) {
		isolateViper(t)
		viper.Set("url", closedLokiAddr(t))
		logs.take()

		err := pingLoki()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loki connectivity check failed: ")
		assert.Empty(t, logs.take(), "no reachability line on failure")
	})
}

// TestPersistentPreRunESuccess runs the real hook end to end against a fake
// Loki, with the OTel SDK disabled so Initialize installs no exporters.
func TestPersistentPreRunESuccess(t *testing.T) {
	isolateViper(t)
	t.Setenv("OTEL_SDK_DISABLED", "true")

	url, _ := fakeLoki(t)
	viper.Set("url", url)
	viper.Set("secret-filter", true)

	prev := handlerConfig
	t.Cleanup(func() { handlerConfig = prev })
	handlerConfig = handlers.Config{}

	require.NoError(t, rootCmd.PersistentPreRunE(rootCmd, nil))
	assert.NotNil(t, handlerConfig.Detector, "the hook publishes the built config")
}

// TestVersionRequiresLoki pins a known wart, reported in the audit and not
// fixed here: the root hook pings Loki before every subcommand, so
// `loki-mcp version` fails when Loki is down and prints nothing.
func TestVersionRequiresLoki(t *testing.T) {
	isolateViper(t)
	t.Setenv("OTEL_SDK_DISABLED", "true")
	viper.Set("url", closedLokiAddr(t))

	out := captureStdout(t, func() {
		_, _, err := executeRoot(t, "version")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loki connectivity check failed: ")
	})
	assert.Empty(t, out, "the subcommand never runs")
}

// TestRootRunEIsStdio pins that a bare `loki-mcp` is literally the stdio
// command: rootCmd is runnable, and its RunE is the same function stdioCmd
// uses. Before FINDINGS.md §1 was fixed rootCmd had no Run at all, so the bare
// invocation printed help and exited 0 without starting any server.
func TestRootRunEIsStdio(t *testing.T) {
	require.NotNil(t, rootCmd.RunE, "a bare invocation must be runnable")
	assert.Nil(t, rootCmd.Run, "RunE, not Run: startup failures must be errors")

	want := reflect.ValueOf(runStdio).Pointer()
	assert.Equal(t, want, reflect.ValueOf(rootCmd.RunE).Pointer(),
		"the bare invocation must run stdio")
	assert.Equal(t, want, reflect.ValueOf(stdioCmd.RunE).Pointer(),
		"`loki-mcp stdio` must stay identical to the bare invocation")
}

// TestBareInvocationServes pins the observable half of the same fix: with no
// subcommand the binary enters the serving path — proven here by the Loki
// connectivity check failing — instead of printing help and exiting 0.
func TestBareInvocationServes(t *testing.T) {
	isolateViper(t)
	t.Setenv("OTEL_SDK_DISABLED", "true")
	viper.Set("url", closedLokiAddr(t))

	out, _, err := executeRoot(t)

	require.Error(t, err, "a bare invocation must not exit 0 having started nothing")
	assert.Contains(t, err.Error(), "loki connectivity check failed: ")
	assert.NotContains(t, out, "Use subcommands to pick a mode explicitly",
		"the long help is not the bare invocation's output any more")
}

// TestUnknownSubcommandStillFails pins that making the root runnable did not
// turn a typo into a silently started stdio server.
func TestUnknownSubcommandStillFails(t *testing.T) {
	isolateViper(t)

	_, _, err := executeRoot(t, "nosuchcommand")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown command "nosuchcommand" for "loki-mcp"`)
}

// executeRoot runs the root command with args and returns what cobra wrote
// through its out writer (command output and the usage block) and its err
// writer (the "Error: …" line), restoring the mutated command state
// afterwards.
//
// The empty argument list is passed as a non-nil slice on purpose: cobra falls
// back to os.Args when SetArgs was given nil, which under `go test` means the
// test binary's own flags.
func executeRoot(t *testing.T, args ...string) (out, errOut string, err error) {
	t.Helper()

	prev := handlerConfig
	t.Cleanup(func() {
		handlerConfig = prev
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	if args == nil {
		args = []string{}
	}

	var outBuf, errBuf bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)

	err = rootCmd.Execute()

	return outBuf.String(), errBuf.String(), err
}
