package model

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// handleCancelTimerExpired disarms the cancel-confirmation state when the
// timer that fired belongs to the current arming cycle. Timers from an
// earlier arm (a lower generation) are ignored so a stale timer cannot
// disarm a newer cycle.
func (m *UI) handleCancelTimerExpired(msg cancelTimerExpiredMsg) {
	if msg.gen == m.cancelGen {
		m.isCanceling = false
	}
}

// cancelTimerCmd creates a command that expires the cancel timer for the
// given generation.
func cancelTimerCmd(gen int) tea.Cmd {
	return tea.Tick(cancelTimerDuration, func(time.Time) tea.Msg {
		return cancelTimerExpiredMsg{gen: gen}
	})
}

// cancelAgent handles the cancel key press. The first press sets isCanceling to true
// and starts a timer. The second press (before the timer expires) actually
// cancels the agent.
func (m *UI) cancelAgent() tea.Cmd {
	if !m.com.Workspace.AgentIsReady() {
		return nil
	}

	if m.isCanceling {
		// Second escape press - actually cancel the agent. Cancel the
		// focused session when it is the one running; otherwise (no
		// focused session, or the busy run belongs to another session,
		// e.g. after detach/reattach) fall back to a workspace-wide
		// cancel so the in-flight run is always stopped.
		m.isCanceling = false
		m.cancelGen++
		if m.hasSession() && m.com.Workspace.AgentIsSessionBusy(m.session.ID) {
			m.com.Workspace.AgentCancel(m.session.ID)
		} else {
			m.com.Workspace.AgentCancelAll()
		}
		// Stop the spinning todo indicator.
		m.todoIsSpinning = false
		m.renderPills()
		return nil
	}

	// Clear queued prompts only when the queue pills are expanded. When
	// collapsed, Esc must not discard the queue; it falls through to the
	// cancel flow instead.
	if m.hasSession() && m.pillsExpanded && m.com.Workspace.AgentQueuedPrompts(m.session.ID) > 0 {
		m.com.Workspace.AgentClearQueue(m.session.ID)
		return nil
	}

	// First escape press - set canceling state and start timer.
	m.isCanceling = true
	m.cancelGen++
	return cancelTimerCmd(m.cancelGen)
}
