package backend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/question"
)

// waitAttention drains the attention channel until an event matching
// sessionID and kind arrives, returning it (or failing on timeout).
func waitAttention(t *testing.T, ch <-chan pubsub.Event[proto.AttentionEvent], sessionID string, kind proto.AttentionKind) proto.AttentionEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Payload.SessionID == sessionID && ev.Payload.Kind == kind {
				return ev.Payload
			}
		case <-deadline:
			t.Fatalf("timed out waiting for attention %s/%s", sessionID, kind)
		}
	}
}

func newAttentionTestWorkspace(id, root string) *Workspace {
	return &Workspace{
		ID:   id,
		Path: root,
		App: &app.App{
			Permissions: permission.NewPermissionService("/tmp", false, nil),
			Questions:   question.NewQuestionService(),
		},
		clients: make(map[string]*clientState),
	}
}

// TestAttentionForwarder_BroadcastsBlockedAndResolved verifies the
// per-workspace forwarder republishes a permission block (tagged with
// the originating workspace) and its resolution onto the backend's
// global attention channel.
func TestAttentionForwarder_BroadcastsBlockedAndResolved(t *testing.T) {
	t.Parallel()
	b := &Backend{attention: pubsub.NewBroker[proto.AttentionEvent]()}
	ws := newAttentionTestWorkspace("ws-1", "/root/one")

	sub := b.AttentionEvents(t.Context())
	b.startAttentionForwarder(ws)

	go func() {
		_, _ = ws.Permissions.Request(t.Context(), permission.CreatePermissionRequest{
			SessionID:  "s1",
			ToolCallID: "tc1",
			ToolName:   "view",
			Action:     "read",
			Path:       "/tmp",
		})
	}()

	blocked := waitAttention(t, sub, "s1", proto.AttentionBlockedPermission)
	require.Equal(t, "ws-1", blocked.WorkspaceID)
	require.Equal(t, "/root/one", blocked.WorkspaceRoot)
	require.Equal(t, "tc1", blocked.ToolCallID)

	// Resolving the workspace's pending prompts must republish resolved.
	ws.Permissions.CancelAll()
	resolved := waitAttention(t, sub, "s1", proto.AttentionResolved)
	require.Equal(t, "ws-1", resolved.WorkspaceID)
}

// TestAttentionForwarder_QuestionBlockedAndResolved mirrors the
// permission test for the question service.
func TestAttentionForwarder_QuestionBlockedAndResolved(t *testing.T) {
	t.Parallel()
	b := &Backend{attention: pubsub.NewBroker[proto.AttentionEvent]()}
	ws := newAttentionTestWorkspace("ws-q", "/root/q")

	sub := b.AttentionEvents(t.Context())
	b.startAttentionForwarder(ws)

	go func() {
		_, _ = ws.Questions.Ask(t.Context(), question.CreateQuestionRequest{
			SessionID:  "sq",
			ToolCallID: "tcq",
			Kind:       question.KindYesNo,
			Prompt:     "ok?",
		})
	}()

	blocked := waitAttention(t, sub, "sq", proto.AttentionBlockedQuestion)
	require.Equal(t, "ws-q", blocked.WorkspaceID)

	ws.Questions.CancelAll()
	waitAttention(t, sub, "sq", proto.AttentionResolved)
}

// TestPublishAttention_TagsWorkspace verifies busy/idle events carry the
// originating workspace id/root.
func TestPublishAttention_TagsWorkspace(t *testing.T) {
	t.Parallel()
	b := &Backend{attention: pubsub.NewBroker[proto.AttentionEvent]()}
	ws := newAttentionTestWorkspace("ws-b", "/root/b")

	sub := b.AttentionEvents(t.Context())
	b.publishAttention(ws, "sb", "", proto.AttentionBusy)

	ev := waitAttention(t, sub, "sb", proto.AttentionBusy)
	require.Equal(t, "ws-b", ev.WorkspaceID)
	require.Equal(t, "/root/b", ev.WorkspaceRoot)
}
