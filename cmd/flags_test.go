package cmd

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnvPrefixIsSetBeforeFlagRegistration pins the reason root.go configures
// viper from a package-level `var _ = func() bool {...}()` instead of an
// init(): envFlag reads viper.GetEnvPrefix() while registering, and package
// variables are initialised before any init() runs. Were it an init(), Go's
// intra-package ordering (by file name: http.go before root.go) would leave
// httpCmd's --host usage reading "[env: HOST]".
func TestEnvPrefixIsSetBeforeFlagRegistration(t *testing.T) {
	assert.Equal(t, "LOKI", viper.GetEnvPrefix())
	assert.Equal(t,
		`LogQL stream selector to restrict all queries (e.g., {namespace="prod"}) [env: LOKI_QUERY_FILTER]`,
		rootCmd.PersistentFlags().Lookup("query-filter").Usage)
	assert.Equal(t,
		"HTTP server bind address [env: LOKI_HOST]",
		httpCmd.Flags().Lookup("host").Usage)
}

// TestRootPersistentFlags pins the full public surface of the root command's
// flags: name, shorthand, default and the exact usage string --help renders.
func TestRootPersistentFlags(t *testing.T) {
	tests := []struct {
		name      string
		shorthand string
		def       string
		usage     string
	}{
		{
			name: "query-filter", shorthand: "f", def: "",
			usage: `LogQL stream selector to restrict all queries (e.g., {namespace="prod"}) [env: LOKI_QUERY_FILTER]`,
		},
		{
			name: "secret-filter", shorthand: "", def: "true",
			usage: "Scan returned log lines for secrets and omit matching lines [env: LOKI_SECRET_FILTER]",
		},
		{
			name: "otel-stderr", shorthand: "", def: "false",
			usage: "Enable OpenTelemetry console exporters on stderr [env: LOKI_OTEL_STDERR]",
		},
		{
			name: "log-level", shorthand: "l", def: "info",
			usage: "Log level: debug, info, warn, error [env: LOKI_LOG_LEVEL]",
		},
		{
			name: "log-format", shorthand: "", def: "text",
			usage: "Log format: text, json [env: LOKI_LOG_FORMAT]",
		},
		{
			name: "url", shorthand: "u", def: "http://localhost:3100",
			usage: "Loki server URL [env: LOKI_URL]",
		},
		{
			name: "org-id", shorthand: "", def: "",
			usage: "Loki org ID for multi-tenant setups (X-Scope-OrgID) [env: LOKI_ORG_ID]",
		},
		{
			name: "username", shorthand: "", def: "",
			usage: "Basic auth username [env: LOKI_USERNAME]",
		},
		{
			name: "password", shorthand: "", def: "",
			usage: "Basic auth password [env: LOKI_PASSWORD]",
		},
		{
			name: "token", shorthand: "", def: "",
			usage: "Bearer token [env: LOKI_TOKEN]",
		},
	}

	pf := rootCmd.PersistentFlags()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := pf.Lookup(tt.name)
			require.NotNil(t, f)
			assert.Equal(t, tt.shorthand, f.Shorthand)
			assert.Equal(t, tt.def, f.DefValue)
			assert.Equal(t, tt.usage, f.Usage)
		})
	}

	var registered []string
	pf.VisitAll(func(f *pflag.Flag) { registered = append(registered, f.Name) })
	want := make([]string, 0, len(tests))
	for _, tt := range tests {
		want = append(want, tt.name)
	}
	assert.ElementsMatch(t, want, registered, "unexpected persistent flag set")
}

// TestEnvVarNameDerivation pins how a flag name becomes an environment
// variable name in the usage suffix: upper-cased, dashes to underscores,
// prefixed with viper's env prefix. This must stay in step with viper's
// AutomaticEnv + SetEnvKeyReplacer configuration, or --help lies.
func TestEnvVarNameDerivation(t *testing.T) {
	isolateViper(t)

	tests := []struct {
		flag string
		want string
	}{
		{"url", "LOKI_URL"},
		{"org-id", "LOKI_ORG_ID"},
		{"query-filter", "LOKI_QUERY_FILTER"},
		{"a-b-c", "LOKI_A_B_C"},
	}

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			envFlag(fs, tt.flag, "", "", "Usage")
			assert.Equal(t, "Usage [env: "+tt.want+"]", fs.Lookup(tt.flag).Usage)

			boolName := tt.flag + "-bool"
			envBoolFlag(fs, boolName, "", false, "Usage")
			assert.Equal(t, "Usage [env: "+tt.want+"_BOOL]", fs.Lookup(boolName).Usage)
		})
	}
}

// TestEnvVarNameWithoutPrefix pins the prefix-less branch: with no env prefix
// configured the suffix carries the bare name and no leading underscore.
func TestEnvVarNameWithoutPrefix(t *testing.T) {
	isolateViper(t)
	viper.Reset() // drops the LOKI prefix; isolateViper restores it afterwards

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	envFlag(fs, "query-filter", "", "", "Usage")
	envBoolFlag(fs, "secret-filter", "", true, "Usage")

	assert.Equal(t, "Usage [env: QUERY_FILTER]", fs.Lookup("query-filter").Usage)
	assert.Equal(t, "Usage [env: SECRET_FILTER]", fs.Lookup("secret-filter").Usage)
}

// TestEnvFlagRegistration pins that envFlag forwards name, shorthand and
// default to pflag unchanged.
func TestEnvFlagRegistration(t *testing.T) {
	isolateViper(t)

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	envFlag(fs, "some-string", "s", "the-default", "A string")
	envBoolFlag(fs, "some-bool", "b", true, "A bool")

	str := fs.Lookup("some-string")
	require.NotNil(t, str)
	assert.Equal(t, "s", str.Shorthand)
	assert.Equal(t, "the-default", str.DefValue)
	assert.Equal(t, "string", str.Value.Type())

	b := fs.Lookup("some-bool")
	require.NotNil(t, b)
	assert.Equal(t, "b", b.Shorthand)
	assert.Equal(t, "true", b.DefValue)
	assert.Equal(t, "bool", b.Value.Type())
}

// TestEnvFlagPrecedence pins the resolution order envFlag wires up:
// explicit flag > environment variable > flag default.
func TestEnvFlagPrecedence(t *testing.T) {
	isolateViper(t)

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	envFlag(fs, "some-string", "s", "the-default", "A string")
	envBoolFlag(fs, "some-bool", "b", true, "A bool")

	t.Run("default when nothing is set", func(t *testing.T) {
		assert.Equal(t, "the-default", viper.GetString("some-string"))
		assert.True(t, viper.GetBool("some-bool"))
	})

	t.Run("env beats default", func(t *testing.T) {
		t.Setenv("LOKI_SOME_STRING", "from-env")
		t.Setenv("LOKI_SOME_BOOL", "false")
		assert.Equal(t, "from-env", viper.GetString("some-string"))
		assert.False(t, viper.GetBool("some-bool"))
	})

	t.Run("flag beats env", func(t *testing.T) {
		t.Setenv("LOKI_SOME_STRING", "from-env")
		t.Setenv("LOKI_SOME_BOOL", "false")
		setFlag(t, fs, "some-string", "from-flag")
		setFlag(t, fs, "some-bool", "true")
		assert.Equal(t, "from-flag", viper.GetString("some-string"))
		assert.True(t, viper.GetBool("some-bool"))
	})
}

// TestRootFlagsResolveFromEnv pins that every real root flag is reachable
// through its documented LOKI_* variable, not just the synthetic ones above.
func TestRootFlagsResolveFromEnv(t *testing.T) {
	isolateViper(t)

	t.Setenv("LOKI_URL", "http://loki.example:3100")
	t.Setenv("LOKI_ORG_ID", "tenant-a")
	t.Setenv("LOKI_QUERY_FILTER", `{namespace="prod"}`)
	t.Setenv("LOKI_LOG_LEVEL", "debug")
	t.Setenv("LOKI_SECRET_FILTER", "false")
	t.Setenv("LOKI_OTEL_STDERR", "true")

	assert.Equal(t, "http://loki.example:3100", viper.GetString("url"))
	assert.Equal(t, "tenant-a", viper.GetString("org-id"))
	assert.Equal(t, `{namespace="prod"}`, viper.GetString("query-filter"))
	assert.Equal(t, "debug", viper.GetString("log-level"))
	assert.False(t, viper.GetBool("secret-filter"))
	assert.True(t, viper.GetBool("otel-stderr"))

	// The flag still wins over the environment.
	setFlag(t, rootCmd.PersistentFlags(), "url", "http://flag:3100")
	assert.Equal(t, "http://flag:3100", viper.GetString("url"))
}
