package model

import (
	"image"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
)

// selectedLargeModel returns the currently selected large language model from
// the agent coordinator, if one exists.
func (m *UI) selectedLargeModel() *workspace.AgentModel {
	if m.com.Workspace.AgentIsReady() {
		model := m.com.Workspace.AgentModel()
		return &model
	}
	return nil
}

// landingView renders the landing page view showing the current working
// directory, model information, and LSP/MCP status in a two-column layout.
func (m *UI) landingView() string {
	t := m.com.Styles
	width := m.layout.main.Dx()

	// Config root (always the .crush/ project root) with wrench icon.
	cfgRootPath := m.com.Workspace.BaseDir()
	cfgRoot := common.PrettyPath(t, styles.WrenchIcon+" "+cfgRootPath, width)

	parts := []string{cfgRoot}

	// Effective working dir (user's launch cwd) with folder icon. Only
	// shown when different from the config root (linked worktrees case).
	if effectivePath := m.com.Workspace.EffectiveWorkingDir(); effectivePath != cfgRootPath {
		parts = append(parts, common.PrettyPath(t, styles.FolderIcon+" "+effectivePath, width))
	}

	// Git branch line (worktree lookups are session-scoped, so the
	// landing screen only shows the plain branch).
	if branch := m.com.Workspace.GitBranch(); branch != "" {
		parts = append(parts, t.Sidebar.WorkingDir.Render(styles.GitBranchIcon+" "+branch))
	}

	parts = append(parts, "", m.modelInfo(width))
	infoSection := lipgloss.JoinVertical(lipgloss.Left, parts...)

	var remainingHeightArea image.Rectangle
	layout.Vertical(
		layout.Len(lipgloss.Height(infoSection)+1),
		layout.Fill(1),
	).Split(m.layout.main).Assign(new(image.Rectangle), &remainingHeightArea)

	remainingHeight := max(1, remainingHeightArea.Dy())

	// Left column: LSP / MCP / Skills status (as before).
	mcpLspSectionWidth := min(30, (width-2)/4)
	lspSection := m.lspInfo(mcpLspSectionWidth, remainingHeight, false)
	mcpSection := m.mcpInfo(mcpLspSectionWidth, remainingHeight, false)
	skillsSection := m.skillsInfo(mcpLspSectionWidth, remainingHeight, false)
	statusCols := lipgloss.JoinHorizontal(lipgloss.Left, lspSection, " ", mcpSection, " ", skillsSection)

	// Right column: recent sessions in this workspace, filling the space
	// that was previously empty. Loaded async at startup; empty until then.
	statusWidth := lipgloss.Width(statusCols)
	const gap = 2
	sessionsWidth := max(20, width-statusWidth-gap)
	recent := m.landingRecentSessions(sessionsWidth, remainingHeight)

	var content string
	if recent != "" {
		sessionsPanel := lipgloss.NewStyle().Width(sessionsWidth).Render(recent)
		content = lipgloss.JoinHorizontal(lipgloss.Left, statusCols, "  ", sessionsPanel)
	} else {
		content = statusCols
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(m.layout.main.Dy() - 1).
		PaddingTop(1).
		Render(
			lipgloss.JoinVertical(lipgloss.Left, infoSection, "", content),
		)
}

// landingRecentSessions renders a compact "Recent sessions" panel for the
// current workspace from the cross-workspace overview data. Returns "" when
// there are no sessions yet (or the data has not loaded). Rows are
// truncated to width so they never wrap.
func (m *UI) landingRecentSessions(width, height int) string {
	if m.leftSidebar == nil {
		return ""
	}
	sessions := m.leftSidebar.AttachedSessions()
	if len(sessions) == 0 {
		return ""
	}

	t := m.com.Styles
	heading := t.Resource.Heading.Render("Recent sessions")
	lines := []string{heading}

	// Reserve the heading + a trailing hint line.
	maxRows := max(1, height-2)
	for i, s := range sessions {
		if i >= maxRows {
			break
		}
		var marker string
		switch {
		case s.IsBusy:
			marker = t.Resource.BusyIcon.String()
		case s.Unread:
			marker = t.Resource.OnlineIcon.String()
		default:
			marker = " "
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		// Prefix is marker + space (2 cells); truncate the title so the
		// row fits width and never wraps.
		title = ansi.Truncate(title, max(1, width-2), "…")
		lines = append(lines, t.Resource.Name.Render(marker+" "+title))
	}

	if len(sessions) > maxRows {
		lines = append(lines, t.Resource.AdditionalText.Render(
			"…and more (ctrl+s)",
		))
	} else {
		lines = append(lines, "", t.Resource.AdditionalText.Render("ctrl+s to browse all"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// effectiveModel returns the model the sidebar's session actually runs on:
// its own model reference when it was spawned with one (swarm `new` with
// `model`) and that reference still resolves, otherwise the workspace's
// large model. Mirrors coordinator.sessionModel so the sidebar never
// disagrees with what a turn uses.
func (m *UI) effectiveModel() *workspace.AgentModel {
	large := m.selectedLargeModel()
	sess := m.sidebarSession()
	cfg := m.com.Config()
	// A previewed session may belong to another workspace whose roles this
	// config knows nothing about; only resolve the committed session's ref.
	if sess == nil || sess != m.session || sess.ModelRef == "" || cfg == nil {
		return large
	}
	sel, err := cfg.ResolveModelRef(sess.ModelRef)
	if err != nil {
		return large
	}
	cw := cfg.GetModel(sel.Provider, sel.Model)
	if cw == nil {
		return large
	}
	if sel.MaxTokens == 0 {
		sel.MaxTokens = cw.DefaultMaxTokens
	}
	return &workspace.AgentModel{CatwalkCfg: *cw, ModelCfg: sel}
}
