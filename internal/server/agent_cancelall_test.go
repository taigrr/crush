package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
)

// cancelRecordingCoordinator records Cancel and CancelAll invocations.
type cancelRecordingCoordinator struct {
	runCoordinator
	cancelSession atomic.Value // string
	cancelAllHits atomic.Int32
}

func (c *cancelRecordingCoordinator) Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}

func (c *cancelRecordingCoordinator) RunAccepted(ctx context.Context, accept *agent.AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}

func (c *cancelRecordingCoordinator) Cancel(sessionID string)        { c.cancelSession.Store(sessionID) }
func (c *cancelRecordingCoordinator) CancelAll()                     { c.cancelAllHits.Add(1) }
func (c *cancelRecordingCoordinator) SoftInterrupt(sessionID string) {}

// TestHandlePostWorkspaceAgentCancel_CancelsAll verifies the
// workspace-wide cancel route reaches coordinator.CancelAll and returns
// 200.
func TestHandlePostWorkspaceAgentCancel_CancelsAll(t *testing.T) {
	t.Parallel()

	coord := &cancelRecordingCoordinator{}
	c, wsID := buildAgentWorkspace(t, coord)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+wsID+"/agent/cancel", nil)
	req.SetPathValue("id", wsID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceAgentCancel(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int32(1), coord.cancelAllHits.Load())
	require.Nil(t, coord.cancelSession.Load(), "workspace-wide cancel must not target a session")
}

// TestHandlePostWorkspaceAgentSessionCancel_CancelsSession verifies the
// per-session cancel route reaches coordinator.Cancel with the session id.
func TestHandlePostWorkspaceAgentSessionCancel_CancelsSession(t *testing.T) {
	t.Parallel()

	coord := &cancelRecordingCoordinator{}
	c, wsID := buildAgentWorkspace(t, coord)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+wsID+"/agent/sessions/S1/cancel", nil)
	req.SetPathValue("id", wsID)
	req.SetPathValue("sid", "S1")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceAgentSessionCancel(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "S1", coord.cancelSession.Load())
	require.Zero(t, coord.cancelAllHits.Load())
}
