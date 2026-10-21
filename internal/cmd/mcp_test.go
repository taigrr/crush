package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

type fakeDialer struct {
	calls    atomic.Int32
	cleanups atomic.Int32
	gate     chan struct{}
	fail     atomic.Bool
	mu       sync.Mutex
	streams  []chan any
}

func (f *fakeDialer) dial(dir string) (*mcpEntry, error) {
	f.calls.Add(1)
	if f.gate != nil {
		<-f.gate
	}
	if f.fail.Load() {
		return nil, errors.New("server unreachable")
	}
	events := make(chan any)
	f.mu.Lock()
	f.streams = append(f.streams, events)
	f.mu.Unlock()
	return newMCPEntry(nil, &proto.Workspace{ID: "ws-" + dir, Path: dir}, events, func() {}, func() { f.cleanups.Add(1) }), nil
}

func newTestConnector(f *fakeDialer) *mcpConnector {
	m := newMCPConnector(&cobra.Command{})
	m.connect = f.dial
	return m
}

func TestMCPConnector_CachesPerCanonicalDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))

	f := &fakeDialer{}
	m := newTestConnector(f)
	_, ws1, err := m.Connect(t.Context(), dir)
	require.NoError(t, err)
	_, ws2, err := m.Connect(t.Context(), link)
	require.NoError(t, err)
	require.Same(t, ws1, ws2, "two spellings of a project share one connection")
	require.EqualValues(t, 1, f.calls.Load())

	m.Close()
	require.EqualValues(t, 1, f.cleanups.Load())
}

func TestMCPConnector_ConcurrentFirstConnectDialsOnce(t *testing.T) {
	t.Parallel()

	f := &fakeDialer{gate: make(chan struct{})}
	m := newTestConnector(f)
	dir := t.TempDir()

	errs := make(chan error, 5)
	for range 5 {
		go func() {
			_, _, err := m.Connect(t.Context(), dir)
			errs <- err
		}()
	}
	require.Eventually(t, func() bool { return f.calls.Load() == 1 }, time.Second, 5*time.Millisecond)

	// A different project is not blocked by the slow one.
	other := &fakeDialer{}
	m.mu.Lock()
	m.connect = func(d string) (*mcpEntry, error) {
		if d == canonicalDir(dir) {
			return f.dial(d)
		}
		return other.dial(d)
	}
	m.mu.Unlock()
	otherDir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, _, err := m.Connect(t.Context(), otherDir)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("a slow connect to one project blocked another project")
	}

	close(f.gate)
	for range 5 {
		require.NoError(t, <-errs)
	}
	require.EqualValues(t, 1, f.calls.Load())
	m.Close()
}

func TestMCPConnector_ReconnectsAfterStreamDies(t *testing.T) {
	t.Parallel()

	f := &fakeDialer{}
	m := newTestConnector(f)
	dir := t.TempDir()

	_, _, err := m.Connect(t.Context(), dir)
	require.NoError(t, err)
	close(f.streams[0])

	require.Eventually(t, func() bool {
		_, _, err := m.Connect(t.Context(), dir)
		return err == nil && f.calls.Load() == 2
	}, time.Second, 5*time.Millisecond)
	require.EqualValues(t, 1, f.cleanups.Load(), "the dead connection is released")
	m.Close()
}

func TestMCPConnector_ErrorsAreNotCached(t *testing.T) {
	t.Parallel()

	f := &fakeDialer{}
	f.fail.Store(true)
	m := newTestConnector(f)
	dir := t.TempDir()

	_, _, err := m.Connect(t.Context(), dir)
	require.ErrorContains(t, err, "server unreachable")
	f.fail.Store(false)
	_, _, err = m.Connect(t.Context(), dir)
	require.NoError(t, err)
	require.EqualValues(t, 2, f.calls.Load())
	m.Close()
}

func TestMCPConnector_WaitingCallerHonorsContext(t *testing.T) {
	t.Parallel()

	f := &fakeDialer{gate: make(chan struct{})}
	m := newTestConnector(f)
	dir := t.TempDir()
	go func() { _, _, _ = m.Connect(t.Context(), dir) }()
	require.Eventually(t, func() bool { return f.calls.Load() == 1 }, time.Second, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := m.Connect(ctx, dir)
	require.ErrorIs(t, err, context.Canceled)
	close(f.gate)
	m.Close()
}

func TestMCPCmdFlags(t *testing.T) {
	t.Parallel()

	f := mcpCmd.Flags().Lookup("permission-mode")
	require.NotNil(t, f)
	require.Equal(t, "delegate", f.DefValue)
	require.NotNil(t, mcpCmd.Flags().Lookup("max-wait"))
}

func TestMCPConnector_CloseDuringDialReleasesConnection(t *testing.T) {
	t.Parallel()

	f := &fakeDialer{gate: make(chan struct{})}
	m := newTestConnector(f)
	errc := make(chan error, 1)
	go func() {
		_, _, err := m.Connect(t.Context(), t.TempDir())
		errc <- err
	}()
	require.Eventually(t, func() bool { return f.calls.Load() == 1 }, time.Second, 5*time.Millisecond)

	m.Close()
	close(f.gate)
	require.ErrorContains(t, <-errc, "shutting down")
	require.EqualValues(t, 1, f.cleanups.Load(), "a dial that finishes after Close is released, not leaked")

	_, _, err := m.Connect(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "shutting down")
}
