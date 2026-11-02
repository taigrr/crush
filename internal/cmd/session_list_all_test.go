package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/projects"
	"github.com/taigrr/crush/internal/session"
)

func seedProjectSession(t *testing.T, projectPath, title string) string {
	t.Helper()
	dataDir := filepath.Join(projectPath, ".crush")
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	_, err = session.NewService(db.New(conn), conn).Create(t.Context(), title)
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))
	require.NoError(t, projects.Register(projectPath, dataDir))
	return dataDir
}

func TestSessionListAllJSON(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	alpha := filepath.Join(t.TempDir(), "alpha")
	beta := filepath.Join(t.TempDir(), "beta")
	alphaDataDir := seedProjectSession(t, alpha, "alpha session")
	seedProjectSession(t, beta, "beta session")

	// A duplicate registration of the same data dir must not double-list.
	require.NoError(t, projects.Register(alpha+"-alias", alphaDataDir))
	// A project without a database is skipped silently.
	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, projects.Register(empty, filepath.Join(empty, ".crush")))

	sessionListAll = true
	sessionListJSON = true
	t.Cleanup(func() {
		sessionListAll = false
		sessionListJSON = false
	})

	var out, errOut bytes.Buffer
	sessionListCmd.SetOut(&out)
	sessionListCmd.SetErr(&errOut)
	sessionListCmd.SetContext(t.Context())
	require.NoError(t, runSessionList(sessionListCmd, nil))
	require.Empty(t, errOut.String())

	var got []sessionJSON
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 2)

	byTitle := map[string]string{}
	for _, s := range got {
		byTitle[s.Title] = s.Project
	}
	require.Contains(t, []string{alpha, alpha + "-alias"}, byTitle["alpha session"])
	require.Equal(t, beta, byTitle["beta session"])
}

func TestSessionListAllEmptyRegistry(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	var errOut bytes.Buffer
	list, err := collectAllSessions(t.Context(), &errOut)
	require.NoError(t, err)
	require.Empty(t, list)
	require.Empty(t, errOut.String())
}
