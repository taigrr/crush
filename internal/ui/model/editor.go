package model

import (
	"image"
	"math/rand/v2"
	"os"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/editor"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/ui/util"
)

func (m *UI) openEditor(value string) tea.Cmd {
	tmpfile, err := os.CreateTemp("", "msg_*.md")
	if err != nil {
		return util.ReportError(err)
	}
	tmpPath := tmpfile.Name()
	defer tmpfile.Close() //nolint:errcheck
	if _, err := tmpfile.WriteString(value); err != nil {
		return util.ReportError(err)
	}
	cmd, err := editor.Command(
		"crush",
		tmpPath,
		editor.AtPosition(
			m.textarea.Line()+1,
			m.textarea.Column()+1,
		),
	)
	if err != nil {
		return util.ReportError(err)
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer func() {
			_ = os.Remove(tmpPath)
		}()

		if err != nil {
			return util.ReportError(err)
		}
		content, err := os.ReadFile(tmpPath)
		if err != nil {
			return util.ReportError(err)
		}
		if len(content) == 0 {
			return util.ReportWarn("Message is empty")
		}
		return openEditorMsg{
			Text: strings.TrimSpace(string(content)),
		}
	})
}

// setEditorPrompt configures the textarea prompt function based on whether
// yolo mode is enabled, and updates the cached m.yoloMode used elsewhere
// (e.g. the placeholder text) so both stay in sync from one source of
// truth. Callers must pass the currently-viewed workspace's live value —
// this function does not query the server itself.
func (m *UI) setEditorPrompt(yolo bool) {
	m.yoloMode = yolo
	if yolo {
		m.textarea.SetPromptFunc(4, m.yoloPromptFunc)
		return
	}
	m.textarea.SetPromptFunc(4, m.normalPromptFunc)
}

// normalPromptFunc returns the normal editor prompt style. On the first line
// it shows a themed ">" (or "!" when the input starts with "!"). Subsequent
// lines show the continuation ":::" dots.
func (m *UI) normalPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		if info.Focused {
			if strings.HasPrefix(m.textarea.Value(), "!") {
				return t.Editor.PromptBangIconFocused.Render()
			}
			return t.Editor.PromptNormalIconFocused.Render()
		}
		if strings.HasPrefix(m.textarea.Value(), "!") {
			return t.Editor.PromptBangIconBlurred.Render()
		}
		return t.Editor.PromptNormalIconBlurred.Render()
	}
	if info.Focused {
		return t.Editor.PromptNormalFocused.Render()
	}
	return t.Editor.PromptNormalBlurred.Render()
}

// yoloPromptFunc returns the yolo mode editor prompt style with warning icon
// and colored dots. The icon is " ! " to flag the elevated-permission mode,
// distinct from the " $ " bang-mode shell prefix.
func (m *UI) yoloPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		if info.Focused {
			return t.Editor.PromptYoloIconFocused.Render()
		} else {
			return t.Editor.PromptYoloIconBlurred.Render()
		}
	}
	if info.Focused {
		return t.Editor.PromptYoloDotsFocused.Render()
	}
	return t.Editor.PromptYoloDotsBlurred.Render()
}

// isAgentBusy reports whether the open session has a run in flight. One
// workspace hosts many sessions (swarm workers, other clients), so the
// workspace-wide busy flag would mark an idle session busy whenever a
// sibling is streaming: "esc cancel" with nothing to cancel, the working
// placeholder, /continue and New Session refused. Without an open
// session it falls back to the workspace-wide flag.
func (m *UI) isAgentBusy() bool {
	if !m.com.Workspace.AgentIsReady() {
		return false
	}
	if m.hasSession() {
		return m.com.Workspace.AgentIsSessionBusy(m.session.ID)
	}
	return m.com.Workspace.AgentIsBusy()
}

// isWorkspaceBusy reports whether any session in the workspace has a run
// in flight. Use it for workspace-scoped side effects (stopping LSPs,
// deferring a model reload) that must not disturb a sibling session.
func (m *UI) isWorkspaceBusy() bool {
	return m.com.Workspace.AgentIsReady() &&
		m.com.Workspace.AgentIsBusy()
}

// hasRunningBash reports whether the current session has a bash tool call
// in flight that could be moved to the background.
func (m *UI) hasRunningBash() bool {
	if !m.hasSession() || m.chat == nil {
		return false
	}
	_, ok := m.chat.RunningToolCall(tools.BashToolName)
	return ok
}

// hasSession returns true if there is an active session with a valid ID.
func (m *UI) hasSession() bool {
	return m.session != nil && m.session.ID != ""
}

// mimeOf detects the MIME type of the given content.
func (m *UI) randomizePlaceholders() {
	m.workingPlaceholder = workingPlaceholders[rand.IntN(len(workingPlaceholders))]
	m.readyPlaceholder = readyPlaceholders[rand.IntN(len(readyPlaceholders))]
}

// editorContentWidth returns the width the editor block (attachments row
// + textarea) is rendered at. It must exactly match the width passed to
// renderEditorView in Draw so mouse hit-testing on the attachments row
// agrees with what was actually drawn.
func (m *UI) editorContentWidth() int {
	return m.layout.editor.Dx()
}

// handleAttachmentClick handles a click on the pending-attachments row of
// the composer (always the first line of the rendered editor block, see
// renderEditorView). Attachment chips are removable by click; this is
// additive to the existing keyboard delete-mode flow and does not touch
// it. Returns true when the click landed on an attachment chip and was
// handled.
func (m *UI) handleAttachmentClick(msg tea.MouseClickMsg) bool {
	if len(m.attachments.List()) == 0 {
		return false
	}
	// The completions popup renders upward from the textarea cursor and
	// the compact details overlay can both cover the attachments row; if
	// either is open a click there belongs to the overlay, not a chip.
	if m.completionsOpen || (m.isCompact && m.detailsOpen) {
		return false
	}
	if msg.Y != m.layout.editor.Min.Y {
		return false
	}
	if !image.Pt(msg.X, msg.Y).In(m.layout.editor) {
		return false
	}
	x := msg.X - m.layout.editor.Min.X
	return m.attachments.HandleMouseClick(x, m.editorContentWidth())
}

// renderEditorView renders the editor view with attachments if any.
func (m *UI) renderEditorView(width int) string {
	var attachmentsView string
	if len(m.attachments.List()) > 0 {
		attachmentsView = m.attachments.Render(width)
	}
	return strings.Join([]string{
		attachmentsView,
		m.textarea.View(),
		"", // margin at bottom of editor
	}, "\n")
}
