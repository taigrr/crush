package model

import (
	"context"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/util"
)

// previewDebounce is the trailing-debounce window used once a burst is in
// progress: the highlighted session must stay put this long before the load
// fires. Rapid j/k movement supersedes the pending load so only the settled
// session is fetched.
const previewDebounce = 150 * time.Millisecond

// previewBurstWindow bounds a rolling "burst" of preview navigation. The
// first previewBurstInstant loads within the window fire immediately (leading
// edge); once that many have fired inside the window, further loads fall back
// to the trailing previewDebounce. An idle gap longer than this window resets
// the burst so a later single move is instant again. It is set slightly wider
// than previewDebounce so a settle-then-move cadence still counts as one
// burst.
const previewBurstWindow = 250 * time.Millisecond

// previewBurstInstant is how many loads at the leading edge of a burst fire
// immediately before the trailing debounce takes over.
const previewBurstInstant = 2

// previewTickMsg fires after the debounce window; gen lets us drop stale
// ticks when the cursor moved again before the timer elapsed.
type previewTickMsg struct{ gen int }

// previewLoadedMsg carries a fetched preview session's messages. id is the
// session the load was for, so a load that resolved after the cursor moved
// on can be dropped. sess and files carry the session's metadata and
// modified-file stats so the right info-sidebar reflects the previewed
// session; both are nil when the sidebar-data fetch failed (best-effort),
// in which case the sidebar keeps showing the committed session's stats.
type previewLoadedMsg struct {
	id    string
	msgs  []message.Message
	sess  *session.Session
	files []SessionFile
}

// schedulePreview debounces a live-preview load for the highlighted session.
// root is the session's workspace root; pass "" (or the current workspace's
// own root) for a same-workspace session. It is a no-op (and cancels any
// pending/active preview back to the committed view) when id is empty or is
// already the committed session — nothing to preview either way.
//
// Foreign-workspace sessions preview too (via [workspace.Workspace.PeekMessages],
// which reads the target workspace — attached or registry-detached — without
// switching this client's own workspace), not just current-workspace ones:
// previewing used to be restricted to the current workspace only because the
// only way to read a foreign workspace's messages was a full
// [workspace.Workspace.SwitchWorkspace], far too heavy to pay on every
// debounced cursor move.
//
// Otherwise it records the pending id/root and bumps the supersede
// generation. Load timing follows a leading-edge burst pattern (see
// previewBurstWindow): the first previewBurstInstant loads in a burst fire
// IMMEDIATELY via previewLoadCmd; the third and later within the window
// return a trailing tick command and the fetch happens on previewTickMsg.
// Both paths funnel through the same pending-id + previewGen supersede
// guards, so an instant load that is superseded before it resolves is
// dropped just like a ticked one (see handlePreviewLoaded / handlePreviewTick).
func (m *UI) schedulePreview(id, root string) tea.Cmd {
	committed := ""
	if m.session != nil {
		committed = m.session.ID
	}
	// Normalize root: a same-workspace hit (empty root from search, or a
	// root matching the currently-attached workspace) always previews
	// through the fast current-workspace path, regardless of how the
	// caller phrased it.
	if root != "" && m.isCurrentWorkspace(root) {
		root = ""
	}
	if id == "" || id == committed {
		// Nothing to preview here: return to the committed view if we were
		// previewing something else.
		return m.cancelPreview()
	}
	if id == m.pendingPreviewID && root == m.pendingPreviewRoot {
		return nil // already waiting to load this exact id: pure no-op
	}
	if id == m.previewSessionID {
		// Returning to the session already shown (e.g. A→B→A within the
		// debounce window). Cancel any in-flight load for the intermediate
		// session so its tick/result can't render over the shown one, and
		// keep showing the current preview without a reload.
		m.pendingPreviewID = ""
		m.pendingPreviewRoot = ""
		m.previewGen++
		return nil
	}
	m.pendingPreviewID = id
	m.pendingPreviewRoot = root
	m.previewGen++
	if m.registerPreviewBurst() {
		// Leading edge of the burst: fire the load now. The load still
		// carries m.pendingPreviewID, and handlePreviewLoaded drops it at
		// render time if the cursor has moved on (pending id changed) — so
		// the instant path keeps the same supersede protection as the tick
		// path without waiting for the debounce.
		return m.previewLoadCmd(id, root)
	}
	gen := m.previewGen
	return tea.Tick(previewDebounce, func(time.Time) tea.Msg {
		return previewTickMsg{gen: gen}
	})
}

// registerPreviewBurst records a preview load against the rolling burst
// window and reports whether it should fire immediately (leading edge). The
// first previewBurstInstant loads within previewBurstWindow return true; once
// that many have accumulated in the window the caller uses the trailing
// debounce. An idle gap longer than the window resets the counter so a later
// single navigation is instant again.
func (m *UI) registerPreviewBurst() bool {
	now := time.Now
	if m.previewNow != nil {
		now = m.previewNow
	}
	t := now()
	if m.previewBurstCount == 0 || t.Sub(m.previewLastNav) > previewBurstWindow {
		m.previewBurstCount = 0
	}
	m.previewLastNav = t
	m.previewBurstCount++
	return m.previewBurstCount <= previewBurstInstant
}

// handlePreviewTick fires the actual load if the tick is still current.
func (m *UI) handlePreviewTick(msg previewTickMsg) tea.Cmd {
	if msg.gen != m.previewGen || m.pendingPreviewID == "" {
		return nil // superseded by a newer move, or cancelled
	}
	return m.previewLoadCmd(m.pendingPreviewID, m.pendingPreviewRoot)
}

// previewLoadCmd fetches a session's messages off the Update goroutine for an
// ephemeral preview. It does NOT touch presence/LSP/CWD/history — only the
// messages are read. root == "" reads the current workspace's live service
// ([workspace.Workspace.ListMessages]); a non-empty root reads any other
// known workspace, attached or not, via [workspace.Workspace.PeekMessages]
// without switching this client's own workspace. On error it emits
// previewLoadFailedMsg so the pending id is cleared and a later return to
// that session retries.
func (m *UI) previewLoadCmd(id, root string) tea.Cmd {
	return func() tea.Msg {
		var msgs []message.Message
		var err error
		if root == "" {
			msgs, err = m.com.Workspace.ListMessages(context.Background(), id)
		} else {
			msgs, err = m.com.Workspace.PeekMessages(context.Background(), root, id)
		}
		if err != nil {
			return previewLoadFailedMsg{id: id, err: err}
		}
		sess, files := m.loadPreviewSidebarData(id, root)
		return previewLoadedMsg{id: id, msgs: msgs, sess: sess, files: files}
	}
}

// loadPreviewSidebarData fetches the previewed session's metadata and
// modified-file stats so the right info-sidebar can reflect the highlighted
// session. It is best-effort: a failure returns (nil, nil) and is logged,
// leaving the sidebar on the committed session's stats rather than failing
// the whole preview (the chat messages already loaded). root == "" reads
// the current workspace directly; a non-empty root peeks any other known
// workspace without switching this client's workspace.
func (m *UI) loadPreviewSidebarData(id, root string) (*session.Session, []SessionFile) {
	if root == "" {
		sess, err := m.com.Workspace.GetSession(context.Background(), id)
		if err != nil {
			slog.Debug("Preview: failed to load session metadata", "session_id", id, "error", err)
			return nil, nil
		}
		files, err := m.loadSessionFiles(id)
		if err != nil {
			slog.Debug("Preview: failed to load session files", "session_id", id, "error", err)
			return &sess, nil
		}
		return &sess, files
	}
	sess, hist, err := m.com.Workspace.PeekSessionInfo(context.Background(), root, id)
	if err != nil {
		slog.Debug("Preview: failed to peek session info", "session_id", id, "root", root, "error", err)
		return nil, nil
	}
	return &sess, computeSessionFiles(hist)
}

// previewLoadFailedMsg reports a failed preview fetch.
type previewLoadFailedMsg struct {
	id  string
	err error
}

// handlePreviewLoadFailed clears the pending id (if still current) so a
// return to that session retries, and surfaces the error.
func (m *UI) handlePreviewLoadFailed(msg previewLoadFailedMsg) tea.Cmd {
	if msg.id == m.pendingPreviewID {
		m.pendingPreviewID = ""
		m.pendingPreviewRoot = ""
	}
	return util.ReportError(msg.err)
}

// handlePreviewLoaded renders a fetched preview into the chat view, unless
// the cursor moved on to a different session while the load was in flight.
func (m *UI) handlePreviewLoaded(msg previewLoadedMsg) tea.Cmd {
	if msg.id != m.pendingPreviewID {
		return nil // stale: cursor moved after the fetch started
	}
	m.previewSessionID = msg.id
	m.pendingPreviewID = "" // load complete; no longer in-flight
	m.pendingPreviewRoot = ""
	// Swap the sidebar over to the previewed session's stats. Both are
	// nil when the best-effort sidebar-data fetch failed; the sidebar
	// then falls back to the committed session (see sidebarSession).
	m.previewSess = msg.sess
	m.previewFiles = msg.files
	// Render the preview messages read-only into the chat view. This reuses
	// the normal renderer but does NOT change m.session, so the committed
	// session stays the routing target for live events.
	return m.setSessionMessages(msg.msgs)
}

// cancelPreview discards any pending/active preview and, if a preview was
// showing, restores the committed session's view (reloading its messages so
// anything that accumulated while previewing — e.g. a busy session still
// generating — is picked up). If there is no committed session it clears the
// chat. Returns a cmd (possibly nil) to run.
//
// Design note (see review round 2): previewing() is driven SOLELY by
// previewSessionID, which is cleared here synchronously. We deliberately do
// NOT hold a "restoring" flag across the async committed-message reload —
// that flag had multiple leak paths (restore-fetch error, no-op/double
// cancel, load-fail-after-supersede) that could wedge previewing() true
// forever and silently drop all committed-session events. The tiny window
// between clearing previewSessionID here and the restore render landing is
// accepted (original LOW #6, deemed acceptable): committed events in that
// window apply to the chat and converge with the restore reload.
func (m *UI) cancelPreview() tea.Cmd {
	m.pendingPreviewID = ""
	m.pendingPreviewRoot = ""
	if m.previewSessionID == "" {
		// Nothing being previewed: do NOT bump the supersede generation
		// (a no-op cancel must not orphan an unrelated in-flight restore).
		return nil
	}
	m.previewSessionID = ""
	m.previewSess = nil
	m.previewFiles = nil
	m.previewGen++ // invalidate any in-flight tick/load for the preview
	if m.session == nil {
		m.chat.ClearMessages()
		return nil
	}
	return m.reloadCommittedMessages(m.previewGen)
}

// reloadCommittedMessages re-fetches and re-renders the committed session's
// messages into the chat view (used to restore after a preview is
// cancelled). gen tags the restore so a stale restore (superseded by a new
// preview scheduled after the cancel) is dropped. On error it emits a
// previewRestoreMsg with nil msgs so the handler is still reached (no state
// to unwind, but keeps behavior uniform).
func (m *UI) reloadCommittedMessages(gen int) tea.Cmd {
	if m.session == nil {
		return nil
	}
	id := m.session.ID
	return func() tea.Msg {
		msgs, err := m.com.Workspace.ListMessages(context.Background(), id)
		if err != nil {
			return util.ReportError(err)()
		}
		return previewRestoreMsg{id: id, msgs: msgs, gen: gen}
	}
}

// previewRestoreMsg carries the committed session's messages to re-render
// after a preview is cancelled.
type previewRestoreMsg struct {
	id   string
	msgs []message.Message
	gen  int
}

// handlePreviewRestore re-renders the committed session, bailing when the
// restore is stale: the supersede generation advanced, a new preview is
// shown/pending since the cancel, or the committed session changed. There is
// no flag to reset — previewing() no longer depends on restore state — so a
// dropped restore can never wedge the view.
func (m *UI) handlePreviewRestore(msg previewRestoreMsg) tea.Cmd {
	if msg.gen != m.previewGen || m.previewSessionID != "" || m.pendingPreviewID != "" {
		return nil
	}
	if m.session == nil || m.session.ID != msg.id {
		return nil
	}
	return m.setSessionMessages(msg.msgs)
}

// previewing reports whether an ephemeral preview is currently shown. It is
// driven solely by previewSessionID (set on load, cleared on cancel/commit),
// so it can never be wedged true by a dropped/failed async restore.
func (m *UI) previewing() bool { return m.previewSessionID != "" }

// sidebarSession returns the session whose stats the right info-sidebar
// should render: the previewed session while a preview is shown and its
// metadata loaded, otherwise the committed session. Gating on
// previewSess != nil keeps the sidebar on the committed session when the
// best-effort sidebar-data fetch failed rather than blanking it.
func (m *UI) sidebarSession() *session.Session {
	if m.previewing() && m.previewSess != nil {
		return m.previewSess
	}
	return m.session
}

// sidebarFiles returns the modified-file stats the right info-sidebar
// should render, mirroring sidebarSession: the previewed session's files
// while previewing (and its data loaded), otherwise the committed
// session's files.
func (m *UI) sidebarFiles() []SessionFile {
	if m.previewing() && m.previewSess != nil {
		return m.previewFiles
	}
	return m.sessionFiles
}
