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

	var out bytes.Buffer
	versionCmd.SetOut(&out)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	Version = "1.2.3"
	versionCmd.Run(versionCmd, nil)
	assert.Equal(t, "loki-mcp 1.2.3\n", out.String())
}

// TestVersionWritesToCobraWriter pins that the command prints through
// cmd.OutOrStdout, so a writer installed with SetOut captures it and nothing
// reaches the process stdout. It used to print with fmt.Println, which ignored
// cobra's writer (FINDINGS.md §13).
func TestVersionWritesToCobraWriter(t *testing.T) {
	var cobraOut bytes.Buffer
	versionCmd.SetOut(&cobraOut)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	processOut := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })

	assert.Equal(t, "loki-mcp "+Version+"\n", cobraOut.String())
	assert.Empty(t, processOut, "nothing bypasses the writer onto fd 1")
}

// TestVersionFallsBackToStdout pins the no-writer case: with nothing installed
// by SetOut, cmd.OutOrStdout resolves to the process stdout as before.
func TestVersionFallsBackToStdout(t *testing.T) {
	out := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })
	assert.Equal(t, "loki-mcp "+Version+"\n", out)
}
