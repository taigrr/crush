package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDaemonEnvSnapshot(t *testing.T) {
	env := snapshotEnv([]string{"AWS_PROFILE=dev", "PWD=/x", "TMUX=/tmp/t", "TERM_PROGRAM=ghostty", "PATH=/bin", "EMPTY=", "BAD"})
	require.Equal(t, map[string]string{"AWS_PROFILE": "dev", "PATH": "/bin", "EMPTY": ""}, env)

	path := filepath.Join(t.TempDir(), "env.json")
	t.Setenv("CRUSH_TEST_SNAPSHOT", "kept")
	require.NoError(t, writeDaemonEnv(path))
	t.Setenv("CRUSH_TEST_SNAPSHOT", "changed")
	require.NoError(t, loadDaemonEnv(path))
	require.Equal(t, "kept", os.Getenv("CRUSH_TEST_SNAPSHOT"))
}
