package backend

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/registry"
)

func TestSessionBundleIncludesNestedSessions(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))

	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	now := time.Now().UnixMilli()
	exec := func(q string, args ...any) {
		_, err := conn.Exec(q, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at) VALUES ('root','Root',0,0,0,0,?,?)`, now, now)
	exec(`INSERT INTO sessions (id, parent_session_id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at) VALUES ('m1$$call1','root','agent',0,0,0,0,?,?)`, now, now)
	exec(`INSERT INTO sessions (id, parent_session_id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at) VALUES ('m2$$call2','m1$$call1','nested agent',0,0,0,0,?,?)`, now, now)
	exec(`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('m1','root','assistant','[]',?,?)`, now, now)
	exec(`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('m2','m1$$call1','assistant','[]',?,?)`, now, now)
	exec(`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('m3','m2$$call2','assistant','[]',?,?)`, now, now)
	require.NoError(t, db.Release(dataDir))
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj", DataDir: dataDir}))

	bundle, err := b.SessionBundle(ctx, "", "/proj", "root")
	require.NoError(t, err)
	require.Equal(t, "Root", bundle.Session.Title)
	require.Len(t, bundle.Messages, 1)
	require.Len(t, bundle.Children, 2, "nested agents at any depth are included")
	require.Len(t, bundle.Children["m2$$call2"], 1)
}
