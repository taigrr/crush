package model

import (
	"image"
	"time"

	tea "charm.land/bubbletea/v2"
)

// scrollCoalesceInterval is the window during which wheel events are
// accumulated before being applied as a single scroll. One display frame
// keeps scrolling feeling immediate while capping the number of redraws a
// trackpad flick can trigger.
const scrollCoalesceInterval = 16 * time.Millisecond

// chatScrollFlushMsg applies the wheel deltas accumulated since the last
// flush. gen identifies the coalesce window the tick was armed for; a
// mismatch means the window was reset and the tick must be ignored.
type chatScrollFlushMsg struct{ gen int }

// chatWheelCoalesced reports whether a vertical wheel event at the given
// screen position would be handled by the chat coalescer rather than a
// dialog, the right sidebar, or a non-chat screen.
func (m *UI) chatWheelCoalesced(msg tea.MouseWheelMsg) bool {
	if msg.Button != tea.MouseWheelUp && msg.Button != tea.MouseWheelDown {
		return false
	}
	if m.state != uiChat || m.dialog.HasDialogs() {
		return false
	}
	if m.rightSidebarScrollable && !m.isCompact && !m.chatFullscreen &&
		image.Pt(msg.X, msg.Y).In(m.layout.sidebar) {
		return false
	}
	return true
}

func wheelLines(msg tea.MouseWheelMsg) int {
	if msg.Button == tea.MouseWheelUp {
		return -MouseScrollThreshold
	}
	return MouseScrollThreshold
}

// absorbChatWheel folds a wheel delta into the open coalesce window and
// reports whether it did so. It is called from the program's message
// filter, which runs on the event-loop goroutine immediately before
// Update, so an absorbed event costs neither an Update nor a View.
//
// The event is not absorbed when no window is open (the leading edge of a
// burst) or when it reverses the direction of the pending backlog. In the
// latter case the backlog and the reversal must be applied as separate,
// individually clamped scrolls: netting them would let overscroll notches
// banked against the top or bottom swallow the reversal.
func (m *UI) absorbChatWheel(lines int) bool {
	if !m.scrollFlushPending {
		return false
	}
	if m.pendingScroll != 0 && (m.pendingScroll > 0) != (lines > 0) {
		return false
	}
	m.pendingScroll += lines
	return true
}

// handleChatWheel handles a wheel event that the filter did not absorb:
// the leading edge of a burst, or a direction reversal. Any pending
// backlog is applied first, then the new delta, so scrolling responds
// without perceptible delay, and a coalesce window is (re)opened during
// which further events are summed by absorbChatWheel.
func (m *UI) handleChatWheel(lines int) tea.Cmd {
	m.markScrollOnly()
	if m.absorbChatWheel(lines) {
		return nil
	}
	var cmds []tea.Cmd
	if m.pendingScroll != 0 {
		cmds = append(cmds, m.applyChatScroll(m.pendingScroll))
		m.pendingScroll = 0
	}
	cmds = append(cmds, m.applyChatScroll(lines))
	if !m.scrollFlushPending {
		cmds = append(cmds, m.openScrollWindow())
	}
	return tea.Batch(cmds...)
}

// handleChatScrollFlush applies any pending wheel delta and keeps the
// coalesce window open while scrolling continues. A flush that applies
// nothing (trailing edge or stale generation) changes nothing visible and
// is scroll-only, so the frame cache survives the end of a burst.
func (m *UI) handleChatScrollFlush(msg chatScrollFlushMsg) tea.Cmd {
	m.markScrollOnly()
	if msg.gen != m.scrollFlushGen {
		return nil
	}
	m.scrollFlushPending = false
	lines := m.pendingScroll
	m.pendingScroll = 0
	if lines == 0 {
		return nil
	}
	if m.state != uiChat || m.dialog.HasDialogs() {
		m.resetChatScroll()
		return nil
	}
	return tea.Batch(m.applyChatScroll(lines), m.openScrollWindow())
}

// openScrollWindow starts a coalesce window and returns the tick that
// closes it. Exactly one tick is outstanding per window: the flag is only
// set here, and only cleared by that tick's flush or by resetChatScroll,
// which also invalidates the tick via the generation counter.
func (m *UI) openScrollWindow() tea.Cmd {
	m.scrollFlushPending = true
	gen := m.scrollFlushGen
	return tea.Tick(scrollCoalesceInterval, func(time.Time) tea.Msg {
		return chatScrollFlushMsg{gen: gen}
	})
}

// resetChatScroll discards any pending wheel delta and invalidates the
// outstanding flush tick. Called when the chat content or screen changes
// so a backlog from before the change is not applied to what replaced it.
func (m *UI) resetChatScroll() {
	m.pendingScroll = 0
	m.scrollFlushPending = false
	m.scrollFlushGen++
}

// applyChatScroll scrolls the chat by lines and, if the selection is then
// outside the viewport, moves it to the nearest visible edge. The
// selection is moved rather than scrolled to so a large coalesced delta is
// applied in full instead of being rewound to the selected item.
func (m *UI) applyChatScroll(lines int) tea.Cmd {
	cmd := m.chat.ScrollByAndAnimate(lines)
	if m.chat.SelectedItemInView() {
		return cmd
	}
	if lines > 0 && m.chat.AtBottom() {
		m.chat.SelectLast()
		return cmd
	}
	m.chat.SelectNearestInView(lines < 0)
	return cmd
}
