package backend

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// insertBusyTestWorkspace is like insertTestWorkspace but lets the test
// control the workspace's busy state via the returned flag.
func insertBusyTestWorkspace(t *testing.T, b *Backend, key string) (*Workspace, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	ws, shutdowns := insertTestWorkspace(t, b, key)
	var busy atomic.Bool
	ws.busyFn = busy.Load
	return ws, shutdowns, &busy
}

// TestDetachWhileBusy_KeepsWorkspaceAlive verifies that when the last
// client detaches while an agent run is in flight, the workspace is NOT
// torn down and the server is NOT shut down. This protects an in-flight
// run from being cancelled merely because the TUI detached/suspended.
func TestDetachWhileBusy_KeepsWorkspaceAlive(t *testing.T) {
	t.Parallel()

	b, srvShutdowns := newTestBackend(t)
	ws, wsShutdowns, busy := insertBusyTestWorkspace(t, b, "/tmp/a")
	busy.Store(true)

	cid := newClientID(t)
	b.registerClient(ws, cid, nil, "")
	require.NoError(t, b.AttachClient(ws.ID, cid))
	b.DetachClient(ws.ID, cid)

	require.Equal(t, int32(0), wsShutdowns.Load(), "busy workspace must not be torn down on detach")
	require.Equal(t, int32(0), srvShutdowns.Load(), "server must not shut down while a run is in flight")
	_, err := b.GetWorkspace(ws.ID)
	require.NoError(t, err, "workspace must remain registered while busy")
}

// TestTeardownIfIdle_AfterRunCompletes verifies the second half of the
// contract: once the run finishes (busy clears), the post-run idle
// re-check tears the now-clientless workspace down and shuts the server.
func TestTeardownIfIdle_AfterRunCompletes(t *testing.T) {
	t.Parallel()

	b, srvShutdowns := newTestBackend(t)
	ws, wsShutdowns, busy := insertBusyTestWorkspace(t, b, "/tmp/a")
	busy.Store(true)

	cid := newClientID(t)
	b.registerClient(ws, cid, nil, "")
	require.NoError(t, b.AttachClient(ws.ID, cid))
	b.DetachClient(ws.ID, cid)

	// Still alive because busy.
	require.Equal(t, int32(0), wsShutdowns.Load())

	// Run completes: agent reports idle, runAgent's post-run re-check fires.
	busy.Store(false)
	b.teardownIfIdle(ws)

	require.Equal(t, int32(1), wsShutdowns.Load(), "idle workspace with no clients must tear down")
	require.Equal(t, int32(1), srvShutdowns.Load(), "last workspace teardown must shut the server down")
	_, err := b.GetWorkspace(ws.ID)
	require.ErrorIs(t, err, ErrWorkspaceNotFound)
}

// TestTeardownIfIdle_KeepsBusyWorkspaceWithNoClients verifies the idle
// re-check is a no-op while the agent is still busy, even with zero
// clients attached.
func TestTeardownIfIdle_KeepsBusyWorkspaceWithNoClients(t *testing.T) {
	t.Parallel()

	b, srvShutdowns := newTestBackend(t)
	ws, wsShutdowns, busy := insertBusyTestWorkspace(t, b, "/tmp/a")
	busy.Store(true)

	b.teardownIfIdle(ws)

	require.Equal(t, int32(0), wsShutdowns.Load())
	require.Equal(t, int32(0), srvShutdowns.Load())
	_, err := b.GetWorkspace(ws.ID)
	require.NoError(t, err)
}

// TestTeardown_Idempotent verifies teardown only fires the shutdown hooks
// once even when reached from multiple paths (e.g. a detach race with the
// post-run idle re-check).
func TestTeardown_Idempotent(t *testing.T) {
	t.Parallel()

	b, srvShutdowns := newTestBackend(t)
	ws, wsShutdowns := insertTestWorkspace(t, b, "/tmp/a")

	b.teardown(ws)
	b.teardown(ws)
	b.teardownIfIdle(ws)

	require.Equal(t, int32(1), wsShutdowns.Load(), "workspace shutdown hook must fire exactly once")
	require.Equal(t, int32(1), srvShutdowns.Load(), "server shutdown must fire exactly once")
}

// TestExplicitShutdown_BypassesBusyCheck verifies that the explicit
// shutdown command (Backend.Shutdown) shuts the server down immediately,
// regardless of whether any workspace agent is busy.
func TestExplicitShutdown_BypassesBusyCheck(t *testing.T) {
	t.Parallel()

	b, srvShutdowns := newTestBackend(t)
	_, _, busy := insertBusyTestWorkspace(t, b, "/tmp/a")
	busy.Store(true)

	b.Shutdown()

	require.Equal(t, int32(1), srvShutdowns.Load(), "explicit shutdown must ignore busy state")
}
