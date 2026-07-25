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

	fs := httpCmd.Flags()
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
	fs.VisitAll(func(f *pflag.Flag) { registered = append(registered, f.Name) })
	want := make([]string, 0, len(tests))
	for _, tt := range tests {
		want = append(want, tt.name)
	}
	assert.ElementsMatch(t, want, registered, "unexpected http flag set")
}

// TestPortResolution pins the PORT special case. The flag is bound both to
// pflag and, via BindEnv, to the unprefixed PORT variable that PaaS platforms
// set.
//
// Surprise: PORT is *not* the only source. viper checks AutomaticEnv (i.e.
// LOKI_PORT) before any explicit BindEnv, so LOKI_PORT silently wins over
// PORT even though --help advertises only "[env: PORT]".
func TestPortResolution(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		isolateViper(t)
		assert.Equal(t, "8080", viper.GetString("port"))
	})

	t.Run("PORT is honoured", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("PORT", "9999")
		assert.Equal(t, "9999", viper.GetString("port"))
	})

	t.Run("LOKI_PORT also resolves and outranks PORT", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("LOKI_PORT", "7777")
		assert.Equal(t, "7777", viper.GetString("port"))

		t.Setenv("PORT", "9999")
		assert.Equal(t, "7777", viper.GetString("port"),
			"AutomaticEnv (LOKI_PORT) is consulted before the explicit BindEnv(PORT)")
	})

	t.Run("flag beats both", func(t *testing.T) {
		isolateViper(t)
		t.Setenv("PORT", "9999")
		t.Setenv("LOKI_PORT", "7777")
		setFlag(t, httpCmd.Flags(), "port", "1234")
		assert.Equal(t, "1234", viper.GetString("port"))
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
