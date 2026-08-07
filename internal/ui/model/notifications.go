package model

import (
	"fmt"
	"log/slog"
	"runtime"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/ui/chat"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/notification"
)

func (m *UI) sendNotification(n notification.Notification) tea.Cmd {
	if !m.shouldSendNotification() {
		return nil
	}

	return m.notifyBackend.Send(n)
}

// selectNotificationBackend chooses the appropriate notification backend based
// on terminal capabilities, environment, and user configuration. This is a pure
// function that should be called once during initialization or when capabilities
// change.
func selectNotificationBackend(caps common.Capabilities, cfg *config.Config) notification.Backend {
	// Check for explicit user preference first.
	if cfg != nil && cfg.Options != nil && cfg.Options.NotificationStyle != "" {
		switch cfg.Options.NotificationStyle {
		case "native":
			slog.Debug("Using native backend (user preference)")
			return notification.NewNativeBackend(notification.Icon)
		case "osc":
			slog.Debug("Using OSC backend (user preference)", "osc99_supported", caps.OSC99Notifications)
			return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
		case "bell":
			slog.Debug("Using bell backend (user preference)")
			return notification.NewBellBackend()
		case "disabled":
			slog.Debug("Notifications disabled (user preference)")
			return notification.NoopBackend{}
		case "auto":
			// Fall through to auto-detection below.
		default:
			slog.Warn("Unknown notification style, using auto", "style", cfg.Options.NotificationStyle)
		}
	}

	// Auto-detect based on environment and capabilities.
	_, isSSH := caps.Env.LookupEnv("SSH_TTY")

	// SSH sessions use terminal-based notifications (OSC 99 or 777).
	if isSSH {
		slog.Debug("Selected OSCBackend for SSH session", "osc99_supported", caps.OSC99Notifications)
		return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
	}

	// Local sessions: prefer OSC on macOS because the native backend (beeep)
	// uses terminal-notifier or AppleScript, which is slow and doesn't display
	// icons properly. OSC 99 provides a more polished experience with icon support.
	if runtime.GOOS == "darwin" {
		slog.Debug("Selected OSCBackend for local macOS session", "osc99_supported", caps.OSC99Notifications)
		return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
	}

	// Non-macOS local sessions use native OS notifications if focus events are supported.
	// Without focus events, we can't suppress notifications when focused, so
	// we disable them entirely to avoid spamming the user.
	if caps.ReportFocusEvents {
		slog.Debug("Selected NativeBackend for local session")
		return notification.NewNativeBackend(notification.Icon)
	}

	slog.Debug("Selected NoopBackend (focus events not supported)")
	return notification.NoopBackend{}
}

func (m *UI) updateNotificationBackend() {
	cfg := m.com.Config()
	m.notifyBackend = selectNotificationBackend(m.caps, cfg)
}

// shouldSendNotification returns true if notifications should be sent based on
// current state. Focus reporting must be supported, window must not be
// focused, and notifications must not be disabled in config.
func (m *UI) shouldSendNotification() bool {
	cfg := m.com.Config()
	if cfg != nil && cfg.Options != nil && cfg.Options.NotificationStyle == "disabled" {
		return false
	}
	return m.caps.ReportFocusEvents && !m.notifyWindowFocused
}

// setState changes the UI state and focus.
func (m *UI) handlePermissionNotification(notification permission.PermissionNotification) {
	if toolItem := m.chat.MessageItem(notification.ToolCallID); toolItem != nil {
		if permItem, ok := toolItem.(chat.ToolMessageItem); ok {
			if notification.Granted {
				permItem.SetStatus(chat.ToolStatusRunning)
			} else {
				permItem.SetStatus(chat.ToolStatusAwaitingPermission)
			}
		}
	}

	// If this notification reflects a final resolution (granted or denied),
	// dismiss any open permissions dialog whose tool call ID matches. This
	// covers the case where another client resolved the request remotely.
	if !notification.Granted && !notification.Denied {
		return
	}
	// The request is resolved: drop the cached pending request so it is
	// not re-surfaced on a later session switch.
	if m.pendingPermission != nil && m.pendingPermission.ToolCallID == notification.ToolCallID {
		m.pendingPermission = nil
	}
	if d := m.dialog.Dialog(dialog.PermissionsID); d != nil {
		if perm, ok := d.(*dialog.Permissions); ok && perm.ToolCallID() == notification.ToolCallID {
			m.dialog.CloseDialog(dialog.PermissionsID)
		}
	}
}

// handleQuestionNotification dismisses an open question dialog whose
// tool call ID matches a resolved (answered or cancelled) question, and
// drops the cached pending request so it is not re-surfaced on a later
// session switch. Mirrors handlePermissionNotification.
func (m *UI) handleQuestionNotification(n question.Notification) {
	if !n.Answered && !n.Cancelled {
		return
	}
	if m.pendingQuestion != nil && m.pendingQuestion.ToolCallID == n.ToolCallID {
		m.pendingQuestion = nil
	}
	if d := m.dialog.Dialog(dialog.QuestionID); d != nil {
		if q, ok := d.(*dialog.Question); ok && q.ToolCallID() == n.ToolCallID {
			m.dialog.CloseDialog(dialog.QuestionID)
		}
	}
}

// handleAgentNotification translates domain agent events into desktop
// notifications using the UI notification backend.
func (m *UI) handleAgentNotification(n notify.Notification) tea.Cmd {
	switch n.Type {
	case notify.TypeAgentFinished:
		var cmds []tea.Cmd
		cmds = append(cmds, m.sendNotification(notification.Notification{
			Title:   "Crush is waiting...",
			Message: fmt.Sprintf("Agent's turn completed in \"%s\"", n.SessionTitle),
		}))
		if m.com.IsHyper() {
			cmds = append(cmds, m.fetchHyperCredits())
		}
		return tea.Batch(cmds...)
	case notify.TypeReAuthenticate:
		return m.handleReAuthenticate(n.ProviderID)
	default:
		return nil
	}
}

func (m *UI) handleReAuthenticate(providerID string) tea.Cmd {
	cfg := m.com.Config()
	if cfg == nil {
		return nil
	}
	providerCfg, ok := cfg.Providers.Get(providerID)
	if !ok {
		return nil
	}
	agentCfg, ok := cfg.Agents[config.AgentCoder]
	if !ok {
		return nil
	}
	return m.openAuthenticationDialog(providerCfg.ToProvider(), cfg.Models[agentCfg.Model], agentCfg.Model)
}

// newSession clears the current session state and prepares for a new session.
// The actual session creation happens when the user sends their first message.
// Returns a command to reload prompt history.
