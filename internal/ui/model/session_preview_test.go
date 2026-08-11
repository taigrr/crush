package model

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/workspace"
)

// previewStubWorkspace records ListMessages calls for preview/restore.
type previewStubWorkspace struct {
	workspace.Workspace
	messages   map[string][]message.Message
	listErrIDs map[string]bool
	calls      []string
}

func (w *previewStubWorkspace) ListMessages(_ context.Context, id string) ([]message.Message, error) {
	w.calls = append(w.calls, id)
	if w.listErrIDs[id] {
		return nil, context.DeadlineExceeded
	}
	return w.messages[id], nil
}

func (w *previewStubWorkspace) BaseDir() string { return "/current" }

func (w *previewStubWorkspace) PermissionSkipRequests() bool { return false }

// PeekMessages records calls tagged with their root so tests can assert a
// foreign-workspace preview went through the peek path (not ListMessages).
func (w *previewStubWorkspace) PeekMessages(_ context.Context, root, id string) ([]message.Message, error) {
	w.calls = append(w.calls, "peek:"+root+":"+id)
	if w.listErrIDs[id] {
		return nil, context.DeadlineExceeded
	}
	return w.messages[id], nil
}

func newPreviewTestUI(t *testing.T, ws *previewStubWorkspace, committedID string) *UI {
	t.Helper()
	m := newTestUI()
	m.com.Workspace = ws
	if committedID != "" {
		m.session = &session.Session{ID: committedID}
	}
	return m
}

// drainMsg runs a returned cmd (if any) and returns its message.
func drainMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestPreview_ScheduleSupersedesStaleTick verifies rapid moves bump the gen
// so an earlier tick is dropped and only the latest fires a load.
func TestPreview_ScheduleSupersedesStaleTick(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{}}
	m := newPreviewTestUI(t, ws, "committed")

	m.schedulePreview("s1", "") // gen 1, pending s1
	gen1 := m.previewGen
	m.schedulePreview("s2", "") // gen 2, pending s2
	require.Equal(t, "s2", m.pendingPreviewID)

	// The stale tick for gen1 must be dropped (no load).
	require.Nil(t, m.handlePreviewTick(previewTickMsg{gen: gen1}))
	// The current tick fires a load command for the pending id.
	cmd := m.handlePreviewTick(previewTickMsg{gen: m.previewGen})
	require.NotNil(t, cmd)
	msg := cmd()
	loaded, ok := msg.(previewLoadedMsg)
	require.True(t, ok)
	require.Equal(t, "s2", loaded.id)
}

// TestPreview_SkipsCommittedButSchedulesForeign verifies no preview is
// scheduled for the committed session, but a foreign-workspace session IS
// scheduled and routes through PeekMessages (not ListMessages) — foreign
// previews no longer require a full workspace switch.
func TestPreview_SkipsCommittedButSchedulesForeign(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{"foreign-id": {{ID: "fm1"}}}}
	m := newPreviewTestUI(t, ws, "committed")

	// Committed session: no pending, no tick.
	require.Nil(t, m.schedulePreview("committed", ""))
	require.Empty(t, m.pendingPreviewID)

	// Foreign workspace: scheduled, with its root recorded.
	cmd := m.schedulePreview("foreign-id", "/other")
	require.Equal(t, "foreign-id", m.pendingPreviewID)
	require.Equal(t, "/other", m.pendingPreviewRoot)
	require.NotNil(t, cmd)
	loaded, ok := drainMsg(cmd).(previewLoadedMsg)
	require.True(t, ok)
	require.Equal(t, "foreign-id", loaded.id)
	require.Len(t, loaded.msgs, 1, "foreign preview content flows through PeekMessages")
	require.Contains(t, ws.calls, "peek:/other:foreign-id", "foreign preview must use PeekMessages, not ListMessages")

	// A root matching the current workspace normalizes to the fast,
	// same-workspace path (ListMessages) instead of PeekMessages.
	ws.calls = nil
	drainMsg(m.schedulePreview("s-current", "/current"))
	require.Contains(t, ws.calls, "s-current")
}

// TestPreview_LoadedDroppedIfCursorMovedOn verifies a resolved load for an id
// that is no longer pending does not render.
func TestPreview_LoadedDroppedIfCursorMovedOn(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{}}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s2" // cursor now on s2

	// A load that resolved for s1 (cursor since moved) is dropped.
	require.Nil(t, m.handlePreviewLoaded(previewLoadedMsg{id: "s1"}))
	require.Empty(t, m.previewSessionID)
}

// TestPreview_LoadedSetsPreviewing verifies a fresh load marks previewing.
func TestPreview_LoadedSetsPreviewing(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{"s1": nil}}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s1"

	m.handlePreviewLoaded(previewLoadedMsg{id: "s1"})
	require.True(t, m.previewing())
	require.Equal(t, "s1", m.previewSessionID)
}

// TestPreview_CancelRestoresCommitted verifies esc/close discards the preview
// and reloads the committed session (picking up anything accumulated).
func TestPreview_CancelRestoresCommitted(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"committed": nil,
		"s1":        nil,
	}}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s1"
	m.handlePreviewLoaded(previewLoadedMsg{id: "s1"})
	require.True(t, m.previewing())

	cmd := m.cancelPreview()
	// previewing() is driven solely by previewSessionID, cleared here — so
	// it is false immediately (no restoring flag that could wedge).
	require.False(t, m.previewing())
	require.Empty(t, m.pendingPreviewID)
	// The restore command reloads the committed session's messages.
	msg := drainMsg(cmd)
	restore, ok := msg.(previewRestoreMsg)
	require.True(t, ok)
	require.Equal(t, "committed", restore.id)
	require.Contains(t, ws.calls, "committed")
	// Applying the restore renders the committed session.
	m.handlePreviewRestore(restore)
	require.False(t, m.previewing())
}

// TestPreview_CancelWhenNotPreviewingIsNoop verifies cancel is a no-op (no
// restore reload) when nothing is being previewed.
func TestPreview_CancelWhenNotPreviewingIsNoop(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{}}
	m := newPreviewTestUI(t, ws, "committed")
	require.Nil(t, m.cancelPreview())
}

// TestPreview_BusyCommittedWhilePreviewing verifies that while previewing
// another session, incoming message events for the busy committed session do
// NOT clobber the preview view (the message-event handler skips chat
// mutation while previewing), and cancel restores the committed session with
// its accumulated messages.
func TestPreview_BusyCommittedWhilePreviewing(t *testing.T) {
	t.Parallel()
	// committed accumulates a 2nd message while previewing s1.
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"committed": {{ID: "m1"}, {ID: "m2"}},
		"s1":        {{ID: "p1"}},
	}}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s1"
	m.handlePreviewLoaded(previewLoadedMsg{id: "s1"})
	require.True(t, m.previewing(), "preview is shown")

	// While previewing, previewing() gates the message-event handler in
	// Update from mutating the chat view — assert the guard predicate.
	require.True(t, m.previewing(), "message events must be skipped while previewing")

	// Cancel restores committed; the reload returns the accumulated msgs.
	cmd := m.cancelPreview()
	restore := drainMsg(cmd).(previewRestoreMsg)
	require.Equal(t, "committed", restore.id)
	require.Len(t, restore.msgs, 2, "committed session accumulated a 2nd message")
	m.handlePreviewRestore(restore)
	require.False(t, m.previewing(), "restore lands -> no longer previewing")
}

// TestPreview_ABAWithinDebounceEndsOnA verifies the A→B→A race: previewing A,
// move to B (schedules B load), move back to A before B's tick fires — the B
// load must be cancelled so it can't render B while the cursor is on A.
func TestPreview_ABAWithinDebounceEndsOnA(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{"A": nil, "B": nil}}
	m := newPreviewTestUI(t, ws, "committed")

	// Preview A (simulate a completed load; load clears pending).
	m.pendingPreviewID = "A"
	m.previewGen++
	m.handlePreviewLoaded(previewLoadedMsg{id: "A"})
	require.Equal(t, "A", m.previewSessionID)
	require.Empty(t, m.pendingPreviewID)

	// Move to B: schedules a B load (pending=B, gen bumped).
	m.schedulePreview("B", "")
	require.Equal(t, "B", m.pendingPreviewID)
	bGen := m.previewGen

	// Move back to A (still shown) before B's tick: must cancel B.
	m.schedulePreview("A", "")
	require.Empty(t, m.pendingPreviewID, "returning to shown session cancels pending B")
	require.Equal(t, "A", m.previewSessionID, "A stays shown")

	// B's tick (stale gen) must be dropped, and even at the current gen the
	// empty pending means no load.
	require.Nil(t, m.handlePreviewTick(previewTickMsg{gen: bGen}))
	require.Nil(t, m.handlePreviewTick(previewTickMsg{gen: m.previewGen}))
}

// TestPreview_RestoreDroppedIfPreviewRescheduled verifies #2: cancel (starts
// a committed restore), then immediately schedule + load a new preview; the
// late restore must NOT clobber the freshly-shown preview.
func TestPreview_RestoreDroppedIfPreviewRescheduled(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"committed": {{ID: "c1"}},
		"s1":        {{ID: "p1"}},
		"s2":        {{ID: "q1"}},
	}}
	m := newPreviewTestUI(t, ws, "committed")

	// Preview s1.
	m.pendingPreviewID = "s1"
	m.previewGen++
	m.handlePreviewLoaded(previewLoadedMsg{id: "s1"})
	require.True(t, m.previewing())

	// Cancel (e.g. cursor hit a header): starts a restore tagged with gen.
	restoreCmd := m.cancelPreview()
	require.NotNil(t, restoreCmd)
	staleRestore := drainMsg(restoreCmd).(previewRestoreMsg)
	require.False(t, m.previewing(), "cancel clears previewing immediately")

	// Immediately schedule + load a new preview s2 before the restore lands.
	m.schedulePreview("s2", "")
	m.handlePreviewLoaded(previewLoadedMsg{id: "s2"})
	require.Equal(t, "s2", m.previewSessionID)

	// Now the stale restore arrives last: must be dropped (gen advanced /
	// a preview is shown), NOT clobber s2.
	require.Nil(t, m.handlePreviewRestore(staleRestore))
	require.Equal(t, "s2", m.previewSessionID, "preview stays shown, restore dropped")
}

// TestPreview_LoadFailureClearsPending verifies #5: a failed load clears the
// pending id so re-navigating to the same session retries.
func TestPreview_LoadFailureClearsPending(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{}}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s1"
	m.handlePreviewLoadFailed(previewLoadFailedMsg{id: "s1", err: context.DeadlineExceeded})
	require.Empty(t, m.pendingPreviewID, "failed load clears pending so a return retries")
}

// The three tests below are the stuck-restoring regressions: each drives a
// path that (with the old `restoring` flag) could leave previewing() wedged
// true forever, silently dropping all committed-session message events.
// With Option 2 (previewing() == previewSessionID != "") none can wedge.

// TestPreview_RestoreFetchErrorDoesNotWedge: cancel starts a committed
// reload that ERRORS (returns ReportError, not a restore msg). previewing()
// must already be false from the synchronous cancel, so committed events
// render again regardless of the failed reload.
func TestPreview_RestoreFetchErrorDoesNotWedge(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{
		messages:   map[string][]message.Message{"s1": nil},
		listErrIDs: map[string]bool{"committed": true}, // restore reload fails
	}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s1"
	m.handlePreviewLoaded(previewLoadedMsg{id: "s1"})
	require.True(t, m.previewing())

	cmd := m.cancelPreview()
	require.False(t, m.previewing(), "cancel clears previewing synchronously")
	// The reload command errors; draining it must not resurrect previewing.
	drainMsg(cmd)
	require.False(t, m.previewing(), "failed restore reload cannot wedge previewing")
}

// TestPreview_DoubleCancelDoesNotWedge: two cancels in a row (reachable via
// esc then sidebar-close, etc.). The second is a no-op that must not bump the
// supersede gen or leave previewing() stuck.
func TestPreview_DoubleCancelDoesNotWedge(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"committed": nil, "s1": nil,
	}}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s1"
	m.handlePreviewLoaded(previewLoadedMsg{id: "s1"})

	first := m.cancelPreview()
	genAfterFirst := m.previewGen
	require.False(t, m.previewing())

	// Second cancel: nothing to cancel -> no-op, must NOT bump gen.
	second := m.cancelPreview()
	require.Nil(t, second, "no-op cancel returns nil")
	require.Equal(t, genAfterFirst, m.previewGen, "no-op cancel must not bump gen")
	require.False(t, m.previewing())

	// The first restore still lands cleanly and doesn't wedge anything.
	if first != nil {
		if restore, ok := drainMsg(first).(previewRestoreMsg); ok {
			m.handlePreviewRestore(restore)
		}
	}
	require.False(t, m.previewing())
}

// TestPreview_LoadFailAfterSupersedeDoesNotWedge: cancel (restore in flight),
// then schedule preview B whose load FAILS. Neither the dropped restore nor
// the failed B load may leave previewing() stuck.
func TestPreview_LoadFailAfterSupersedeDoesNotWedge(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"committed": nil, "s1": nil,
	}}
	m := newPreviewTestUI(t, ws, "committed")
	m.pendingPreviewID = "s1"
	m.handlePreviewLoaded(previewLoadedMsg{id: "s1"})

	restoreCmd := m.cancelPreview()
	staleRestore, _ := drainMsg(restoreCmd).(previewRestoreMsg)

	// Schedule B, then its load fails.
	m.schedulePreview("B", "")
	require.Equal(t, "B", m.pendingPreviewID)
	m.handlePreviewLoadFailed(previewLoadFailedMsg{id: "B", err: context.DeadlineExceeded})
	require.Empty(t, m.pendingPreviewID, "failed load clears pending")
	require.False(t, m.previewing(), "no preview shown, not wedged")

	// The stale restore arriving now is dropped (gen advanced) and cannot
	// wedge — previewing() stays false.
	m.handlePreviewRestore(staleRestore)
	require.False(t, m.previewing())
}

// fakeClock is a mutable clock for driving the burst-window logic
// deterministically in tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

// TestPreview_LeadingEdgeFirstTwoInstant verifies the leading-edge burst
// pattern: the first two rapid schedulePreview calls in a window fire loads
// IMMEDIATELY (their cmd yields a previewLoadedMsg, no tick wait), and the
// third within the same window falls back to the trailing debounce tick.
func TestPreview_LeadingEdgeFirstTwoInstant(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"s1": nil, "s2": nil, "s3": nil,
	}}
	m := newPreviewTestUI(t, ws, "committed")
	clk := &fakeClock{t: time.Unix(0, 0)}
	m.previewNow = clk.now

	// 1st load: instant.
	cmd := m.schedulePreview("s1", "")
	require.Equal(t, "s1", m.pendingPreviewID)
	_, ok := drainMsg(cmd).(previewLoadedMsg)
	require.True(t, ok, "1st load in window fires immediately")

	// 2nd load, still inside the window: instant.
	clk.advance(10 * time.Millisecond)
	cmd = m.schedulePreview("s2", "")
	_, ok = drainMsg(cmd).(previewLoadedMsg)
	require.True(t, ok, "2nd load in window fires immediately")

	// 3rd load, still inside the window: trailing debounce tick.
	clk.advance(10 * time.Millisecond)
	cmd = m.schedulePreview("s3", "")
	require.NotNil(t, cmd)
	_, ok = drainMsg(cmd).(previewTickMsg)
	require.True(t, ok, "3rd load in window is tick-gated")
	require.Equal(t, 3, m.previewBurstCount)
}

// TestPreview_BurstCounterResetsAfterWindow verifies that an idle gap longer
// than the burst window resets the counter, so a later single navigation is
// instant again.
func TestPreview_BurstCounterResetsAfterWindow(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"s1": nil, "s2": nil, "s3": nil,
	}}
	m := newPreviewTestUI(t, ws, "committed")
	clk := &fakeClock{t: time.Unix(0, 0)}
	m.previewNow = clk.now

	// Exhaust the leading edge: two instant loads.
	drainMsg(m.schedulePreview("s1", ""))
	clk.advance(10 * time.Millisecond)
	drainMsg(m.schedulePreview("s2", ""))
	require.Equal(t, 2, m.previewBurstCount)

	// Idle longer than the window, then navigate once more: instant again.
	clk.advance(previewBurstWindow + time.Millisecond)
	cmd := m.schedulePreview("s3", "")
	_, ok := drainMsg(cmd).(previewLoadedMsg)
	require.True(t, ok, "load after idle gap fires immediately")
	require.Equal(t, 1, m.previewBurstCount, "burst counter reset after window")
}

// TestPreview_InstantLoadSupersededDoesNotRender is the critical correctness
// check: an instant (leading-edge) load must NOT render a stale session if
// the cursor moved on before it resolved. The instant path funnels through
// the same pending-id guard as the tick path, so a superseded instant load is
// dropped.
func TestPreview_InstantLoadSupersededDoesNotRender(t *testing.T) {
	t.Parallel()
	ws := &previewStubWorkspace{messages: map[string][]message.Message{
		"s1": {{ID: "p1"}}, "s2": {{ID: "q1"}},
	}}
	m := newPreviewTestUI(t, ws, "committed")
	clk := &fakeClock{t: time.Unix(0, 0)}
	m.previewNow = clk.now

	// First navigation fires an instant load for s1 (pending=s1). Capture
	// the cmd but do NOT resolve it yet — the cursor is about to move on.
	s1Cmd := m.schedulePreview("s1", "")
	require.Equal(t, "s1", m.pendingPreviewID)
	s1Msg := drainMsg(s1Cmd).(previewLoadedMsg)

	// Cursor moves on to s2 before s1's load renders (also instant). This
	// bumps previewGen and sets pending=s2.
	clk.advance(10 * time.Millisecond)
	m.schedulePreview("s2", "")
	require.Equal(t, "s2", m.pendingPreviewID)

	// Now s1's earlier instant load resolves last: it must be DROPPED, not
	// rendered, because pending is no longer s1.
	require.Nil(t, m.handlePreviewLoaded(s1Msg))
	require.Empty(t, m.previewSessionID, "superseded instant load must not render")
	require.Equal(t, "s2", m.pendingPreviewID, "pending stays on the current session")
}
