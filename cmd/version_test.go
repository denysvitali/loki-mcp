package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionCommand pins the output format and the default value of Version,
// which is overridden through -ldflags at build time.
func TestVersionCommand(t *testing.T) {
	assert.Equal(t, "version", versionCmd.Use)
	assert.Equal(t, "Print the version number", versionCmd.Short)
	require.NotNil(t, versionCmd.Run)

	prev := Version
	t.Cleanup(func() { Version = prev })

	Version = "1.2.3"
	out := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })
	assert.Equal(t, "loki-mcp 1.2.3\n", out)
}

// TestVersionWritesToProcessStdout pins that the command prints with
// fmt.Println rather than cmd.OutOrStdout: cobra's output writer is ignored.
func TestVersionWritesToProcessStdout(t *testing.T) {
	var cobraOut bytes.Buffer
	versionCmd.SetOut(&cobraOut)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	out := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })

	assert.Equal(t, "loki-mcp "+Version+"\n", out)
	assert.Empty(t, cobraOut.String())
}
