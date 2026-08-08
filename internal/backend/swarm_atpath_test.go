package backend_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
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

	gotID, sess, err := b.CreateSwarmSessionAtPath(t.Context(), wd, "hello")
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

	gotID, sess, err := b.CreateSwarmSessionAtPath(t.Context(), target, "hello")
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

	// Re-init (as the HTTP /agent/init flow does) rebuilds the
	// coordinator; it must re-wire, not drop, the swarm backend.
	require.NoError(t, b.InitAgent(t.Context(), ws.ID))
	sc, ok = ws.AgentCoordinator.(agent.SwarmConfigurable)
	require.True(t, ok)
	require.True(t, sc.SwarmWired(), "InitAgent must re-wire the swarm backend after rebuilding the coordinator")
}
