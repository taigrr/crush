//go:build !windows

package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "crs")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestListenUnixStaleSocket(t *testing.T) {
	t.Parallel()
	path := shortSocketPath(t)

	old, err := net.Listen("unix", path)
	require.NoError(t, err)
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, old.Close())
	_, err = os.Stat(path)
	require.NoError(t, err)

	ln, err := listen("unix", path)
	require.NoError(t, err)
	defer ln.Close()
}

func TestListenUnixLiveSocket(t *testing.T) {
	t.Parallel()
	path := shortSocketPath(t)

	live, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer live.Close()
	go func() {
		for {
			c, err := live.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, err = listen("unix", path)
	require.Error(t, err)

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	_ = conn.Close()
}
