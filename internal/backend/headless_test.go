package backend

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/fantasy"
)

// policyCoordinator records the permission policy each run's context
// carries.
type policyCoordinator struct {
	*blockingCoordinator
	seen atomic.Value
}

func (c *policyCoordinator) RunAccepted(ctx context.Context, accept *agent.AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	c.seen.Store(permission.PolicyFromContext(ctx))
	return c.blockingCoordinator.RunAccepted(ctx, accept, sessionID, prompt, attachments...)
}

func insertPolicyWorkspace(t *testing.T, b *Backend) (*Workspace, *policyCoordinator) {
	t.Helper()
	coord := &policyCoordinator{blockingCoordinator: newBlockingCoordinator()}
	ws := insertAgentWorkspace(t, b, coord)
	ws.Permissions = permission.NewPermissionService(t.TempDir(), false, nil)
	return ws, coord
}

func waitForRunEntered(t *testing.T, coord *policyCoordinator) {
	t.Helper()
	select {
	case <-coord.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("run never started")
	}
}

func TestSendMessage_RunPolicyTravelsOnTheTurnContext(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	ws, coord := insertPolicyWorkspace(t, b)

	require.NoError(t, b.SendMessage(ws.ID, proto.AgentMessage{
		SessionID: "S1", Prompt: "hi", PermissionPolicy: proto.PermissionPolicyDeny,
	}))
	waitForRunEntered(t, coord)
	close(coord.release)
	require.Equal(t, permission.SessionPolicyDeny, coord.seen.Load())
	require.Equal(t, permission.SessionPolicyPrompt, ws.Permissions.SessionPolicy("S1"),
		"the policy never touches the session's stored default, so other turns are unaffected")
}

func TestSendMessage_WithoutPolicyCarriesNone(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	ws, coord := insertPolicyWorkspace(t, b)

	require.NoError(t, b.SendMessage(ws.ID, proto.AgentMessage{SessionID: "S1", Prompt: "hi"}))
	waitForRunEntered(t, coord)
	close(coord.release)
	require.Equal(t, permission.SessionPolicy(""), coord.seen.Load())
}

func TestSendMessage_InvalidPermissionPolicy(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	ws, coord := insertPolicyWorkspace(t, b)

	err := b.SendMessage(ws.ID, proto.AgentMessage{SessionID: "S1", Prompt: "hi", PermissionPolicy: "yolo"})
	require.ErrorIs(t, err, ErrInvalidPermissionPolicy)
	require.Zero(t, coord.runCount.Load(), "an invalid policy must reject the message before dispatch")
}

// fakeSessions implements just enough of session.Service for the
// ownership walk in PendingPrompts.
type fakeSessions struct {
	session.Service
	parents map[string]string
}

func (f fakeSessions) Get(_ context.Context, id string) (session.Session, error) {
	parent, ok := f.parents[id]
	if !ok {
		return session.Session{}, errors.New("not found")
	}
	return session.Session{ID: id, ParentSessionID: parent}, nil
}

func TestPendingPrompts_IncludesSubAgentSessions(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	ws := insertAgentWorkspace(t, b, newBlockingCoordinator())
	perms := permission.NewPermissionService(t.TempDir(), false, nil)
	questions := question.NewQuestionService()
	ws.Permissions = perms
	ws.Questions = questions
	ws.Sessions = fakeSessions{parents: map[string]string{
		"root":       "",
		"child":      "root",
		"grandchild": "child",
		"other":      "",
	}}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	permReqs := perms.Subscribe(ctx)
	questionReqs := questions.Subscribe(ctx)

	ask := func(sessionID, toolCallID string) {
		go func() {
			_, _ = perms.Request(ctx, permission.CreatePermissionRequest{
				SessionID: sessionID, ToolCallID: toolCallID, ToolName: "bash", Action: "execute", Path: "/tmp",
			})
		}()
		select {
		case <-permReqs:
		case <-time.After(2 * time.Second):
			t.Fatalf("permission request for %s was never published", sessionID)
		}
	}
	ask("grandchild", "tc-grandchild")
	ask("other", "tc-other")

	go func() {
		_, _ = questions.Ask(ctx, question.CreateQuestionRequest{
			SessionID: "root", ToolCallID: "tc-q", Kind: question.KindYesNo, Prompt: "Proceed?",
		})
	}()
	select {
	case <-questionReqs:
	case <-time.After(2 * time.Second):
		t.Fatal("question was never published")
	}

	pending, err := b.PendingPrompts(t.Context(), ws.ID, "root")
	require.NoError(t, err)
	require.Len(t, pending.Permissions, 1)
	require.Equal(t, "grandchild", pending.Permissions[0].SessionID)
	require.Equal(t, "tc-grandchild", pending.Permissions[0].ToolCallID)
	require.Len(t, pending.Questions, 1)
	require.Equal(t, "Proceed?", pending.Questions[0].Prompt)
	require.Equal(t, proto.QuestionKindYesNo, pending.Questions[0].Kind)
	require.False(t, pending.IsEmpty())

	pending, err = b.PendingPrompts(t.Context(), ws.ID, "child")
	require.NoError(t, err)
	require.Len(t, pending.Permissions, 1, "a mid-level session sees its descendants")
	require.Empty(t, pending.Questions, "but not its ancestors")

	pending, err = b.PendingPrompts(t.Context(), ws.ID, "unrelated")
	require.NoError(t, err)
	require.True(t, pending.IsEmpty())
	require.NotNil(t, pending.Permissions, "empty lists serialize as [] not null")
}

func TestPendingPrompts_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	_, err := b.PendingPrompts(t.Context(), "nope", "s")
	require.ErrorIs(t, err, ErrWorkspaceNotFound)
}
