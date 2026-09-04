package backend

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
)

// recordingCoordinator records Cancel and CancelAll invocations so tests
// can assert which cancel path the backend took. All other methods are
// zero-value no-ops.
type recordingCoordinator struct {
	cancelCalls   []string
	cancelAllHits atomic.Int32
}

func (c *recordingCoordinator) Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}

func (c *recordingCoordinator) RunAccepted(ctx context.Context, accept *agent.AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}
func (c *recordingCoordinator) BeginAccepted(string) *agent.AcceptedRun { return nil }
func (c *recordingCoordinator) Cancel(sessionID string) {
	c.cancelCalls = append(c.cancelCalls, sessionID)
}

func (c *recordingCoordinator) CancelAll()                                    { c.cancelAllHits.Add(1) }
func (c *recordingCoordinator) SoftInterrupt(sessionID string)                {}
func (c *recordingCoordinator) IsBusy() bool                                  { return false }
func (c *recordingCoordinator) IsSessionBusy(string) bool                     { return false }
func (c *recordingCoordinator) IsSessionBusyOrAccepted(string) bool           { return false }
func (c *recordingCoordinator) QueuedPrompts(string) int                      { return 0 }
func (c *recordingCoordinator) QueuedPromptsList(string) []string             { return nil }
func (c *recordingCoordinator) ClearQueue(string)                             {}
func (c *recordingCoordinator) Summarize(context.Context, string) error       { return nil }
func (c *recordingCoordinator) RegenerateTitle(context.Context, string) error { return nil }
func (c *recordingCoordinator) Model() agent.Model                            { return agent.Model{} }
func (c *recordingCoordinator) UpdateModels(context.Context) error            { return nil }
func (c *recordingCoordinator) UpdateModelsWhenIdle(context.Context) (bool, error) {
	return false, nil
}
func (c *recordingCoordinator) SetGoal(string, string) {}
func (c *recordingCoordinator) ClearGoal(string)       {}
func (c *recordingCoordinator) GoalStatus(string) (string, int, int, bool) {
	return "", 0, 0, false
}

func TestCancelSession_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	require.ErrorIs(t, b.CancelSession("nope", "S1"), ErrWorkspaceNotFound)
}

func TestCancelSession_TargetsSession(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	coord := &recordingCoordinator{}
	ws := insertAgentWorkspace(t, b, coord)

	require.NoError(t, b.CancelSession(ws.ID, "S1"))
	require.Equal(t, []string{"S1"}, coord.cancelCalls)
	require.Zero(t, coord.cancelAllHits.Load(), "session cancel must not trigger CancelAll")
}

func TestCancelAllSessions_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	require.ErrorIs(t, b.CancelAllSessions("nope"), ErrWorkspaceNotFound)
}

func TestCancelAllSessions_CancelsEveryRun(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	coord := &recordingCoordinator{}
	ws := insertAgentWorkspace(t, b, coord)

	require.NoError(t, b.CancelAllSessions(ws.ID))
	require.Equal(t, int32(1), coord.cancelAllHits.Load())
	require.Empty(t, coord.cancelCalls, "workspace-wide cancel must not target a single session")
}

// TestCancelAllSessions_NilCoordinatorIsNoOp verifies the call is safe on
// a workspace whose agent was never initialized.
func TestCancelAllSessions_NilCoordinatorIsNoOp(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	ws := insertAgentWorkspace(t, b, nil)
	require.NoError(t, b.CancelAllSessions(ws.ID))
}
