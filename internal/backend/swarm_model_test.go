package backend_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
)

// writeSwarmRolesProject writes a crush.json that enables swarm and defines
// a custom provider with large/small plus a "scout" role.
func writeSwarmRolesProject(t *testing.T, dir string) {
	t.Helper()
	const cfg = `{
  "options": {"swarm": {"enabled": true}},
  "providers": {
    "dp": {
      "type": "openai-compat",
      "base_url": "http://127.0.0.1:0/v1",
      "api_key": "x",
      "models": [{"id": "big", "name": "Big"}, {"id": "tiny", "name": "Tiny"}]
    }
  },
  "models": {
    "large": {"provider": "dp", "model": "big"},
    "small": {"provider": "dp", "model": "big"},
    "scout": {"provider": "dp", "model": "tiny"}
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crush.json"), []byte(cfg), 0o644))
}

func TestCreateSwarmSession_ModelRef(t *testing.T) {
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

	// Default: no ref -> worker runs large (unchanged behavior).
	plain, err := b.CreateSwarmSession(t.Context(), ws.ID, "worker", "")
	require.NoError(t, err)
	require.Empty(t, plain.ModelRef)
	require.NotEmpty(t, plain.Color)

	// A role name is stored verbatim and survives a round trip.
	scout, err := b.CreateSwarmSession(t.Context(), ws.ID, "worker", "scout")
	require.NoError(t, err)
	require.Equal(t, "scout", scout.ModelRef)
	got, err := ws.Sessions.Get(t.Context(), scout.ID)
	require.NoError(t, err)
	require.Equal(t, "scout", got.ModelRef)

	// provider/model form is accepted too.
	qualified, err := b.CreateSwarmSession(t.Context(), ws.ID, "worker", "dp/tiny")
	require.NoError(t, err)
	require.Equal(t, "dp/tiny", qualified.ModelRef)

	// An unresolvable ref is rejected against the target config, leaving
	// no orphan session, with the typed error the HTTP layer maps to 400.
	before, err := ws.Sessions.List(t.Context())
	require.NoError(t, err)
	_, err = b.CreateSwarmSession(t.Context(), ws.ID, "bad", "ghost")
	require.ErrorIs(t, err, backend.ErrInvalidSessionModel)
	after, err := ws.Sessions.List(t.Context())
	require.NoError(t, err)
	require.Len(t, after, len(before))
}
