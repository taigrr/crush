package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
)

// commitStubWorkspace records SwitchWorkspace calls and reports a fixed
// current-workspace root, so foreign-vs-current commit routing can be
// verified without a live backend.
type commitStubWorkspace struct {
	workspace.Workspace
	baseDir    string
	switchedTo string
}

func (w *commitStubWorkspace) BaseDir() string { return w.baseDir }

func (w *commitStubWorkspace) SwitchWorkspace(_ context.Context, path string) error {
	w.switchedTo = path
	return nil
}

func (w *commitStubWorkspace) PermissionSkipRequests() bool { return false }

func newCommitTestUI(ws *commitStubWorkspace) *UI {
	s := styles.CharmtonePantera()
	m := &UI{com: &common.Common{Styles: &s, Workspace: ws}}
	m.dialog = dialog.NewOverlay()
	return m
}

// TestCommitSearchResult_ForeignWorkspaceSwitches verifies that committing
// a hit whose workspace root differs from the current one routes through a
// workspace switch (switch-then-open), not a plain in-workspace load.
func TestCommitSearchResult_ForeignWorkspaceSwitches(t *testing.T) {
	t.Parallel()
	ws := &commitStubWorkspace{baseDir: "/current"}
	m := newCommitTestUI(ws)

	cmd := m.commitSearchResult(proto.SessionHit{
		SessionID:     "s-foreign",
		WorkspaceRoot: "/other",
	})
	require.NotNil(t, cmd)
	msg := cmd()

	switched, ok := msg.(workspaceSwitchedMsg)
	require.True(t, ok, "foreign-workspace commit must emit workspaceSwitchedMsg")
	require.Equal(t, "s-foreign", switched.sessionID)
	require.Equal(t, "/other", ws.switchedTo)
}

// TestCommitSearchResult_CurrentWorkspaceLoadsDirectly verifies that a hit
// in the current workspace does NOT switch workspaces.
func TestCommitSearchResult_CurrentWorkspaceLoadsDirectly(t *testing.T) {
	t.Parallel()
	ws := &commitStubWorkspace{baseDir: "/current"}
	m := newCommitTestUI(ws)

	cmd := m.commitSearchResult(proto.SessionHit{
		SessionID:     "s-local",
		WorkspaceRoot: "/current",
	})
	require.NotNil(t, cmd)

	// A current-workspace commit loads directly and never switches.
	require.Empty(t, ws.switchedTo)
}

// TestCommitSearchResult_EmptyRootLoadsDirectly verifies the defensive
// fallback: a hit with no workspace root loads in the current workspace
// rather than routing an empty root into SwitchWorkspace (which errors).
func TestCommitSearchResult_EmptyRootLoadsDirectly(t *testing.T) {
	t.Parallel()
	ws := &commitStubWorkspace{baseDir: "/current"}
	m := newCommitTestUI(ws)

	cmd := m.commitSearchResult(proto.SessionHit{SessionID: "s", WorkspaceRoot: ""})
	require.NotNil(t, cmd)
	require.Empty(t, ws.switchedTo)
}
