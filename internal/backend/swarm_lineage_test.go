package backend_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
)

func gitForSwarmTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

// initSiblingWorktrees creates a git repo at <base>/main with a linked
// worktree at <base>/linked. Both collapse to the same project root (and
// therefore to one workspace), which is exactly the layout where a
// swarm-spawned session's working dir matters.
func initSiblingWorktrees(t *testing.T) (mainDir, linkedDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("git linked worktrees behave differently on windows test runners")
	}
	base := t.TempDir()
	mainDir = filepath.Join(base, "main")
	linkedDir = filepath.Join(base, "linked")
	require.NoError(t, os.MkdirAll(mainDir, 0o755))
	gitForSwarmTest(t, mainDir, "init", "-b", "main")
	writeSwarmProject(t, mainDir)
	gitForSwarmTest(t, mainDir, "add", ".")
	gitForSwarmTest(t, mainDir, "commit", "-m", "init")
	gitForSwarmTest(t, mainDir, "worktree", "add", "-b", "feature", linkedDir, "HEAD")
	// Resolve symlinks (macOS /var -> /private/var) so comparisons
	// against the canonical working dir the backend stores line up.
	var err error
	mainDir, err = filepath.EvalSymlinks(mainDir)
	require.NoError(t, err)
	linkedDir, err = filepath.EvalSymlinks(linkedDir)
	require.NoError(t, err)
	return mainDir, linkedDir
}

// TestCreateSwarmSessionAtPath_SiblingWorktreePinsWorkingDir is the
// regression test for the "worker runs in the wrong tree" bug: a
// workspace is already running for the main tree, a session spawns
// `swarm new` with path=<linked worktree>, and the spawned session must
// be pinned to the linked tree even though it shares the running
// workspace.
func TestCreateSwarmSessionAtPath_SiblingWorktreePinsWorkingDir(t *testing.T) {
	isolateConfigHome(t)
	mainDir, linkedDir := initSiblingWorktrees(t)

	srvCfg, err := config.Init(mainDir, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	ws, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: uuid.New().String(),
		Path:     mainDir,
	})
	require.NoError(t, err)

	gotID, sess, err := b.CreateSwarmSessionAtPath(t.Context(), linkedDir, backend.SwarmSpawnOptions{Title: "worker"})
	require.NoError(t, err)
	require.Equal(t, ws.ID, gotID, "sibling worktree must reuse the running workspace")
	require.Equal(t, linkedDir, sess.WorkingDir, "spawned session must be pinned to the linked worktree")

	// Persisted, not just returned.
	got, err := ws.Sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, linkedDir, got.WorkingDir)
}

// working_dir must resolve to the target workspace: a subdirectory and a
// linked worktree are accepted (and canonicalized); an unrelated
// directory, a file, and a missing path are refused without creating a
// session.
func TestCreateSwarmSession_WorkingDirValidation(t *testing.T) {
	isolateConfigHome(t)
	mainDir, linkedDir := initSiblingWorktrees(t)

	srvCfg, err := config.Init(mainDir, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	ws, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: uuid.New().String(),
		Path:     mainDir,
	})
	require.NoError(t, err)

	sub := filepath.Join(mainDir, "src", "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	for _, tc := range []struct {
		name string
		dir  string
		want string
	}{
		{"subdirectory", sub, sub},
		{"linked worktree", linkedDir, linkedDir},
		{"trailing slash is canonicalized", linkedDir + string(filepath.Separator), linkedDir},
	} {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			sess, err := b.CreateSwarmSession(t.Context(), ws.ID, backend.SwarmSpawnOptions{Title: "w", WorkingDir: tc.dir})
			require.NoError(t, err)
			require.Equal(t, tc.want, sess.WorkingDir)
		})
	}

	before, err := ws.Sessions.List(t.Context())
	require.NoError(t, err)

	outside := t.TempDir()
	file := filepath.Join(mainDir, "README.md")
	require.NoError(t, os.WriteFile(file, []byte("hi\n"), 0o644))

	for _, tc := range []struct {
		name string
		dir  string
		is   error
	}{
		{"unrelated directory", outside, backend.ErrSwarmWorkingDirOutside},
		{"file", file, nil},
		{"missing path", filepath.Join(mainDir, "does-not-exist"), nil},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			_, err := b.CreateSwarmSession(t.Context(), ws.ID, backend.SwarmSpawnOptions{Title: "w", WorkingDir: tc.dir})
			require.Error(t, err)
			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
			}
		})
	}

	after, err := ws.Sessions.List(t.Context())
	require.NoError(t, err)
	require.Len(t, after, len(before), "a rejected working_dir must not leave an orphan session")
}

// Lineage is stamped from the spawner and re-derived from the spawner's
// real session row when it can be found, so the recorded workspace id is
// the one the spawner actually lives in. parent_session_id stays NULL so
// the worker remains listed and swarm-addressable.
func TestCreateSwarmSession_StampsLineage(t *testing.T) {
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

	spawner, err := b.CreateSession(t.Context(), ws.ID, "orchestrator")
	require.NoError(t, err)

	worker, err := b.CreateSwarmSession(t.Context(), ws.ID, backend.SwarmSpawnOptions{
		Title:                "worker",
		SpawnedBySessionID:   spawner.ID,
		SpawnedByWorkspaceID: "forged-workspace-id",
	})
	require.NoError(t, err)
	require.Equal(t, spawner.ID, worker.SpawnedBySessionID)
	require.Equal(t, ws.ID, worker.SpawnedByWorkspaceID, "workspace id must come from the spawner's real row, not the caller")
	require.Empty(t, worker.ParentSessionID, "lineage must not mark the worker as a hidden sub-session")

	// Listed (not hidden like a task child) and addressable.
	listed, err := ws.Sessions.List(t.Context())
	require.NoError(t, err)
	var found bool
	for _, s := range listed {
		if s.ID == worker.ID {
			found = true
			require.Equal(t, spawner.ID, s.SpawnedBySessionID)
		}
	}
	require.True(t, found, "spawned worker must stay in the session list")
	res, err := b.LookupSwarmAddress(t.Context(), worker.ID)
	require.NoError(t, err)
	require.False(t, res.Sub)

	// The spawner itself has no lineage.
	require.Empty(t, spawner.SpawnedBySessionID)

	// A spawner that cannot be located keeps the claimed values rather
	// than dropping lineage.
	orphan, err := b.CreateSwarmSession(t.Context(), ws.ID, backend.SwarmSpawnOptions{
		Title:                "worker",
		SpawnedBySessionID:   "gone-session",
		SpawnedByWorkspaceID: "gone-workspace",
	})
	require.NoError(t, err)
	require.Equal(t, "gone-session", orphan.SpawnedBySessionID)
	require.Equal(t, "gone-workspace", orphan.SpawnedByWorkspaceID)
}
