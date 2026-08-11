package backend

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/registry"
	"github.com/taigrr/crush/internal/session"
)

// insertAttachedMessageWorkspace installs a synthetic, ATTACHED workspace
// backed by a real (open) database at root, with one session and one
// message, so PeekSessionMessages' attached branch can be exercised without
// a full app.New boot.
func insertAttachedMessageWorkspace(t *testing.T, b *Backend, root, sessionID string) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir) })

	q := db.New(conn)
	_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: sessionID, Title: "attached"})
	require.NoError(t, err)

	messages := message.NewService(q)
	_, err = messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hi from attached"}},
	})
	require.NoError(t, err)

	ws := &Workspace{
		ID:           uuid.New().String(),
		Path:         root,
		resolvedPath: root,
		clients:      make(map[string]*clientState),
		shutdownFn:   func() {},
	}
	ws.App = &app.App{
		Sessions: session.NewService(q, conn),
		Messages: messages,
	}
	ws.ctx, ws.cancel = context.WithCancel(b.ctx)
	b.mu.Lock()
	b.workspaces.Set(ws.ID, ws)
	b.pathIndex[root] = ws.ID
	b.mu.Unlock()
}

// TestPeekSessionMessages_Attached verifies a session in an already-
// attached workspace is read from its live in-memory service.
func TestPeekSessionMessages_Attached(t *testing.T) {
	t.Cleanup(db.ResetPool)
	b, _ := newTestBackend(t)
	insertAttachedMessageWorkspace(t, b, "/proj/attached", "s1")

	msgs, err := b.PeekSessionMessages(context.Background(), "/proj/attached", "s1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "hi from attached", msgs[0].Content().String())
}

// TestPeekSessionMessages_Detached verifies a session in a registry-known
// but not-attached workspace is read READ-ONLY, without attaching it.
func TestPeekSessionMessages_Detached(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()

	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: "s1", Title: "detached"})
	require.NoError(t, err)
	messages := message.NewService(q)
	_, err = messages.Create(ctx, "s1", message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hi from detached"}},
	})
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))
	db.ResetPool()

	b := newTestBackendWithRegistry(t)
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/detached", DataDir: dataDir}))

	msgs, err := b.PeekSessionMessages(ctx, "/proj/detached", "s1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "hi from detached", msgs[0].Content().String())

	// Peeking must not have attached the workspace.
	require.Empty(t, b.workspaces.Len())
}

// TestPeekSessionMessages_UnknownWorkspace verifies an unrecognized root
// returns ErrPreviewWorkspaceNotFound rather than a generic error.
func TestPeekSessionMessages_UnknownWorkspace(t *testing.T) {
	b := newTestBackendWithRegistry(t)
	_, err := b.PeekSessionMessages(context.Background(), "/nowhere", "s1")
	require.ErrorIs(t, err, ErrPreviewWorkspaceNotFound)
}
