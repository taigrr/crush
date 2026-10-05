package model

import (
	"context"

	agenttools "github.com/taigrr/crush/internal/agent/tools"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/version"
)

// openDialog opens a dialog by its ID.
func (m *UI) newSession() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	m.session = nil
	m.rightSidebarOffset = 0
	m.sessionFiles = nil
	m.sessionFileReads = nil
	m.previewSessionID = ""
	m.pendingPreviewID = ""
	m.pendingPreviewRoot = ""
	m.previewSess = nil
	m.previewFiles = nil
	m.previewGen++
	m.leftSidebar.SetActiveSession("")
	// Clear active session for worktree-aware working directory.
	m.com.Workspace.SetActiveSessionID("")
	// Close any permission dialog bound to the session we just left.
	m.syncPermissionDialogForSession()
	m.syncQuestionDialogForSession()
	m.setState(uiLanding, uiFocusEditor)
	m.textarea.Focus()
	m.chat.Blur()
	m.chat.ClearMessages()
	m.pillsExpanded = false
	m.pillsAutoExpanded = false
	m.promptQueue = 0
	m.pillsView = ""
	m.historyReset()
	agenttools.ResetCache()
	// LSP clients are shared across the workspace's sessions. Only stop
	// them when no run is in flight; otherwise a busy session (this one
	// or another, all continuing on the server) could have its in-flight
	// LSP tool calls disrupted. LSP restarts on demand for the new
	// session either way.
	busy := m.isWorkspaceBusy()
	return tea.Batch(
		func() tea.Msg {
			if !busy {
				m.com.Workspace.LSPStopAll(context.Background())
			}
			return nil
		},
		m.loadPromptHistory(),
		m.reportCurrentSession(""),
	)
}

// handlePasteMsg handles a paste message.
func (m *UI) drawSessionDetails(scr uv.Screen, area uv.Rectangle) {
	if m.session == nil {
		return
	}

	s := m.com.Styles

	width := area.Dx() - s.CompactDetails.View.GetHorizontalFrameSize()
	height := area.Dy() - s.CompactDetails.View.GetVerticalFrameSize()

	title := m.renderSessionTitle(s.CompactDetails.Title, width)
	blocks := []string{
		title,
		"",
		m.modelInfo(width),
		"",
	}

	detailsHeader := lipgloss.JoinVertical(
		lipgloss.Left,
		blocks...,
	)

	version := s.CompactDetails.Version.Width(width).AlignHorizontal(lipgloss.Right).Render(version.Version)

	remainingHeight := height - lipgloss.Height(detailsHeader) - lipgloss.Height(version)

	const maxSectionWidth = 50
	sectionWidth := max(1, min(maxSectionWidth, width/4-2)) // account for spacing between sections
	maxItemsPerSection := remainingHeight - 3               // Account for section title and spacing

	lspSection := m.lspInfo(sectionWidth, maxItemsPerSection, false)
	mcpSection := m.mcpInfo(sectionWidth, maxItemsPerSection, false)
	skillsSection := m.skillsInfo(sectionWidth, maxItemsPerSection, false)
	filesSection := m.filesInfo(m.sidebarFiles(), m.com.Workspace.WorkingDir(), sectionWidth, maxItemsPerSection, false)
	sections := lipgloss.JoinHorizontal(lipgloss.Top, filesSection, " ", lspSection, " ", mcpSection, " ", skillsSection)
	uv.NewStyledString(
		s.CompactDetails.View.
			Width(area.Dx()).
			Render(
				lipgloss.JoinVertical(
					lipgloss.Left,
					detailsHeader,
					sections,
					version,
				),
			),
	).Draw(scr, area)
}
