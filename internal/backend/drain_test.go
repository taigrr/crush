package backend

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/journal"
	"github.com/taigrr/crush/internal/swarm"
)

// busySessionSet is a test double for a workspace's in-flight sessions.
type busySessionSet struct {
	mu   sync.Mutex
	busy map[string]struct{}
}

func newBusySessionSet(ids ...string) *busySessionSet {
	s := &busySessionSet{busy: make(map[string]struct{})}
	for _, id := range ids {
		s.busy[id] = struct{}{}
	}
	return s
}

func (s *busySessionSet) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.busy))
	for id := range s.busy {
		out = append(out, id)
	}
	return out
}

func (s *busySessionSet) finish(id string) {
	s.mu.Lock()
	delete(s.busy, id)
	s.mu.Unlock()
}

func insertDrainTestWorkspace(t *testing.T, b *Backend, key string, ids ...string) (*Workspace, *busySessionSet) {
	t.Helper()
	ws, _ := insertTestWorkspace(t, b, key)
	set := newBusySessionSet(ids...)
	SetWorkspaceBusySessionsForTest(ws, set.list)
	return ws, set
}

// TestDrain_WaitsForActiveRunsThenShutsDown is the core drain contract:
// health reports draining + the live run count, the server stays up
// while runs are active, and it shuts itself down once they finish.
func TestDrain_WaitsForActiveRunsThenShutsDown(t *testing.T) {
	// Not parallel: mutates the package-level drainPollInterval.
	prevPoll := drainPollInterval
	drainPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { drainPollInterval = prevPoll })

	b, srvShutdowns := newTestBackend(t)
	_, setA := insertDrainTestWorkspace(t, b, "/tmp/a", "s1", "s2")
	_, setB := insertDrainTestWorkspace(t, b, "/tmp/b", "s3")

	require.False(t, b.Health().Draining)
	require.Equal(t, 3, b.Health().ActiveRuns)

	h := b.Drain()
	require.True(t, h.Draining)
	require.Equal(t, 3, h.ActiveRuns)

	// Idempotent: a second drain reports the same state.
	require.True(t, b.Drain().Draining)

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), srvShutdowns.Load(), "server must not shut down while runs are active")

	setA.finish("s1")
	setB.finish("s3")
	SignalDrainForTest(b)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), srvShutdowns.Load(), "one run still active")
	require.Equal(t, 1, b.Health().ActiveRuns)

	setA.finish("s2")
	SignalDrainForTest(b)
	require.Eventually(t, func() bool { return srvShutdowns.Load() == 1 }, 2*time.Second, 5*time.Millisecond,
		"server must shut itself down once the last run finishes")
	require.Equal(t, 0, b.workspaces.Len(), "drain completion tears every workspace down")
}

// TestDrain_ImmediateWhenIdle: with nothing active the drain completes
// at once.
func TestDrain_ImmediateWhenIdle(t *testing.T) {
	// Not parallel: mutates the package-level drainPollInterval.
	prevPoll := drainPollInterval
	drainPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { drainPollInterval = prevPoll })

	b, srvShutdowns := newTestBackend(t)
	insertDrainTestWorkspace(t, b, "/tmp/a")

	h := b.Drain()
	require.True(t, h.Draining)
	require.Equal(t, 0, h.ActiveRuns)
	require.Eventually(t, func() bool { return srvShutdowns.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
}

// TestMayRehydrate_IsolatedAndSharedDataDir: an isolated workspace never
// replays the journal, and neither does a workspace whose data directory
// is already owned by another live workspace in this process.
func TestMayRehydrate_IsolatedAndSharedDataDir(t *testing.T) {
	xdgIsolated(t)
	b, _ := newTestBackend(t)

	dataDir := t.TempDir()
	mk := func(isolated bool) *Workspace {
		cfg, err := config.Init(t.TempDir(), dataDir, false)
		require.NoError(t, err)
		ws := &Workspace{ID: uuid.New().String(), Cfg: cfg, isolated: isolated, clients: make(map[string]*clientState)}
		ws.App = app.NewForTest(t.Context())
		return ws
	}

	shared := mk(false)
	require.True(t, b.mayRehydrate(shared), "a lone shared workspace owns its journal")

	iso := mk(true)
	require.False(t, b.mayRehydrate(iso), "isolated workspaces never replay")

	// An isolated workspace attached first (a `crush run` in flight)
	// must not stop the shared workspace from replaying.
	iso.AgentCoordinator = &errorCoordinator{}
	b.workspaces.Set(iso.ID, iso)
	require.True(t, b.mayRehydrate(shared), "a live isolated workspace does not own the journal")
	b.workspaces.Del(iso.ID)

	// Nor does a shared one that never got a coordinator.
	unconfigured := mk(false)
	b.workspaces.Set(unconfigured.ID, unconfigured)
	require.True(t, b.mayRehydrate(shared))
	b.workspaces.Del(unconfigured.ID)

	shared.AgentCoordinator = &errorCoordinator{}
	b.workspaces.Set(shared.ID, shared)
	second := mk(false)
	require.False(t, b.mayRehydrate(second), "a second workspace on the same data dir must not replay")

	other, err := config.Init(t.TempDir(), t.TempDir(), false)
	require.NoError(t, err)
	elsewhere := &Workspace{ID: uuid.New().String(), Cfg: other, clients: make(map[string]*clientState)}
	elsewhere.App = app.NewForTest(t.Context())
	require.True(t, b.mayRehydrate(elsewhere), "a different data dir is unaffected")
}

// TestActiveRuns_CountsLiveRunGoroutines: a run whose session momentarily
// looks idle to the coordinator (a goal/reply continuation between
// turns) still holds the drain open while its runAgent goroutine lives.
func TestActiveRuns_CountsLiveRunGoroutines(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	ws, set := insertDrainTestWorkspace(t, b, "/tmp/a")
	require.Equal(t, 0, b.ActiveRuns())

	ws.runMu.Lock()
	ws.liveRuns = 1
	ws.runMu.Unlock()
	require.Equal(t, 1, b.ActiveRuns(), "a live run goroutine counts even with no busy session")

	set.busy["s1"] = struct{}{}
	require.Equal(t, 1, b.ActiveRuns(), "the same run seen both ways is not double counted")

	ws.runMu.Lock()
	ws.liveRuns = 0
	ws.runMu.Unlock()
	require.Equal(t, 1, b.ActiveRuns())
	set.finish("s1")
	require.Equal(t, 0, b.ActiveRuns())
}

// TestShutdownWorkspaces_ForcedDiscardsJournal: a forced (non-drained)
// shutdown must empty both journal tables — and must do so before the
// workspace releases its database connection.
func TestShutdownWorkspaces_ForcedDiscardsJournal(t *testing.T) {
	xdgIsolated(t)
	dataDir := t.TempDir()
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	store := journal.New(conn, db.New(conn), dataDir)
	require.NoError(t, store.SaveQueue(t.Context(), "s1", []journal.QueuedPrompt{{SessionID: "s1", Prompt: "queued"}}))
	require.NoError(t, store.SaveReplies("s1", []swarm.ReplyObligation{{SenderSessionID: "parent"}}))

	b, _ := newTestBackend(t)
	cfg, err := config.Init(t.TempDir(), dataDir, false)
	require.NoError(t, err)
	a := app.NewForTest(t.Context())
	a.Journal = store
	released := false
	ws := &Workspace{ID: uuid.New().String(), Path: "/tmp/forced", resolvedPath: "/tmp/forced", Cfg: cfg, App: a, clients: make(map[string]*clientState)}
	ws.shutdownFn = func() {
		released = true
		require.NoError(t, db.Release(dataDir))
	}
	InsertWorkspaceForTest(b, ws)

	b.ShutdownWorkspaces()
	require.True(t, released)

	// Re-open the database and check the tables directly.
	conn, err = db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir) })
	q := db.New(conn)
	queue, err := q.ListSessionQueue(t.Context())
	require.NoError(t, err)
	require.Empty(t, queue, "forced shutdown drops queued prompts")
	replies, err := q.ListSwarmReplyObligations(t.Context())
	require.NoError(t, err)
	require.Empty(t, replies, "forced shutdown drops reply obligations (cleared before the DB is released)")
}
