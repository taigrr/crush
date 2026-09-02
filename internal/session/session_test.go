package session

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
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

func TestCreateWithModelStampsAndSetModelClears(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	// Plain Create leaves the stamp empty (historical behavior).
	plain, err := sessions.Create(t.Context(), "plain")
	require.NoError(t, err)
	require.Nil(t, plain.Model)

	// CreateWithModel stamps provider/model/effort and the stamp is
	// visible on the Created payload and on a fresh Get.
	want := &config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1", ReasoningEffort: "low"}
	stamped, err := sessions.CreateWithModel(t.Context(), "stamped", want)
	require.NoError(t, err)
	require.NotNil(t, stamped.Model)
	require.Equal(t, *want, *stamped.Model)

	fetched, err := sessions.Get(t.Context(), stamped.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched.Model)
	require.Equal(t, *want, *fetched.Model)

	// Task children can be stamped too, and keep their parent pointer.
	child, err := sessions.CreateTaskSessionWithModel(t.Context(), "call-1", stamped.ID, "child",
		&config.SelectedModel{Provider: "dp-claude", Model: "claude-haiku-4-5-20251001"})
	require.NoError(t, err)
	require.Equal(t, stamped.ID, child.ParentSessionID)
	require.NotNil(t, child.Model)
	require.Equal(t, "claude-haiku-4-5-20251001", child.Model.Model)

	// SetModel replaces the stamp; SetModel(nil) clears it.
	require.NoError(t, sessions.SetModel(t.Context(), stamped.ID,
		&config.SelectedModel{Provider: "dp-gpt", Model: "gpt-5.6-luna(low)"}))
	fetched, err = sessions.Get(t.Context(), stamped.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched.Model)
	require.Equal(t, "dp-gpt", fetched.Model.Provider)
	require.Equal(t, "", fetched.Model.ReasoningEffort)

	require.NoError(t, sessions.SetModel(t.Context(), stamped.ID, nil))
	fetched, err = sessions.Get(t.Context(), stamped.ID)
	require.NoError(t, err)
	require.Nil(t, fetched.Model)

	// An incomplete selection is treated as "unset", never persisted
	// half-written.
	half, err := sessions.CreateWithModel(t.Context(), "half", &config.SelectedModel{Provider: "dp-gpt"})
	require.NoError(t, err)
	require.Nil(t, half.Model)
}
