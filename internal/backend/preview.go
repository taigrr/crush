package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
)

// ErrPreviewWorkspaceNotFound is returned by [Backend.PeekSessionMessages]
// when root does not match any attached or registry-known workspace.
var ErrPreviewWorkspaceNotFound = errors.New("preview: workspace not found")

// PeekSessionMessages returns a session's messages from the workspace
// rooted at root, WITHOUT switching, attaching, or otherwise disturbing
// the caller's own current workspace. This is what lets the session
// sidebar's live preview work for a session in a workspace the client
// isn't currently viewing: previewing used to be restricted to the
// current workspace precisely because fetching a foreign workspace's
// messages previously meant a full [ClientWorkspace.SwitchWorkspace] —
// too heavy to do on every debounced cursor move.
//
// root is matched against attached workspaces first (live, in-memory
// service — always up to date, including a session still streaming);
// if none match, root is looked up in the on-disk registry and read
// READ-ONLY via [db.OpenReadOnly] (no attach, no migrations, no lock),
// mirroring the same safe pattern as [Backend.ListWorkspaceOverviews] and
// [Backend.searchAllWorkspaces].
func (b *Backend) PeekSessionMessages(ctx context.Context, root, sessionID string) ([]message.Message, error) {
	if root == "" || sessionID == "" {
		return nil, fmt.Errorf("preview: root and session id are required")
	}

	for _, ws := range b.workspaces.Seq2() {
		if ws.resolvedPath != root || ws.App == nil {
			continue
		}
		// Drain debounced updates so a preview of a still-streaming
		// session shows the latest content rather than racing the
		// debounce timer in message.Service. A flush failure is not fatal
		// to a read-only preview: log and fall through to List, which
		// still returns the last-persisted messages.
		if err := ws.Messages.FlushAll(ctx); err != nil {
			slog.Debug("Preview: flush before list failed, reading persisted messages", "root", root, "error", err)
		}
		return ws.Messages.List(ctx, sessionID)
	}

	dataDir, found, err := b.registryDataDirForRoot(root)
	if err != nil {
		return nil, fmt.Errorf("preview: failed to list registry: %w", err)
	}
	if !found {
		return nil, ErrPreviewWorkspaceNotFound
	}
	conn, err := db.OpenReadOnly(dataDir)
	if err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	if conn == nil {
		// No database yet: an uninitialized/empty workspace has no
		// messages to preview.
		return nil, nil
	}
	defer conn.Close()
	queries := db.New(conn)
	messages := message.NewService(queries)
	return messages.List(ctx, sessionID)
}
