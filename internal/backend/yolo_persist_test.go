package backend_test

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
)

// TestYolo_PersistsAcrossAutoclose verifies that enabling yolo (permission
// skip-requests) at runtime survives a workspace idle-teardown and
// recreate — the exact "it disappears when the workspace autocloses"
// regression. The runtime toggle lives only on the live config override
// and permission service (both destroyed on teardown), so the backend
// remembers it per-root and restores it on the next CreateWorkspace.
func TestYolo_PersistsAcrossAutoclose(t *testing.T) {
	isolateConfigHome(t)

	wd := t.TempDir()
	dataDir := filepath.Join(wd, ".crush")

	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	cid := uuid.New().String()
	ws, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: cid,
		Path:     wd,
		DataDir:  dataDir,
	})
	require.NoError(t, err)

	// Yolo starts off (no --yolo flag).
	skip, err := b.GetPermissionsSkip(ws.ID)
	require.NoError(t, err)
	require.False(t, skip)

	// User enables yolo at runtime.
	require.NoError(t, b.SetPermissionsSkip(ws.ID, true))

	// Workspace autocloses (last client's hold released -> idle teardown).
	require.NoError(t, b.DeleteWorkspace(ws.ID, cid))

	// Recreate the workspace at the same path with NO yolo flag — as an
	// autoclose/reopen would. Yolo must be restored from the remembered
	// per-root state.
	cid2 := uuid.New().String()
	ws2, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: cid2,
		Path:     wd,
		DataDir:  dataDir,
	})
	require.NoError(t, err)
	require.NotEqual(t, ws.ID, ws2.ID, "a fresh workspace was created after teardown")

	skip, err = b.GetPermissionsSkip(ws2.ID)
	require.NoError(t, err)
	require.True(t, skip, "yolo must persist across autoclose/recreate")
}

// TestYolo_DisableAlsoPersists verifies turning yolo back off is remembered
// too (the map records the exact toggle, not a one-way latch).
func TestYolo_DisableAlsoPersists(t *testing.T) {
	isolateConfigHome(t)

	wd := t.TempDir()
	dataDir := filepath.Join(wd, ".crush")

	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)
	t.Cleanup(b.Shutdown)

	// Start in yolo via the flag.
	cid := uuid.New().String()
	ws, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: cid,
		Path:     wd,
		DataDir:  dataDir,
		YOLO:     true,
	})
	require.NoError(t, err)
	skip, err := b.GetPermissionsSkip(ws.ID)
	require.NoError(t, err)
	require.True(t, skip)

	// Turn it off at runtime, then autoclose.
	require.NoError(t, b.SetPermissionsSkip(ws.ID, false))
	require.NoError(t, b.DeleteWorkspace(ws.ID, cid))

	// Recreate WITH the yolo flag again: the remembered runtime-off does
	// not override an explicit flag (args.YOLO wins), so it comes back on.
	cid2 := uuid.New().String()
	ws2, _, err := b.CreateWorkspace(proto.Workspace{
		ClientID: cid2,
		Path:     wd,
		DataDir:  dataDir,
		YOLO:     true,
	})
	require.NoError(t, err)
	skip, err = b.GetPermissionsSkip(ws2.ID)
	require.NoError(t, err)
	require.True(t, skip, "explicit --yolo flag still wins on recreate")
}
