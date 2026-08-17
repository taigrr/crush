package dialog

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles/themes"
	"github.com/taigrr/crush/internal/workspace"
)

// pickerStubWorkspace is a minimal workspace.Workspace for picker tests.
type pickerStubWorkspace struct {
	workspace.Workspace
	sessions   []session.Session
	archived   []session.Session
	archiveErr map[string]bool
	archived2  []string // records archived ids
}

func (w *pickerStubWorkspace) ListSessions(context.Context) ([]session.Session, error) {
	return w.sessions, nil
}

func (w *pickerStubWorkspace) ListArchivedSessions(context.Context) ([]session.Session, error) {
	return w.archived, nil
}

func (w *pickerStubWorkspace) ArchiveSession(_ context.Context, id string) error {
	if w.archiveErr[id] {
		return context.DeadlineExceeded
	}
	w.archived2 = append(w.archived2, id)
	return nil
}

func (w *pickerStubWorkspace) AgentIsReady() bool             { return false }
func (w *pickerStubWorkspace) AgentIsSessionBusy(string) bool { return false }

func newTestPicker(t *testing.T, ws *pickerStubWorkspace, activeID string) *Session {
	t.Helper()
	sty := themes.CharmtonePantera()
	com := &common.Common{Styles: &sty, Workspace: ws}
	s, err := NewSessions(com, activeID)
	require.NoError(t, err)
	// Give the list a size so selection/scroll math is well-defined.
	s.list.SetSize(40, 20)
	return s
}

func keyPress(r rune) tea.KeyPressMsg  { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func ctrlPress(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

func sampleSessions() []session.Session {
	return []session.Session{
		{ID: "s1", Title: "One"},
		{ID: "s2", Title: "Two"},
		{ID: "s3", Title: "Three"},
	}
}

// TestPicker_EnterSelectModeViaCtrlV verifies ctrl+v switches into select
// mode with an empty selection.
func TestPicker_EnterSelectModeViaCtrlV(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v'))
	require.Equal(t, sessionsModeSelecting, s.sessionsMode)
	require.Equal(t, 0, s.selection.Count())
}

// TestPicker_SpaceTogglesDiscrete verifies space toggles the current row in
// and out of a non-contiguous selection.
func TestPicker_SpaceTogglesDiscrete(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v')) // cursor on s1 (index 0)
	s.HandleMsg(keyPress(' '))  // toggle s1
	require.True(t, s.selection.Selected("s1"))
	s.HandleMsg(keyPress(' ')) // toggle s1 off
	require.False(t, s.selection.Selected("s1"))
}

// TestPicker_VisualSweep verifies v enters visual mode and j sweeps a
// contiguous range.
func TestPicker_VisualSweep(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v'))                     // select mode, cursor s1
	s.HandleMsg(keyPress('v'))                      // visual anchor s1
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}) // -> s2, sweep s1..s2
	require.ElementsMatch(t, []string{"s1", "s2"}, s.selection.IDs())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}) // -> s3
	require.ElementsMatch(t, []string{"s1", "s2", "s3"}, s.selection.IDs())
}

// TestPicker_EscExitsSelectClearsSelection verifies esc leaves select mode
// and drops the selection (returning to normal mode, not closing).
func TestPicker_EscExitsSelectClearsSelection(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.HandleMsg(keyPress(' '))
	require.Equal(t, 1, s.selection.Count())
	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, action, "esc in select mode should not close the dialog")
	require.Equal(t, sessionsModeNormal, s.sessionsMode)
	require.Equal(t, 0, s.selection.Count())
}

// TestPicker_EscInNormalModeCloses verifies esc still closes the dialog when
// not in select mode.
func TestPicker_EscInNormalModeCloses(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, ok := action.(ActionClose)
	require.True(t, ok)
}

// TestPicker_ArchivableSelectionSkipsActiveAndUnknown verifies the bulk
// gathering excludes the active session and unknown ids, sorted.
func TestPicker_ArchivableSelectionSkipsActiveAndUnknown(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.selection.Toggle("s1")    // active -> skipped
	s.selection.Toggle("s2")    // archivable
	s.selection.Toggle("s3")    // archivable
	s.selection.Toggle("ghost") // unknown -> skipped

	ids, skipped := s.archivableSelection()
	require.Equal(t, []string{"s2", "s3"}, ids)
	require.Equal(t, 2, skipped)
}

// TestPicker_ArchiveSelectedArchivesAllAndExits verifies `a` archives the
// selection, removes them from the active list, and exits select mode.
func TestPicker_ArchiveSelectedArchivesAllAndExits(t *testing.T) {
	t.Parallel()
	ws := &pickerStubWorkspace{sessions: sampleSessions()}
	s := newTestPicker(t, ws, "s1")
	s.HandleMsg(ctrlPress('v'))
	// Select s2 and s3 (s1 is active, would be skipped anyway).
	s.selection.Toggle("s2")
	s.selection.Toggle("s3")

	// `a` fires the archive command; state is reconciled only when the
	// resulting pickerBulkArchivedMsg comes back (nothing moves yet).
	action := s.HandleMsg(keyPress('a'))
	cmd, ok := action.(ActionCmd)
	require.True(t, ok)
	require.NotNil(t, cmd.Cmd)

	resultMsg := cmd.Cmd()
	bulk, ok := resultMsg.(pickerBulkArchivedMsg)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"s2", "s3"}, bulk.succeeded)
	require.Empty(t, bulk.failed)
	require.ElementsMatch(t, []string{"s2", "s3"}, ws.archived2)

	// Feed the result back into the dialog to reconcile local state.
	s.HandleMsg(bulk)
	require.Equal(t, sessionsModeNormal, s.sessionsMode, "full success exits select mode")
	require.Equal(t, 0, s.selection.Count())
	remaining := make([]string, len(s.sessions))
	for i, sess := range s.sessions {
		remaining[i] = sess.ID
	}
	require.Equal(t, []string{"s1"}, remaining)
	require.Equal(t, 0, s.list.Selected(), "cursor snapped to first row")
}

// TestPicker_ArchivePartialFailureKeepsFailedSelected verifies that on a
// partial failure the failed sessions stay active and re-selected for retry,
// staying in select mode.
func TestPicker_ArchivePartialFailureKeepsFailedSelected(t *testing.T) {
	t.Parallel()
	ws := &pickerStubWorkspace{
		sessions:   sampleSessions(),
		archiveErr: map[string]bool{"s3": true}, // s3 fails
	}
	s := newTestPicker(t, ws, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.selection.Toggle("s2")
	s.selection.Toggle("s3")

	action := s.HandleMsg(keyPress('a'))
	cmd := action.(ActionCmd)
	bulk := cmd.Cmd().(pickerBulkArchivedMsg)
	require.ElementsMatch(t, []string{"s2"}, bulk.succeeded)
	require.ElementsMatch(t, []string{"s3"}, bulk.failed)

	s.HandleMsg(bulk)
	require.Equal(t, sessionsModeSelecting, s.sessionsMode, "partial failure stays in select mode")
	require.True(t, s.selection.Selected("s3"), "failed session re-selected for retry")
	require.False(t, s.selection.Selected("s2"), "succeeded session not selected")
	// s2 archived (gone from active), s3 remains active.
	remaining := make([]string, len(s.sessions))
	for i, sess := range s.sessions {
		remaining[i] = sess.ID
	}
	require.ElementsMatch(t, []string{"s1", "s3"}, remaining)
}

// TestPicker_JKMoveInSelectMode drives real 'j'/'k' KeyPressMsg through the
// select-mode handler and asserts the cursor moves (regression: j/k were
// bound only to Next/Previous which don't include them).
func TestPicker_JKMoveInSelectMode(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v'))
	require.Equal(t, 0, s.list.Selected())

	s.HandleMsg(keyPress('j')) // down
	require.Equal(t, 1, s.list.Selected(), "j should move down")
	s.HandleMsg(keyPress('j'))
	require.Equal(t, 2, s.list.Selected())
	s.HandleMsg(keyPress('k')) // up
	require.Equal(t, 1, s.list.Selected(), "k should move up")
}

// TestPicker_JKSweepInVisualMode verifies j sweeps a contiguous range while
// in visual mode via the real key path.
func TestPicker_JKSweepInVisualMode(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.HandleMsg(keyPress('v')) // anchor s1
	s.HandleMsg(keyPress('j')) // -> s2, sweep
	s.HandleMsg(keyPress('j')) // -> s3, sweep
	require.ElementsMatch(t, []string{"s1", "s2", "s3"}, s.selection.IDs())
}

// TestPicker_ArchiveSelectedEmptyReports verifies archiving with only the
// active session selected archives nothing and reports.
func TestPicker_ArchiveSelectedEmptyReports(t *testing.T) {
	t.Parallel()
	ws := &pickerStubWorkspace{sessions: sampleSessions()}
	s := newTestPicker(t, ws, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.selection.Toggle("s1") // only the active session

	action := s.HandleMsg(keyPress('a'))
	require.Equal(t, sessionsModeNormal, s.sessionsMode)
	_, ok := action.(ActionCmd)
	require.True(t, ok)
	require.Empty(t, ws.archived2)
}

// TestPicker_ArchiveAllActiveCursorSkipsSeparator verifies that after
// archiving EVERY active session (possible when there's no active session so
// nothing is skipped), the cursor does not park on the "Archived" separator.
func TestPicker_ArchiveAllActiveCursorSkipsSeparator(t *testing.T) {
	t.Parallel()
	ws := &pickerStubWorkspace{
		sessions: sampleSessions(),
		archived: []session.Session{{ID: "old", Title: "Old"}},
	}
	// activeID "" => archivableSelection skips nothing; all can be archived.
	s := newTestPicker(t, ws, "")
	s.HandleMsg(ctrlPress('v'))
	s.selection.Toggle("s1")
	s.selection.Toggle("s2")
	s.selection.Toggle("s3")

	action := s.HandleMsg(keyPress('a'))
	bulk := action.(ActionCmd).Cmd().(pickerBulkArchivedMsg)
	require.ElementsMatch(t, []string{"s1", "s2", "s3"}, bulk.succeeded)

	s.HandleMsg(bulk)
	require.Empty(t, s.sessions, "all active sessions archived")
	require.False(t, s.isSelectedSeparator(), "cursor must not park on the Archived separator")
}

// TestPicker_PartialFailureClearsVisualAnchor verifies that after a partial
// failure the visual anchor is cleared (SetSelection resets it), so the next
// j does not extend a stale sweep.
func TestPicker_PartialFailureClearsVisualAnchor(t *testing.T) {
	t.Parallel()
	ws := &pickerStubWorkspace{
		sessions:   sampleSessions(),
		archiveErr: map[string]bool{"s3": true},
	}
	s := newTestPicker(t, ws, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.HandleMsg(keyPress('v'))                      // visual anchor s1
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}) // sweep -> s2
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}) // sweep -> s3 (s1,s2,s3)

	action := s.HandleMsg(keyPress('a'))
	bulk := action.(ActionCmd).Cmd().(pickerBulkArchivedMsg)
	require.ElementsMatch(t, []string{"s3"}, bulk.failed)
	s.HandleMsg(bulk)

	require.False(t, s.selection.Visual(), "visual mode cleared after reconcile")
	require.Equal(t, []string{"s3"}, s.selection.IDs(), "failed session stays selected")
	before := append([]string(nil), s.selection.IDs()...)
	// A plain j now should NOT extend any sweep (no anchor / not visual).
	s.HandleMsg(keyPress('j'))
	require.Equal(t, before, s.selection.IDs(), "post-archive j must not extend a stale sweep")
}

// TestPicker_PlainJKNoSelection verifies plain j/k movement in select mode
// (not visual) never adds to the selection.
func TestPicker_PlainJKNoSelection(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.HandleMsg(keyPress('j'))
	s.HandleMsg(keyPress('j'))
	s.HandleMsg(keyPress('k'))
	require.Equal(t, 0, s.selection.Count(), "plain j/k must not select anything")
}

// TestPicker_StaleSelectionPrunedOnSync verifies a selected id that is not a
// known active session is pruned by syncSelectionMarks, so it never reaches
// archivableSelection (no phantom "skipped" count) or pads the hint.
func TestPicker_StaleSelectionPrunedOnSync(t *testing.T) {
	t.Parallel()
	s := newTestPicker(t, &pickerStubWorkspace{sessions: sampleSessions()}, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.selection.Toggle("s2")    // real
	s.selection.Toggle("ghost") // phantom, not in s.sessions
	require.Equal(t, 2, s.selection.Count())

	s.syncSelectionMarks() // prunes unknown ids
	require.Equal(t, 1, s.selection.Count())
	require.True(t, s.selection.Selected("s2"))
	require.False(t, s.selection.Selected("ghost"))

	ids, skipped := s.archivableSelection()
	require.Equal(t, []string{"s2"}, ids)
	require.Equal(t, 0, skipped, "phantom id must not be counted as skipped")
}

// TestPicker_EscBeforeAsyncResultStaysNormal verifies that if the user exits
// select mode (esc) before an async partial-failure result arrives, the
// reconcile does NOT yank them back into select mode.
func TestPicker_EscBeforeAsyncResultStaysNormal(t *testing.T) {
	t.Parallel()
	ws := &pickerStubWorkspace{
		sessions:   sampleSessions(),
		archiveErr: map[string]bool{"s3": true},
	}
	s := newTestPicker(t, ws, "s1")
	s.HandleMsg(ctrlPress('v'))
	s.selection.Toggle("s2")
	s.selection.Toggle("s3")

	action := s.HandleMsg(keyPress('a'))
	bulk := action.(ActionCmd).Cmd().(pickerBulkArchivedMsg)

	// User exits select mode before the async result lands.
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, sessionsModeNormal, s.sessionsMode)

	// Now the (partial-failure) result arrives.
	s.HandleMsg(bulk)
	require.Equal(t, sessionsModeNormal, s.sessionsMode, "must not be yanked back into select mode")
	require.Equal(t, 0, s.selection.Count(), "selection dropped, not re-established")
	// s2 archived, s3 (failed) still active.
	remaining := make([]string, len(s.sessions))
	for i, sess := range s.sessions {
		remaining[i] = sess.ID
	}
	require.ElementsMatch(t, []string{"s1", "s3"}, remaining)
}
