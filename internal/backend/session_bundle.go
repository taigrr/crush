package backend

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/transfer"
)

// SessionBundle is everything a client needs to show a session in one
// round trip: metadata, file history, read files, messages, and the
// messages of every nested agent-tool session (keyed by session ID).
// Remote sessions used to cost one request per piece and one more per
// nested tool, each a full network round trip.
type SessionBundle struct {
	Session   session.Session
	Files     []history.File
	ReadFiles []string
	Messages  []message.Message
	Children  map[string][]message.Message
}

// SessionBundle loads sessionID from the attached workspace workspaceID,
// or, when workspaceID is empty, from the workspace rooted at root
// (attached or registry-detached, read-only).
func (b *Backend) SessionBundle(ctx context.Context, workspaceID, root, sessionID string) (SessionBundle, error) {
	if sessionID == "" {
		return SessionBundle{}, fmt.Errorf("session bundle: session id is required")
	}
	var ws *Workspace
	if workspaceID != "" {
		w, err := b.GetWorkspace(workspaceID)
		if err != nil {
			return SessionBundle{}, err
		}
		ws = w
	} else {
		for _, w := range b.workspaces.Seq2() {
			if w.resolvedPath == root && w.App != nil {
				ws = w
				break
			}
		}
	}
	if ws != nil {
		if err := ws.Messages.FlushAll(ctx); err != nil {
			slog.Debug("Session bundle: flush failed, reading persisted messages", "error", err)
		}
		bundle, err := loadBundle(ctx, ws.DB(), ws.Sessions, ws.History, ws.Messages, sessionID)
		if err != nil {
			return SessionBundle{}, err
		}
		if ws.FileTracker != nil {
			bundle.ReadFiles, _ = ws.FileTracker.ListReadFiles(ctx, sessionID)
		}
		return bundle, nil
	}

	dataDir, found, err := b.registryDataDirForRoot(root)
	if err != nil {
		return SessionBundle{}, fmt.Errorf("session bundle: %w", err)
	}
	if !found {
		return SessionBundle{}, ErrPreviewWorkspaceNotFound
	}
	conn, err := db.OpenReadOnly(dataDir)
	if err != nil {
		return SessionBundle{}, fmt.Errorf("session bundle: %w", err)
	}
	if conn == nil {
		return SessionBundle{}, nil
	}
	defer conn.Close()
	q := db.New(conn)
	return loadBundle(ctx, conn, session.NewService(q, conn), history.NewService(q, conn), message.NewService(q), sessionID)
}

func loadBundle(ctx context.Context, conn *sql.DB, sessions session.Service, hist history.Service, msgs message.Service, sessionID string) (SessionBundle, error) {
	sess, err := sessions.Get(ctx, sessionID)
	if err != nil {
		return SessionBundle{}, err
	}
	files, err := hist.ListBySession(ctx, sessionID)
	if err != nil {
		return SessionBundle{}, err
	}
	messages, err := msgs.List(ctx, sessionID)
	if err != nil {
		return SessionBundle{}, err
	}
	ids, err := transfer.SessionTree(ctx, conn, sessionID)
	if err != nil {
		return SessionBundle{}, err
	}
	children := make(map[string][]message.Message, len(ids))
	for _, id := range ids[1:] {
		cm, err := msgs.List(ctx, id)
		if err != nil {
			return SessionBundle{}, err
		}
		children[id] = cm
	}
	return SessionBundle{Session: sess, Files: files, Messages: messages, Children: children}, nil
}
