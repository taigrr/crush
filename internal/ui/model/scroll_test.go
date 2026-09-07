package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func wheelMsg(u *UI, button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{Button: button, X: u.layout.main.Min.X + 1, Y: u.layout.main.Min.Y + 1}
}

func flushMsg(u *UI) chatScrollFlushMsg {
	return chatScrollFlushMsg{gen: u.scrollFlushGen}
}

func scrollPos(u *UI) [2]int {
	idx, line := u.chat.ScrollPosition()
	return [2]int{idx, line}
}

// wheel delivers a wheel event the way the program does: through the
// filter first, then Update if the filter let it through. It reports
// whether Update ran and the command it returned.
func wheel(u *UI, msg tea.MouseWheelMsg) (delivered bool, cmd tea.Cmd) {
	filtered := MouseEventFilter(u, msg)
	if filtered == nil {
		return false, nil
	}
	_, cmd = u.Update(filtered)
	return true, cmd
}

func TestWheel_FirstEventAppliesImmediately(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	before := u.chat.Offset()

	delivered, cmd := wheel(u, wheelMsg(u, tea.MouseWheelUp))
	require.True(t, delivered, "leading edge must reach Update")
	require.NotNil(t, cmd, "leading edge must schedule a flush")
	require.Equal(t, before-MouseScrollThreshold, u.chat.Offset(), "first wheel event must scroll immediately by the full delta")
	require.True(t, u.scrollFlushPending)
	require.Equal(t, 0, u.pendingScroll)
}

func TestWheel_BurstIsAbsorbedByFilterAndAppliedOnce(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	u.chat.SetSelected(u.chat.Len() - 1)

	up := wheelMsg(u, tea.MouseWheelUp)
	_, _ = wheel(u, up)
	afterFirst := scrollPos(u)
	offsetAfterFirst := u.chat.Offset()

	for range 4 {
		delivered, _ := wheel(u, up)
		require.False(t, delivered, "events inside the window must be absorbed by the filter, not delivered to Update")
	}
	require.Equal(t, afterFirst, scrollPos(u), "absorbed events must not move the view")
	require.Equal(t, -4*MouseScrollThreshold, u.pendingScroll)

	_, cmd := u.Update(flushMsg(u))
	require.NotNil(t, cmd, "flush with pending delta must re-arm the window")
	require.Equal(t, offsetAfterFirst-4*MouseScrollThreshold, u.chat.Offset(), "flush must apply the summed delta in full")
	require.True(t, u.chat.SelectedItemInView(), "selection must be pulled into view")
	require.Equal(t, 0, u.pendingScroll)
	require.True(t, u.scrollFlushPending)

	_, cmd = u.Update(flushMsg(u))
	require.Nil(t, cmd, "flush with nothing pending must close the window")
	require.False(t, u.scrollFlushPending)
}

func TestWheel_ReversalAppliesBacklogAndNewDirectionSeparately(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	bottom := u.chat.Offset()

	// Overscroll notches at the bottom bank up harmlessly in the window.
	down := wheelMsg(u, tea.MouseWheelDown)
	up := wheelMsg(u, tea.MouseWheelUp)
	_, _ = wheel(u, down)
	for range 4 {
		delivered, _ := wheel(u, down)
		require.False(t, delivered)
	}
	require.Equal(t, 4*MouseScrollThreshold, u.pendingScroll)
	require.Equal(t, bottom, u.chat.Offset())

	// A reversal must not be swallowed by the banked overscroll: it is
	// delivered, the backlog is clamped away, and the up notch moves.
	delivered, _ := wheel(u, up)
	require.True(t, delivered, "reversal must reach Update")
	require.Equal(t, bottom-MouseScrollThreshold, u.chat.Offset(), "reversal must scroll by its full delta")
	require.Zero(t, u.pendingScroll)
	require.True(t, u.scrollFlushPending, "window stays open across a reversal")

	// Further ups are absorbed into the same window.
	delivered, _ = wheel(u, up)
	require.False(t, delivered)
	require.Equal(t, -MouseScrollThreshold, u.pendingScroll)
}

func TestWheel_ReversalMidBacklogDoesNotLoseDistance(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	start := u.chat.Offset()

	up := wheelMsg(u, tea.MouseWheelUp)
	down := wheelMsg(u, tea.MouseWheelDown)
	_, _ = wheel(u, up)
	for range 3 {
		_, _ = wheel(u, up)
	}
	_, _ = wheel(u, down)
	// 4 ups applied (1 leading + 3 backlog on reversal), then 1 down.
	require.Equal(t, start-4*MouseScrollThreshold+MouseScrollThreshold, u.chat.Offset())
	require.Zero(t, u.pendingScroll)
}

func TestWheel_TrailingEmptyFlushKeepsFrameCache(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	// Select an item that stays in view both at the bottom and one notch
	// up so the round trip lands on an identical frame key.
	u.chat.SetSelected(u.chat.Len() - 10)
	bottom := u.View()

	up := wheelMsg(u, tea.MouseWheelUp)
	down := wheelMsg(u, tea.MouseWheelDown)
	_, _ = wheel(u, up)
	moved := u.View()
	require.NotEqual(t, bottom.Content, moved.Content)
	_, _ = u.Update(flushMsg(u))
	u.View()
	require.Equal(t, 1, u.frames.hits, "empty trailing flush must be a cache hit")
	require.Equal(t, 2, u.frames.Len(), "empty trailing flush must not reset the cache")
	require.False(t, u.scrollFlushPending)

	// Scroll back down: the bottom frame rendered earlier must still be there.
	_, _ = wheel(u, down)
	back := u.View()
	require.Equal(t, 2, u.frames.hits)
	require.Equal(t, bottom.Content, back.Content)
	require.True(t, u.chat.AtBottom())
}

func TestWheel_StaleFlushGenerationIsIgnoredAndScrollOnly(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	stale := flushMsg(u)
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	pos := scrollPos(u)
	u.View()
	frames := u.frames.Len()

	u.resetChatScroll()
	require.Zero(t, u.pendingScroll)
	require.False(t, u.scrollFlushPending)

	_, cmd := u.Update(stale)
	require.Nil(t, cmd)
	require.Equal(t, pos, scrollPos(u), "stale flush must not scroll")
	require.False(t, u.scrollFlushPending, "stale flush must not reopen the window")
	u.View()
	require.Equal(t, frames, u.frames.Len(), "stale flush must not reset the frame cache")
}

func TestWheel_FocusChangeKeepsPendingDelta(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	msg := flushMsg(u)
	pending := u.pendingScroll
	require.NotZero(t, pending)

	u.setState(uiChat, uiFocusMain)
	require.Equal(t, pending, u.pendingScroll, "a focus change must not discard the backlog")
	before := u.chat.Offset()
	_, _ = u.Update(msg)
	require.Equal(t, before+pending, u.chat.Offset())
}

func TestWheel_StateChangeDropsPendingDelta(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	stale := flushMsg(u)
	require.NotZero(t, u.pendingScroll)

	u.setState(uiLanding, uiFocusEditor)
	require.Zero(t, u.pendingScroll)
	_, _ = u.Update(stale)
	require.False(t, u.scrollFlushPending)
}

func TestWheel_FlushWithDialogOpenIsDropped(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	pos := scrollPos(u)

	u.dialog.OpenDialog(stubDialog{})
	_, cmd := u.Update(flushMsg(u))
	require.Nil(t, cmd)
	require.Equal(t, pos, scrollPos(u), "backlog must not scroll the chat under a dialog")
	require.Zero(t, u.pendingScroll)
	require.False(t, u.scrollFlushPending)
}

func TestWheel_FlushOutsideChatIsDropped(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	_, _ = wheel(u, wheelMsg(u, tea.MouseWheelUp))
	msg := flushMsg(u)
	require.NotZero(t, u.pendingScroll)

	u.state = uiLanding
	_, cmd := u.Update(msg)
	require.Nil(t, cmd)
	require.Zero(t, u.pendingScroll)
	require.False(t, u.scrollFlushPending)
}

func TestWheel_UpdateAbsorbsWhenFilterBypassed(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	u.chat.SetSelected(u.chat.Len() - 1)
	u.View()

	// Events delivered straight to Update (no filter) must still be
	// coalesced and remain scroll-only.
	up := wheelMsg(u, tea.MouseWheelUp)
	_, _ = u.Update(up)
	moved := u.View()
	for range 5 {
		_, cmd := u.Update(up)
		require.Nil(t, cmd)
		v := u.View()
		require.Equal(t, moved.Content, v.Content)
	}
	require.Equal(t, 5, u.frames.hits, "coalesced wheel events must be served from the frame cache")
	require.Equal(t, -5*MouseScrollThreshold, u.pendingScroll)
}

func TestApplyChatScroll_LargeDeltaIsNotRewoundBySelection(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()
	u.chat.SelectLast()
	before := u.chat.Offset()

	u.applyChatScroll(-60)
	require.Equal(t, before-60, u.chat.Offset(), "selection follow must not rewind the viewport")
	require.True(t, u.chat.SelectedItemInView())

	u.applyChatScroll(60)
	require.True(t, u.chat.AtBottom())
	require.Equal(t, u.chat.Len()-1, u.chat.Selected(), "reaching the bottom must select the last item")
}

func TestApplyChatScroll_OutOfViewSelectionGoesToNearestEdge(t *testing.T) {
	t.Parallel()
	u := newFrameTestUI(t)
	u.chat.ScrollToBottom()

	// Selection far above the viewport, user scrolls up: it should land on
	// the top row (nearest edge), not jump to the bottom row.
	u.chat.SetSelected(0)
	u.applyChatScroll(-5)
	got := u.chat.Selected()
	u.chat.SelectFirstInView()
	require.Equal(t, u.chat.Selected(), got, "selection above viewport must snap to the top edge")

	// Selection below the viewport, user scrolls down: bottom row.
	u.chat.ScrollToTop()
	u.chat.SetSelected(u.chat.Len() - 1)
	u.applyChatScroll(5)
	got = u.chat.Selected()
	u.chat.SelectLastInView()
	require.Equal(t, u.chat.Selected(), got, "selection below viewport must snap to the bottom edge")
}

// Not parallel: exercises the package-level throttle clock.
func TestMouseEventFilter_RoutesWheelEvents(t *testing.T) {
	u := newFrameTestUI(t)
	up := wheelMsg(u, tea.MouseWheelUp)
	left := up
	left.Button = tea.MouseWheelLeft
	farFuture := time.Now().Add(time.Hour)

	lastMouseEvent = farFuture
	require.NotNil(t, MouseEventFilter(u, up), "chat wheel must not be throttled")
	require.Nil(t, MouseEventFilter(u, left), "horizontal wheel stays throttled")

	u.dialog.OpenDialog(stubDialog{})
	lastMouseEvent = farFuture
	require.Nil(t, MouseEventFilter(u, up), "wheel bound for a dialog stays throttled")
	u.dialog.CloseFrontDialog()

	lastMouseEvent = time.Time{}
	require.NotNil(t, MouseEventFilter(u, left), "throttle must pass events once the interval elapsed")
}
