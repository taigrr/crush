package backend_test

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/swarm"
)

func TestResolveSwarmTarget(t *testing.T) {
	isolateConfigHome(t)
	wd := t.TempDir()
	writeSwarmRolesProject(t, wd)

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

	sess, err := b.CreateSession(t.Context(), ws.ID, "target")
	require.NoError(t, err)
	address := swarm.FormatAddress(swarm.Identity{Color: sess.Color, Animal: sess.Animal}, sess.ID)

	got, err := b.ResolveSwarmTarget(t.Context(), address)
	require.NoError(t, err)
	require.Equal(t, ws.ID, got.WorkspaceID)
	require.Equal(t, sess.ID, got.SessionID)
	require.Equal(t, address, got.Address)
	require.NotEmpty(t, got.Path)

	got, err = b.ResolveSwarmTarget(t.Context(), sess.ID)
	require.NoError(t, err, "raw session IDs resolve too")
	require.Equal(t, sess.ID, got.SessionID)

	child, err := ws.Sessions.CreateTaskSession(t.Context(), "tool-call", sess.ID, "child")
	require.NoError(t, err)
	_, err = b.ResolveSwarmTarget(t.Context(), child.ID)
	require.Error(t, err, "sub-agent sessions are never addressable")

	_, err = b.ResolveSwarmTarget(t.Context(), "nonexistent-animal-ffff")
	require.Error(t, err)

	_, err = b.ResolveSwarmTarget(t.Context(), "!")
	require.ErrorIs(t, err, backend.ErrSwarmAddressInvalid)
}
