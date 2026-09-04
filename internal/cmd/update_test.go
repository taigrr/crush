package cmd

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsDeadSocketErr(t *testing.T) {
	t.Parallel()
	require.False(t, isDeadSocketErr(nil))
	require.True(t, isDeadSocketErr(fs.ErrNotExist))
	require.True(t, isDeadSocketErr(&net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}))
	require.True(t, isDeadSocketErr(&net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ENOENT}}))

	// A live-but-slow server must never be declared dead.
	require.False(t, isDeadSocketErr(context.DeadlineExceeded))
	require.False(t, isDeadSocketErr(&net.OpError{Op: "dial", Err: &timeoutErr{}}))
	require.False(t, isDeadSocketErr(&net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.EAGAIN}}))
	require.False(t, isDeadSocketErr(&net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.EACCES}}))
	require.False(t, isDeadSocketErr(errors.New("invalid character '<' looking for beginning of value")))
}

type timeoutErr struct{}

func (*timeoutErr) Error() string   { return "i/o timeout" }
func (*timeoutErr) Timeout() bool   { return true }
func (*timeoutErr) Temporary() bool { return true }

func TestIsServerCmdline(t *testing.T) {
	t.Parallel()
	require.True(t, isServerCmdline([]string{"/usr/bin/crush", "server"}))
	require.True(t, isServerCmdline([]string{"crush", "server", "--host", "unix:///tmp/x.sock"}))
	require.False(t, isServerCmdline([]string{"crush"}), "a TUI is not a server")
	require.False(t, isServerCmdline([]string{"crush", "run", "hi"}))
	require.False(t, isServerCmdline([]string{"server"}), "argv[0] alone does not count")
	require.False(t, isServerCmdline(nil), "unknown command lines are not trusted")
	require.False(t, isServerCmdline([]string{"crush", "run", "restart", "the", "server"}), "the word elsewhere in argv does not count")
}

func TestIsCrushProcess_RejectsUnrelatedPid(t *testing.T) {
	t.Parallel()
	// Our own test binary is alive but is neither named crush nor
	// running `server`.
	require.False(t, isCrushProcess(os.Getpid()))
	require.False(t, isCrushProcess(0))
	require.False(t, isCrushProcess(-1))
}

func TestWaitForSocketGone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	u := &urlHost{host: dir + "/absent.sock"}
	require.True(t, waitForSocketGone(context.Background(), u.URL(), 50*time.Millisecond))
	present := dir + "/present.sock"
	require.NoError(t, os.WriteFile(present, nil, 0o600))
	require.False(t, waitForSocketGone(context.Background(), (&urlHost{host: present}).URL(), 50*time.Millisecond))
}

type urlHost struct{ host string }

func (u *urlHost) URL() *url.URL { return &url.URL{Scheme: "unix", Host: u.host} }
