package model

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
)

// archiveStubWorkspace records archive calls and returns a canned session
// list so archiveCurrentSession's switch-away target can be verified.
type archiveStubWorkspace struct {
	workspace.Workspace
	archived   []string
	sessions   []session.Session
	listErr    error
	archiveErr error
	// failIDs, when set, makes ArchiveSession fail for those specific IDs
	// (used to exercise collect-all-failures behavior).
	failIDs map[string]bool
	// markedRead records MarkSessionSeen calls; markFailIDs makes those
	// specific IDs fail.
	markedRead  []string
	markFailIDs map[string]bool
}

func (w *archiveStubWorkspace) MarkSessionSeen(_ context.Context, id string) error {
	if w.markFailIDs[id] {
		return errors.New("mark seen failed for " + id)
	}
	w.markedRead = append(w.markedRead, id)
	return nil
}

func (w *archiveStubWorkspace) ArchiveSession(_ context.Context, id string) error {
	if w.failIDs[id] {
		return errors.New("archive failed for " + id)
	}
	if w.archiveErr != nil {
		return w.archiveErr
	}
	w.archived = append(w.archived, id)
	return nil
}

func (w *archiveStubWorkspace) ListSessions(_ context.Context) ([]session.Session, error) {
	if w.listErr != nil {
		return nil, w.listErr
	}
	return w.sessions, nil
}

func (w *archiveStubWorkspace) ListWorkspaceOverviews(_ context.Context) ([]proto.WorkspaceOverview, error) {
	return nil, nil
}

func (w *archiveStubWorkspace) BaseDir() string { return "" }

func newArchiveTestUI(t *testing.T, ws *archiveStubWorkspace, activeID string) *UI {
	t.Helper()
	s := styles.CharmtonePantera()
	m := &UI{com: &common.Common{Styles: &s, Workspace: ws}}
	m.session = &session.Session{ID: activeID}
	return m
}

// TestArchiveCurrentSession_SwitchesToMostRecentRemaining verifies the
// active session is archived and the next target is the most-recently-
// updated remaining session (ListSessions is updated_at desc), skipping the
// archived one.
func TestArchiveCurrentSession_SwitchesToMostRecentRemaining(t *testing.T) {
	t.Parallel()
	ws := &archiveStubWorkspace{
		sessions: []session.Session{
			{ID: "cur"}, // archived one may still appear pre-refresh; skipped
			{ID: "recent"},
			{ID: "older"},
		},
	}
	m := newArchiveTestUI(t, ws, "cur")

	cmd := m.archiveCurrentSession()
	require.NotNil(t, cmd)
	msg := cmd()

	require.Equal(t, []string{"cur"}, ws.archived)
	archived, ok := msg.(activeSessionArchivedMsg)
	require.True(t, ok)
	require.NoError(t, archived.err)
	require.Equal(t, "recent", archived.nextSessionID)
}

// TestArchiveCurrentSession_NoRemaining verifies an empty next target when
// the archived session was the only one.
func TestArchiveCurrentSession_NoRemaining(t *testing.T) {
	t.Parallel()
	ws := &archiveStubWorkspace{
		sessions: []session.Session{{ID: "cur"}},
	}
	m := newArchiveTestUI(t, ws, "cur")

	msg := m.archiveCurrentSession()().(activeSessionArchivedMsg)
	require.NoError(t, msg.err)
	require.Equal(t, "", msg.nextSessionID)
}

// TestArchiveCurrentSession_ArchiveError verifies an archive failure is
// surfaced and no switch target is chosen.
func TestArchiveCurrentSession_ArchiveError(t *testing.T) {
	t.Parallel()
	ws := &archiveStubWorkspace{archiveErr: errors.New("boom")}
	m := newArchiveTestUI(t, ws, "cur")

	msg := m.archiveCurrentSession()().(activeSessionArchivedMsg)
	require.Error(t, msg.err)
	require.Equal(t, "", msg.nextSessionID)
	require.Empty(t, ws.archived)
}

// TestArchiveCurrentSession_ListErrorStillArchives verifies that when the
// post-archive session list fails, the archive still counts as done and the
// next target is simply empty (falls back to the empty landing state).
func TestArchiveCurrentSession_ListErrorStillArchives(t *testing.T) {
	t.Parallel()
	ws := &archiveStubWorkspace{listErr: errors.New("boom")}
	m := newArchiveTestUI(t, ws, "cur")

	msg := m.archiveCurrentSession()().(activeSessionArchivedMsg)
	require.NoError(t, msg.err)
	require.Equal(t, "", msg.nextSessionID)
	require.Equal(t, []string{"cur"}, ws.archived)
}

// TestArchiveSessionsCmd_CollectsAllFailures verifies the bulk archive
// attempts every id (does not abort on the first failure) and reports the
// individual failures, keeping successes out of the failed set.
func TestArchiveSessionsCmd_CollectsAllFailures(t *testing.T) {
	t.Parallel()
	ws := &archiveStubWorkspace{
		failIDs:  map[string]bool{"b": true},
		sessions: nil, // ListWorkspaceOverviews via embedded stub returns nil, nil
	}
	s := styles.CharmtonePantera()
	m := &UI{com: &common.Common{Styles: &s, Workspace: ws}}

	// a, b, c in deterministic order; b fails, a and c succeed.
	msg := m.archiveSessionsCmd([]string{"a", "b", "c"}, "", 0)().(sessionsArchivedMsg)

	require.Equal(t, 2, msg.succeeded)
	require.Equal(t, []string{"b"}, msg.failed)
	require.Equal(t, []string{"a", "c"}, ws.archived) // both non-failing archived
}
