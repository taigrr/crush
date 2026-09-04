package backend_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/registry"
)

// writeSwarmProject writes a crush.json into dir that opts the
// workspace into swarm so CreateSwarmSession(AtPath) passes its gate.
func writeSwarmProject(t *testing.T, dir string) {
	t.Helper()
	const cfg = `{"options":{"swarm":{"enabled":true}}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crush.json"), []byte(cfg), 0o644))
}

// isolateConfigHome points config.Init's global reads at a throwaway
// HOME/XDG tree so the host machine's config can't leak in.
func isolateConfigHome(t *testing.T) {
	t.Helper()
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(hostHome, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(hostHome, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(hostHome, ".cache"))
}

// TestCreateSwarmSessionAtPath_ReuseExisting verifies that when a
// workspace is already running for the path, the path-based helper
// reuses it rather than spinning up a second one.
func TestCreateSwarmSessionAtPath_ReuseExisting(t *testing.T) {
	isolateConfigHome(t)

	wd := t.TempDir()
	writeSwarmProject(t, wd)

	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	cid := uuid.New().String()
	ws, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: cid,
		Path:     wd,
		DataDir:  filepath.Join(wd, ".crush"),
	})
	require.NoError(t, err)

	gotID, sess, err := b.CreateSwarmSessionAtPath(t.Context(), wd, backend.SwarmSpawnOptions{Title: "hello"})
	require.NoError(t, err)
	require.Equal(t, ws.ID, gotID, "must reuse the already-running workspace")
	require.NotEmpty(t, sess.ID)
	require.NotEmpty(t, sess.Color)
	require.NotEmpty(t, sess.Animal)
}

// TestCreateSwarmSessionAtPath_CreateNew verifies that when no
// workspace is running for the path, the helper brings one up and
// creates the session in it.
func TestCreateSwarmSessionAtPath_CreateNew(t *testing.T) {
	isolateConfigHome(t)

	// srvCfg is anchored at an unrelated dir; the target path is a
	// separate, never-attached project directory.
	base := t.TempDir()
	srvCfg, err := config.Init(base, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	target := t.TempDir()
	writeSwarmProject(t, target)

	gotID, sess, err := b.CreateSwarmSessionAtPath(t.Context(), target, backend.SwarmSpawnOptions{Title: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, gotID, "must bring up a new workspace")
	require.NotEmpty(t, sess.ID)
	require.NotEmpty(t, sess.Color)
	require.NotEmpty(t, sess.Animal)

	// The workspace must now be resolvable and running.
	ws, err := b.GetWorkspace(gotID)
	require.NoError(t, err)
	require.NotNil(t, ws)
}

// TestLookupSwarmAddress_ReattachesTornDownWorkspace guards the
// regression where a workspace spawned via CreateSwarmSessionAtPath
// (address:"new" + path) became permanently unaddressable the moment
// its synthetic client's creation-grace timer fired and idle-teardown
// released the workspace, since no real SSE stream ever attaches to
// renew that hold. In production this happened within ~createGrace
// (30s) of spawning — often just after the spawned session sent its
// very first reply, breaking the exact "sub-session reports back,
// orchestrator replies" swarm pattern this method exists to support,
// even though the session's data was never touched on disk.
//
// The fix is not to keep the workspace pinned in memory forever
// (that trades a quick bug for an unbounded resource leak); it's to
// let idle-teardown behave normally and have LookupSwarmAddress
// transparently re-attach an on-disk-but-detached workspace when an
// address can't be found among the currently-running ones.
func TestLookupSwarmAddress_ReattachesTornDownWorkspace(t *testing.T) {
	isolateConfigHome(t)

	base := t.TempDir()
	srvCfg, err := config.Init(base, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)
	// Long enough that CreateSwarmSessionAtPath's own synchronous
	// CreateSwarmSession call (which runs immediately after
	// CreateWorkspace registers the synthetic client and arms this
	// timer) reliably finishes first; short enough that the
	// require.Eventually below doesn't need to wait long.
	b.SetCreateGrace(75 * time.Millisecond)

	target := t.TempDir()
	writeSwarmProject(t, target)

	gotID, sess, err := b.CreateSwarmSessionAtPath(t.Context(), target, backend.SwarmSpawnOptions{Title: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, gotID)

	// Let the synthetic client's short creation-grace timer expire
	// and idle-teardown actually release the workspace, mirroring
	// what happens in production once a spawned session finishes
	// its turn with no client attached.
	require.Eventually(t, func() bool {
		_, err := b.GetWorkspace(gotID)
		return err != nil
	}, time.Second, 5*time.Millisecond, "workspace must actually tear down once idle (that's expected/correct now)")

	// The session's data survives teardown untouched: looking it up
	// by its swarm address must transparently re-attach the
	// workspace rather than report NotFound.
	addr := sess.Color + "-" + sess.Animal
	result, err := b.LookupSwarmAddress(t.Context(), addr)
	require.NoError(t, err, "a torn-down-but-on-disk session must still resolve via LookupSwarmAddress")
	require.Equal(t, sess.ID, result.SessionID)
	require.NotEmpty(t, result.WorkspaceID)

	ws, err := b.GetWorkspace(result.WorkspaceID)
	require.NoError(t, err, "LookupSwarmAddress must have actually re-attached the workspace, not just reported a stale id")
	require.NotNil(t, ws)
}

// TestLookupSwarmAddress_SkipsStaleRegistryEntriesWithoutCreatingThem
// guards against the reattach fallback having the side effect of
// resurrecting a data directory for a registry entry whose root no
// longer has anything on disk (e.g. a deleted or renamed project):
// db.Connect creates its data directory on demand, so probing every
// registry entry unconditionally would silently recreate stale ones.
func TestLookupSwarmAddress_SkipsStaleRegistryEntriesWithoutCreatingThem(t *testing.T) {
	isolateConfigHome(t)

	base := t.TempDir()
	srvCfg, err := config.Init(base, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	// A registry entry pointing at a data dir that was never
	// actually initialized (no crush.db on disk).
	staleRoot := filepath.Join(base, "stale-project")
	staleDataDir := filepath.Join(staleRoot, ".crush")
	require.NoError(t, registry.New().Add(registry.Entry{
		Root:    staleRoot,
		DataDir: staleDataDir,
	}))

	_, err = b.LookupSwarmAddress(t.Context(), "aliceblue-tiger")
	require.ErrorIs(t, err, backend.ErrSwarmAddressNotFound)

	_, statErr := os.Stat(staleDataDir)
	require.True(t, os.IsNotExist(statErr), "probing a stale registry entry must not create its data directory")
}

// TestCreateWorkspace_WiresSwarm guards the regression where
// path-spawned (and any non-HTTP) workspaces never had the swarm
// backend injected: swarm wiring lives in CreateWorkspace — the single
// funnel all backend workspace creation flows through — so a workspace
// created directly, WITHOUT a follow-up InitAgent call, must already
// carry a swarm dispatcher. A second InitAgent (which rebuilds the
// coordinator) must re-wire idempotently.
func TestCreateWorkspace_WiresSwarm(t *testing.T) {
	isolateConfigHome(t)

	wd := t.TempDir()
	writeSwarmProject(t, wd)

	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	ws, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: uuid.New().String(),
		Path:     wd,
		DataDir:  filepath.Join(wd, ".crush"),
	})
	require.NoError(t, err)

	// The coordinator must exist (config must be configured for this
	// assertion to be meaningful) and must already have the swarm
	// dispatcher wired in — from CreateWorkspace alone, with no
	// follow-up InitAgent call.
	require.NotNil(t, ws.AgentCoordinator, "coordinator must be built by CreateWorkspace")
	sc, ok := ws.AgentCoordinator.(agent.SwarmConfigurable)
	require.True(t, ok)
	require.True(t, sc.SwarmWired(), "CreateWorkspace must wire the swarm backend without a follow-up InitAgent")

	// Re-init (as every connecting client does via /agent/init) must
	// keep the SAME coordinator: a rebuild would orphan any run already
	// dispatched on it (e.g. a replayed journaled queue) from the
	// busy/drain/teardown paths, which consult ws.AgentCoordinator.
	before := ws.AgentCoordinator
	require.NoError(t, b.InitAgent(t.Context(), ws.ID))
	require.Same(t, before, ws.AgentCoordinator, "InitAgent must not rebuild an existing coordinator")
	sc, ok = ws.AgentCoordinator.(agent.SwarmConfigurable)
	require.True(t, ok)
	require.True(t, sc.SwarmWired(), "swarm wiring must survive InitAgent")
}

// TestCreateWorkspace_ReuseRewiresSwarm guards the regression where a
// client switching to an already-attached workspace (e.g. the TUI's
// workspace/session switcher) went through CreateWorkspace's dedup
// ("first wins") reuse branch, which returned the cached *Workspace
// without ever re-checking or re-running wireSwarmBackend. If that
// workspace's swarm backend had not been wired yet — most visibly
// after a crash/restart that lost the in-memory wiring race — the
// swarm tool would silently and permanently disappear for every
// subsequent switch back to it, with no error surfaced anywhere.
func TestCreateWorkspace_ReuseRewiresSwarm(t *testing.T) {
	isolateConfigHome(t)

	wd := t.TempDir()
	writeSwarmProject(t, wd)

	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	ws, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: uuid.New().String(),
		Path:     wd,
		DataDir:  filepath.Join(wd, ".crush"),
	})
	require.NoError(t, err)

	sc, ok := ws.AgentCoordinator.(agent.SwarmConfigurable)
	require.True(t, ok)

	// Simulate the lost-wiring state directly: clear the swarm
	// backend on the already-attached workspace, as if the earlier
	// wireSwarmBackend call in CreateWorkspace had never run (the
	// race window this test guards) or the process had restarted
	// without persisting the in-memory wiring.
	require.NoError(t, sc.SetSwarmBackend(t.Context(), nil, ws.ID, nil))
	require.False(t, sc.SwarmWired(), "precondition: swarm backend must be cleared")

	// A second client "switching to" the same path takes the dedup
	// reuse branch, not the fresh-creation path.
	ws2, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: uuid.New().String(),
		Path:     wd,
		DataDir:  filepath.Join(wd, ".crush"),
	})
	require.NoError(t, err)
	require.Equal(t, ws.ID, ws2.ID, "must reuse the already-running workspace")

	sc2, ok := ws2.AgentCoordinator.(agent.SwarmConfigurable)
	require.True(t, ok)
	require.True(t, sc2.SwarmWired(), "CreateWorkspace's reuse branch must re-wire a workspace whose swarm backend was lost")
}

// TestCreateWorkspace_ConcurrentCreateBothRewireSwarm exercises the
// *second* dedup branch (the "recheck under lock, lost the race"
// path), which the sequential test above cannot reach: that branch
// only runs when two callers both pass the initial unlocked pathIndex
// check for a path with no existing workspace, so it requires an
// actual concurrent race on a brand-new path. It guards against a
// regression where only one of the two near-identical dedup branches
// gets the swarm re-wiring fix (wrong variable, wrong branch, or
// dropped during a future merge) going undetected.
func TestCreateWorkspace_ConcurrentCreateBothRewireSwarm(t *testing.T) {
	isolateConfigHome(t)

	wd := t.TempDir()
	writeSwarmProject(t, wd)

	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	const n = 8
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
		mu    sync.Mutex
		ids   []string
		errs  []error
	)
	start.Add(1)
	done.Add(n)
	for range n {
		go func() {
			defer done.Done()
			start.Wait()
			ws, _, err := b.CreateWorkspace(proto.Workspace{
				ClientID: uuid.New().String(),
				Path:     wd,
				DataDir:  filepath.Join(wd, ".crush"),
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids = append(ids, ws.ID)
		}()
	}
	start.Done()
	done.Wait()

	require.Empty(t, errs)
	require.Len(t, ids, n)
	for _, id := range ids[1:] {
		require.Equal(t, ids[0], id, "all concurrent callers for the same path must dedup onto one workspace")
	}

	ws, err := b.GetWorkspace(ids[0])
	require.NoError(t, err)
	sc, ok := ws.AgentCoordinator.(agent.SwarmConfigurable)
	require.True(t, ok)
	require.True(t, sc.SwarmWired(), "the workspace surviving a CreateWorkspace race must end up with swarm wired, regardless of which dedup branch each racing caller took")
}
