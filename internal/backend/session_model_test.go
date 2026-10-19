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

func TestCreateSessionWithModel(t *testing.T) {
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

	plain, err := b.CreateSession(t.Context(), ws.ID, "plain")
	require.NoError(t, err)
	require.Empty(t, plain.ModelRef)
	require.NotEmpty(t, plain.Color, "the swarm identity is assigned before returning")
	require.NotEmpty(t, plain.Animal)

	scout, err := b.CreateSessionWithModel(t.Context(), ws.ID, "headless", " dp/tiny ")
	require.NoError(t, err)
	require.Equal(t, "dp/tiny", scout.ModelRef)
	got, err := ws.Sessions.Get(t.Context(), scout.ID)
	require.NoError(t, err)
	require.Equal(t, "dp/tiny", got.ModelRef)

	before, err := ws.Sessions.List(t.Context())
	require.NoError(t, err)
	_, err = b.CreateSessionWithModel(t.Context(), ws.ID, "bad", "ghost")
	require.ErrorIs(t, err, backend.ErrInvalidSessionModel)
	after, err := ws.Sessions.List(t.Context())
	require.NoError(t, err)
	require.Len(t, after, len(before), "an invalid model leaves no orphan session")

	_, err = b.CreateSessionWithModel(t.Context(), "nope", "x", "")
	require.ErrorIs(t, err, backend.ErrWorkspaceNotFound)
}
