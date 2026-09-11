//go:build windows

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitWindowsCommandLine(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{`C:\Program Files\crush\crush.exe`, "server", "--host", "npipe:////./pipe/x"},
		splitWindowsCommandLine(`"C:\Program Files\crush\crush.exe" server --host npipe:////./pipe/x`))
	require.Equal(t, []string{`C:\tools\crush server\crush.exe`}, splitWindowsCommandLine(`"C:\tools\crush server\crush.exe"`),
		"a space in the exe path must not be mistaken for the server subcommand")
	require.False(t, isServerCmdline(splitWindowsCommandLine(`"C:\tools\crush server\crush.exe"`)))
}
