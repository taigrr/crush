package model

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

var lastMouseEvent time.Time

// mouseThrottleInterval is the minimum spacing between mouse events that
// are not otherwise coalesced. Trackpads emit far more events than the UI
// can usefully redraw.
const mouseThrottleInterval = 15 * time.Millisecond

// MouseEventFilter runs on the event-loop goroutine immediately before
// Update and thins the mouse event stream.
//
// Vertical wheel events bound for the chat view are coalesced: while a
// scroll window is open (see scroll.go) the delta is folded into the
// model's pending scroll and the event is dropped, so it costs neither an
// Update nor a View and no scroll distance is lost. Wheel events for
// dialogs, the right sidebar, horizontal wheels and non-chat screens, as
// well as mouse motion, are rate-limited instead.
func MouseEventFilter(m tea.Model, msg tea.Msg) tea.Msg {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		ui, ok := m.(*UI)
		if !ok || !ui.chatWheelCoalesced(msg) {
			return throttleMouse(msg)
		}
		if ui.absorbChatWheel(wheelLines(msg)) {
			return nil
		}
		return msg
	case tea.MouseMotionMsg:
		return throttleMouse(msg)
	}
	return msg
}

func throttleMouse(msg tea.Msg) tea.Msg {
	now := time.Now()
	if now.Sub(lastMouseEvent) < mouseThrottleInterval {
		return nil
	}
	lastMouseEvent = now
	return msg
}
