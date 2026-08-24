package model

import (
	"fmt"
	"image"
	"math/rand/v2"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/screen"
	"github.com/taigrr/crush/internal/home"
	"github.com/taigrr/crush/internal/version"
)

// drawHeader draws the header section of the UI.
func (m *UI) drawHeader(scr uv.Screen, area uv.Rectangle) {
	m.header.drawHeader(
		scr,
		area,
		m.session,
		m.isCompact,
		m.detailsOpen,
		area.Dx(),
		m.hyperCredits,
	)
}

// Draw implements [uv.Drawable] and draws the UI model.
func (m *UI) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	layout := m.generateLayout(area.Dx(), area.Dy())

	if m.layout != layout {
		m.layout = layout
		m.updateSize()
	}

	// Clear the screen first
	screen.Clear(scr)

	// Tint the outer frame when a background session wants attention.
	// Drawn behind the content so the reserved margin cells carry it and
	// nothing else is displaced.
	if m.state == uiChat || m.state == uiLanding {
		m.drawAttentionBorder(scr, area)
	}

	switch m.state {
	case uiOnboarding:
		m.drawHeader(scr, layout.header)

		// NOTE: Onboarding flow will be rendered as dialogs below, but
		// positioned at the bottom left of the screen.

	case uiInitialize:
		m.drawHeader(scr, layout.header)

		main := uv.NewStyledString(m.initializeView())
		main.Draw(scr, layout.main)

	case uiLanding:
		m.drawHeader(scr, layout.header)
		main := uv.NewStyledString(m.landingView())
		main.Draw(scr, layout.main)

		editor := uv.NewStyledString(m.renderEditorView(m.editorContentWidth()))
		editor.Draw(scr, layout.editor)

	case uiChat:
		if m.isCompact {
			m.drawHeader(scr, layout.header)
		} else if layout.sidebar.Dx() > 0 {
			m.drawSidebar(scr, layout.sidebar)
		}

		m.chat.Draw(scr, layout.main)
		if layout.pills.Dy() > 0 && m.pillsView != "" {
			uv.NewStyledString(m.pillsView).Draw(scr, layout.pills)
		}

		editor := uv.NewStyledString(m.renderEditorView(m.editorContentWidth()))
		editor.Draw(scr, layout.editor)

		// Draw details overlay in compact mode when open
		if m.isCompact && m.detailsOpen {
			m.drawSessionDetails(scr, layout.sessionDetails)
		}
	}

	isOnboarding := m.state == uiOnboarding

	// Draw the left session navigator over its carved-out column.
	if m.leftSidebarVisible && layout.leftSidebar.Dx() > 0 {
		m.drawLeftSidebar(scr, layout.leftSidebar)
	}

	// Add status and help layer
	m.status.SetHideHelp(isOnboarding)
	m.status.Draw(scr, layout.status)

	// Draw completions popup if open
	if !isOnboarding && m.completionsOpen && m.completions.HasItems() {
		w, h := m.completions.Size()
		x := m.completionsPositionStart.X
		y := m.completionsPositionStart.Y - h

		screenW := area.Dx()
		if x+w > screenW {
			x = screenW - w
		}
		x = max(0, x)
		y = max(0, y+1) // Offset for attachments row

		completionsView := uv.NewStyledString(m.completions.Render())
		completionsView.Draw(scr, image.Rectangle{
			Min: image.Pt(x, y),
			Max: image.Pt(x+w, y+h),
		})
	}

	// Debugging rendering (visually see when the tui rerenders)
	if os.Getenv("CRUSH_UI_DEBUG") == "true" {
		debugView := lipgloss.NewStyle().Background(lipgloss.ANSIColor(rand.IntN(256))).Width(4).Height(2)
		debug := uv.NewStyledString(debugView.String())
		debug.Draw(scr, image.Rectangle{
			Min: image.Pt(4, 1),
			Max: image.Pt(8, 3),
		})
	}

	// This needs to come last to overlay on top of everything. We always pass
	// the full screen bounds because the dialogs will position themselves
	// accordingly.
	if m.dialog.HasDialogs() {
		return m.dialog.Draw(scr, scr.Bounds())
	}

	if m.focus == uiFocusEditor {
		return m.editorCaret()
	}
	return nil
}

// editorCaret returns the terminal caret position for the focused editor,
// translated from textarea-local coordinates into screen coordinates. It
// returns nil when the caret should be hidden.
func (m *UI) editorCaret() *tea.Cursor {
	if m.layout.editor.Dy() <= 0 {
		// Don't show cursor if editor is not visible
		return nil
	}
	if m.detailsOpen && m.isCompact {
		// Don't show cursor if details overlay is open
		return nil
	}
	if !m.textarea.Focused() {
		return nil
	}

	cur := m.textarea.Cursor()
	if cur == nil {
		return nil
	}
	// Offset by the editor's origin so the caret tracks the editor when panes
	// to the left (app margin, session navigator) shift it horizontally.
	cur.X += m.layout.editor.Min.X
	cur.Y += m.layout.editor.Min.Y + 1 // Offset for attachments row
	return cur
}

// View renders the UI model's view.
func (m *UI) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if !m.isTransparent {
		v.BackgroundColor = m.com.Styles.Background
	}
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = m.caps.ReportFocusEvents
	v.WindowTitle = "crush " + home.Short(m.com.Workspace.WorkingDir())

	if m.versionMismatch {
		v.Content = m.renderVersionMismatchBanner()
		return v
	}

	canvas := uv.NewScreenBuffer(m.width, m.height)
	v.Cursor = m.Draw(canvas, canvas.Bounds())

	content := strings.ReplaceAll(canvas.Render(), "\r\n", "\n") // normalize newlines
	contentLines := strings.Split(content, "\n")
	for i, line := range contentLines {
		// Trim trailing spaces for concise rendering
		contentLines[i] = strings.TrimRight(line, " ")
	}

	content = strings.Join(contentLines, "\n")

	v.Content = content
	if m.progressBarEnabled && m.sendProgressBar && m.isAgentBusy() {
		// HACK: use a random percentage to prevent ghostty from hiding it
		// after a timeout.
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarIndeterminate, rand.IntN(100))
	}

	return v
}

// renderVersionMismatchBanner renders a full-screen notice shown when the
// connected server's version differs from this client's. Interaction is
// unsafe across a protocol boundary, so the UI blocks until the user
// restarts crush.
func (m *UI) renderVersionMismatchBanner() string {
	serverVer := m.serverVersionStr
	if serverVer == "" {
		serverVer = "unknown"
	}
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("9")).
		Padding(0, 1).
		Render("VERSION MISMATCH")

	lines := []string{
		title,
		"",
		"The crush server is running a different version than this client.",
		"",
		fmt.Sprintf("  client: %s", version.Version),
		fmt.Sprintf("  server: %s", serverVer),
		"",
		"This usually happens when another crush instance restarted the",
		"shared server with a newer or older binary.",
		"",
		"Please quit (ctrl+c) and restart crush to reconnect.",
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("9")).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		box,
	)
}
