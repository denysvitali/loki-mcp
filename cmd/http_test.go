package cmd

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPFlags pins the http subcommand's flag surface. --port is registered
// by hand rather than through envFlag because its env var has no LOKI_ prefix.
func TestHTTPFlags(t *testing.T) {
	tests := []struct {
		name      string
		shorthand string
		def       string
		usage     string
	}{
		{
			name: "host", shorthand: "", def: "0.0.0.0",
			usage: "HTTP server bind address [env: LOKI_HOST]",
		},
		{
			name: "disable-sse", shorthand: "", def: "false",
			usage: "Disable legacy SSE endpoints (/sse, /mcp) [env: LOKI_DISABLE_SSE]",
		},
		{
			name: "port", shorthand: "p", def: "8080",
			usage: "HTTP server port [env: PORT]",
		},
	}

	// LocalFlags, not Flags: once anything has executed `http`, cobra has
	// merged the root's persistent flags into httpCmd.Flags() for good, and
	// the enumeration below would see them too.
	fs := httpCmd.LocalFlags()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fs.Lookup(tt.name)
			require.NotNil(t, f)
			assert.Equal(t, tt.shorthand, f.Shorthand)
			assert.Equal(t, tt.def, f.DefValue)
			assert.Equal(t, tt.usage, f.Usage)
		})
	}

	var registered []string
	fs.VisitAll(func(f *pflag.Flag) {
		// --help is cobra's, added to whichever command executes.
		if f.Name == "help" {
			return
		}
		registered = append(registered, f.Name)
	})
	want := make([]string, 0, len(tests))
	for _, tt := range tests {
		want = append(want, tt.name)
	}
	assert.ElementsMatch(t, want, registered, "unexpected http flag set")
}

// TestPortResolution pins the PORT special case and its documented
// precedence: --port when passed, then PORT, then the default. LOKI_PORT is
// not a source at all.
//
// It would become one, and the highest-ranked one, the moment the port key is
// registered with viper: viper's AutomaticEnv resolves the prefixed LOKI_PORT
// before any explicit BindEnv, so the undocumented name would beat the
// "[env: PORT]" the flag advertises. This test is what catches that.
func TestPortResolution(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		isolateViper(t)
		assert.Equal(t, "8080", resolvePort(httpCmd.Flags()))
	})

	t.Run("PORT is honoured", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("PORT", "9999")
		assert.Equal(t, "9999", resolvePort(httpCmd.Flags()))
	})

	t.Run("an empty PORT falls back to the default", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("PORT", "")
		assert.Equal(t, "8080", resolvePort(httpCmd.Flags()))
	})

	t.Run("LOKI_PORT is not consulted", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("LOKI_PORT", "7777")
		assert.Equal(t, "8080", resolvePort(httpCmd.Flags()),
			"LOKI_PORT alone must not move the port off its default")
	})

	t.Run("PORT wins over LOKI_PORT", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("LOKI_PORT", "7777")
		t.Setenv("PORT", "9999")
		assert.Equal(t, "9999", resolvePort(httpCmd.Flags()),
			"the documented variable must win")
	})

	t.Run("flag beats both", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("PORT", "9999")
		t.Setenv("LOKI_PORT", "7777")
		setFlag(t, httpCmd.Flags(), "port", "1234")
		assert.Equal(t, "1234", resolvePort(httpCmd.Flags()))
	})

	t.Run("an unpassed flag does not beat PORT", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("PORT", "9999")
		require.False(t, httpCmd.Flags().Lookup("port").Changed)
		assert.Equal(t, "9999", resolvePort(httpCmd.Flags()),
			"the flag only wins when the user actually passed it")
	})

	// A regression guard on the mechanism, not just the outcome: rebinding
	// the flag to viper is what would put the key back under AutomaticEnv,
	// where LOKI_PORT outranks PORT again.
	t.Run("the port flag is not bound to viper", func(t *testing.T) {
		isolateViper(t)
		setFlag(t, httpCmd.Flags(), "port", "1234")
		assert.NotEqual(t, "1234", viper.GetString("port"))
	})
}

// TestHostAndDisableSSEResolution pins the two prefixed http flags.
func TestHostAndDisableSSEResolution(t *testing.T) {
	isolateViper(t)

	assert.Equal(t, "0.0.0.0", viper.GetString("host"))
	assert.False(t, viper.GetBool("disable-sse"))

	t.Setenv("LOKI_HOST", "127.0.0.1")
	t.Setenv("LOKI_DISABLE_SSE", "true")

	assert.Equal(t, "127.0.0.1", viper.GetString("host"))
	assert.True(t, viper.GetBool("disable-sse"))
}
