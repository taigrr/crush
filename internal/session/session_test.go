package session

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

func TestSetFavoritePersistsAndPublishes(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	require.False(t, created.Favorite)

	require.NoError(t, sessions.SetFavorite(t.Context(), created.ID, true))
	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, fetched.Favorite)

	require.NoError(t, sessions.SetFavorite(t.Context(), created.ID, false))
	fetched, err = sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.False(t, fetched.Favorite)
}

func TestEstimatedUsageStateSurvivesFetchModifySave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, fetched.EstimatedUsage)

	fetched.Todos = []Todo{{
		Content:    "Check estimate state",
		Status:     TodoStatusInProgress,
		ActiveForm: "Checking estimate state",
	}}

	updated, err := sessions.Save(t.Context(), fetched)
	require.NoError(t, err)
	require.True(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, refetched.EstimatedUsage)
}

func TestEstimatedUsageStateCanBeClearedByExplicitSave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	saved.EstimatedUsage = false
	updated, err := sessions.Save(t.Context(), saved)
	require.NoError(t, err)
	require.False(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.False(t, refetched.EstimatedUsage)
}

func TestCreateWithOptionsStampsLineageAndWorkingDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	// The Created payload must already carry every stamp so subscribers
	// see one consistent row.
	events := sessions.Subscribe(t.Context())
	spawned, err := sessions.CreateWithOptions(t.Context(), "worker", CreateOptions{
		ModelRef:             "scout",
		SpawnedBySessionID:   "spawner",
		SpawnedByWorkspaceID: "ws-1",
		WorkingDir:           "/proj/wt-linked2",
	})
	require.NoError(t, err)
	require.Equal(t, "spawner", spawned.SpawnedBySessionID)
	require.Equal(t, "ws-1", spawned.SpawnedByWorkspaceID)
	require.Equal(t, "/proj/wt-linked2", spawned.WorkingDir)
	require.Equal(t, "scout", spawned.ModelRef)
	require.Empty(t, spawned.ParentSessionID, "lineage must not make the session a hidden sub-session")

	ev := <-events
	require.Equal(t, spawned.ID, ev.Payload.ID)
	require.Equal(t, "spawner", ev.Payload.SpawnedBySessionID)
	require.Equal(t, "/proj/wt-linked2", ev.Payload.WorkingDir)
	require.Equal(t, "scout", ev.Payload.ModelRef)

	fetched, err := sessions.Get(t.Context(), spawned.ID)
	require.NoError(t, err)
	require.Equal(t, "spawner", fetched.SpawnedBySessionID)
	require.Equal(t, "ws-1", fetched.SpawnedByWorkspaceID)
	require.Equal(t, "/proj/wt-linked2", fetched.WorkingDir)

	// Spawned sessions stay in the top-level list.
	listed, err := sessions.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, spawned.ID, listed[0].ID)

	// A workspace id without a session id is never stored on its own.
	half, err := sessions.CreateWithOptions(t.Context(), "half", CreateOptions{SpawnedByWorkspaceID: "ws-1"})
	require.NoError(t, err)
	require.Empty(t, half.SpawnedBySessionID)
	require.Empty(t, half.SpawnedByWorkspaceID)
	require.Empty(t, half.ModelRef)

	// CreateWithModelRef is the single-stamp form of the same insert.
	viaRef, err := sessions.CreateWithModelRef(t.Context(), "ref", "openai/gpt-5")
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-5", viaRef.ModelRef)
	require.Empty(t, viaRef.SpawnedBySessionID)

	// SetSpawnedBy stamps after the fact and publishes.
	require.NoError(t, sessions.SetSpawnedBy(t.Context(), half.ID, "later", "ws-2"))
	fetched, err = sessions.Get(t.Context(), half.ID)
	require.NoError(t, err)
	require.Equal(t, "later", fetched.SpawnedBySessionID)
	require.Equal(t, "ws-2", fetched.SpawnedByWorkspaceID)
}
