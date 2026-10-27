package cluster

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceDefinitions(t *testing.T) {
	t.Parallel()
	spec := ServiceSpec{
		Executable: "/Users/me/go/bin/crush",
		Args:       []string{"server", "--persistent"},
		LogDir:     "/tmp/crush logs",
		Env:        []string{"PATH=/usr/bin:/a&b", "HOME=/Users/me"},
	}

	plist, err := LaunchdPlist(spec)
	require.NoError(t, err)
	require.Contains(t, plist, "<string>"+ServiceLabel+"</string>")
	require.Contains(t, plist, "<string>--persistent</string>")
	require.Contains(t, plist, "<key>KeepAlive</key>\n\t<true/>")
	require.Contains(t, plist, "/usr/bin:/a&amp;b")

	unit, err := SystemdUnit(spec)
	require.NoError(t, err)
	require.Contains(t, unit, "ExecStart=/Users/me/go/bin/crush server --persistent\n")
	require.Contains(t, unit, "Restart=always")
	require.Contains(t, unit, `Environment="HOME=/Users/me"`)
}

func TestServiceThroughLoginShell(t *testing.T) {
	t.Parallel()
	spec := ServiceSpec{Executable: "/Users/me/go/bin/crush", Args: []string{"server", "--persistent"}, LogDir: "/tmp", Shell: "/bin/zsh"}
	plist, err := LaunchdPlist(spec)
	require.NoError(t, err)
	require.Contains(t, plist, "<string>/bin/zsh</string>\n\t\t<string>-l</string>\n\t\t<string>-i</string>\n\t\t<string>-c</string>")
	require.Contains(t, plist, "exec &apos;/Users/me/go/bin/crush&apos; &apos;server&apos; &apos;--persistent&apos;")
	unit, err := SystemdUnit(spec)
	require.NoError(t, err)
	require.Contains(t, unit, `ExecStart=/bin/zsh -l -i -c "exec '/Users/me/go/bin/crush' 'server' '--persistent'"`)
}
