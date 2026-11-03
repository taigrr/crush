package backend

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/registry"
)

// fakePeeker lists canned sessions, blocking forever on stuck data dirs
// the way an open(2) on a wedged volume does.
type fakePeeker struct {
	stuck   map[string]bool
	release chan struct{}
	calls   atomic.Int32
}

func (f *fakePeeker) peek(_ context.Context, dataDir string) ([]db.PeekedSession, error) {
	f.calls.Add(1)
	if f.stuck[dataDir] {
		<-f.release // Ignores ctx, like a blocked syscall.
	}
	return []db.PeekedSession{{ID: filepath.Base(dataDir), Title: "ok"}}, nil
}

func newOverviewBackend(t *testing.T, peeker *fakePeeker, entries ...registry.Entry) *Backend {
	t.Helper()
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	for _, e := range entries {
		require.NoError(t, b.registry.Add(e))
	}
	b.peekCache.peek = peeker.peek
	b.peekCache.guard.timeout = 100 * time.Millisecond
	return b
}

func TestListWorkspaceOverviews_StuckPeekReturnsPartial(t *testing.T) {
	t.Parallel()
	peeker := &fakePeeker{stuck: map[string]bool{"/data/stuck": true}, release: make(chan struct{})}
	t.Cleanup(func() { close(peeker.release) })
	b := newOverviewBackend(t, peeker,
		registry.Entry{Root: "/proj/stuck", DataDir: "/data/stuck", LastUsed: 2},
		registry.Entry{Root: "/proj/fine", DataDir: "/data/fine", LastUsed: 1},
	)

	start := time.Now()
	ovs, err := b.ListWorkspaceOverviews(t.Context())
	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
	require.Len(t, ovs, 2)

	byRoot := map[string]int{}
	for i, ov := range ovs {
		byRoot[ov.Root] = i
	}
	stuck, fine := ovs[byRoot["/proj/stuck"]], ovs[byRoot["/proj/fine"]]
	require.True(t, stuck.Unavailable)
	require.NotEmpty(t, stuck.Error)
	require.Empty(t, stuck.Sessions)
	require.False(t, fine.Unavailable)
	require.Len(t, fine.Sessions, 1)

	// Repeated requests fail fast and do not start more blocked opens.
	for range 5 {
		start := time.Now()
		_, err := b.ListWorkspaceOverviews(t.Context())
		require.NoError(t, err)
		require.Less(t, time.Since(start), 50*time.Millisecond+b.peekCache.guard.timeout)
	}
	// One open of each path: the fine one is cached, the stuck one is
	// never retried while it is still blocked.
	require.Equal(t, int32(2), peeker.calls.Load())
}

func TestStallGuard_RecoversAfterStuckCallFinishes(t *testing.T) {
	t.Parallel()
	g := &stallGuard{timeout: 50 * time.Millisecond}
	release := make(chan struct{})
	_, err := guarded(t.Context(), g, "k", func(context.Context) (int, error) {
		<-release
		return 1, nil
	})
	require.ErrorIs(t, err, ErrDataDirUnavailable)

	// Still stuck: fails without running fn.
	ran := false
	_, err = guarded(t.Context(), g, "k", func(context.Context) (int, error) {
		ran = true
		return 2, nil
	})
	require.ErrorIs(t, err, ErrDataDirUnavailable)
	require.False(t, ran)

	close(release)
	require.Eventually(t, func() bool {
		v, err := guarded(t.Context(), g, "k", func(context.Context) (int, error) { return 3, nil })
		return err == nil && v == 3
	}, time.Second, 10*time.Millisecond)
}

func TestStallGuard_ConcurrentCallersShareOneSlot(t *testing.T) {
	t.Parallel()
	g := &stallGuard{timeout: time.Second}
	var running, maxRunning atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := guarded(t.Context(), g, "k", func(context.Context) (struct{}, error) {
				n := running.Add(1)
				for {
					m := maxRunning.Load()
					if n <= m || maxRunning.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				running.Add(-1)
				return struct{}{}, nil
			})
			require.NoError(t, err)
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), maxRunning.Load())
}

func TestListWorkspaceOverviews_DedupesSharedDataDir(t *testing.T) {
	t.Parallel()
	peeker := &fakePeeker{}
	b := newOverviewBackend(t, peeker,
		registry.Entry{Root: "/proj/a", DataDir: "/data/shared", LastUsed: 2},
		registry.Entry{Root: "/proj/a-link", DataDir: "/data/shared/", LastUsed: 1},
	)
	ovs, err := b.ListWorkspaceOverviews(t.Context())
	require.NoError(t, err)
	require.Len(t, ovs, 1)
	require.Equal(t, "/proj/a", ovs[0].Root)
}

func TestListWorkspaceOverviews_HidesTempWorkspaces(t *testing.T) {
	// The registry must live outside a temp dir, or the server counts as
	// a test server and keeps temp workspaces.
	dir, err := os.MkdirTemp(".", "registry-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)

	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(abs, "workspaces.jsonl"))
	b.peekCache.peek = (&fakePeeker{}).peek
	for _, e := range []registry.Entry{
		{Root: "/proj/real", DataDir: "/proj/real/.crush"},
		{Root: "/private/tmp/crush-balance-test", DataDir: "/private/tmp/crush-balance-test/.crush"},
		{Root: "/tmp/x", DataDir: "/tmp/x/.crush"},
		{Root: "/proj/y", DataDir: filepath.Join(os.TempDir(), "y")},
	} {
		require.NoError(t, b.registry.Add(e))
	}
	ovs, err := b.ListWorkspaceOverviews(t.Context())
	require.NoError(t, err)
	require.Len(t, ovs, 1)
	require.Equal(t, "/proj/real", ovs[0].Root)

	// A server whose own registry is temporary keeps them.
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/tmp/x", DataDir: "/tmp/x/.crush"}))
	ovs, err = b.ListWorkspaceOverviews(t.Context())
	require.NoError(t, err)
	require.Len(t, ovs, 1)
}
