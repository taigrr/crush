package model

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/swarm"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/logo"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
	"github.com/taigrr/crush/internal/worktree"
)

// modelInfo renders the current model information including reasoning
// settings and context usage/cost for the sidebar.
func (m *UI) modelInfo(width int) string {
	model := m.effectiveModel()
	providerName := ""
	reasoningInfo := ""

	if model != nil {
		if providerConfig, ok := m.com.Config().Providers.Get(model.ModelCfg.Provider); ok {
			providerName = providerConfig.Name
			reasoningInfo = reasoningInfoFor(model)
		}
	}

	var modelContext *common.ModelContextInfo
	if model != nil && m.sidebarSession() != nil {
		sess := m.sidebarSession()
		modelContext = &common.ModelContextInfo{
			ContextUsed:  sess.CompletionTokens + sess.PromptTokens,
			Cost:         sess.Cost,
			ModelContext: int64(model.CatwalkCfg.ContextWindow),
		}
	}
	var modelName string
	if model != nil {
		modelName = model.CatwalkCfg.Name
	}
	return common.ModelInfo(m.com.Styles, modelName, providerName, reasoningInfo, modelContext, width, m.hyperCredits)
}

// reasoningInfoFor builds the sidebar's reasoning label for a model: the
// thinking/reasoning state. Returns "" when the model cannot reason.
func reasoningInfoFor(model *workspace.AgentModel) string {
	if !model.CatwalkCfg.CanReason {
		return ""
	}
	if len(model.CatwalkCfg.ReasoningLevels) == 0 {
		if model.ModelCfg.Think {
			return "Thinking On"
		}
		return "Thinking Off"
	}
	effort := cmp.Or(model.ModelCfg.ReasoningEffort, model.CatwalkCfg.DefaultReasoningEffort)
	return fmt.Sprintf("Reasoning %s", common.FormatReasoningEffort(effort))
}

// getDynamicHeightLimits will give us the num of items to show in each section based on the height
// some items are more important than others.
func getDynamicHeightLimits(availableHeight, fileCount, lspCount, mcpCount, skillCount int) (maxFiles, maxLSPs, maxMCPs, maxSkills int) {
	const (
		minItemsPerSection = 2
		// Keep these high so dynamic layout uses available sidebar space
		// instead of hitting small hard limits.
		defaultMaxFilesShown    = 1000
		defaultMaxLSPsShown     = 1000
		defaultMaxMCPsShown     = 1000
		defaultMaxSkillsShown   = 1000
		minAvailableHeightLimit = 10
	)

	if availableHeight < minAvailableHeightLimit {
		return minItemsPerSection, minItemsPerSection, minItemsPerSection, minItemsPerSection
	}

	maxFiles = minItemsPerSection
	maxLSPs = minItemsPerSection
	maxMCPs = minItemsPerSection
	maxSkills = minItemsPerSection

	remainingHeight := max(0, availableHeight-(minItemsPerSection*4))

	sectionValues := []*int{&maxFiles, &maxLSPs, &maxMCPs, &maxSkills}
	sectionCaps := []int{defaultMaxFilesShown, defaultMaxLSPsShown, defaultMaxMCPsShown, defaultMaxSkillsShown}
	sectionNeeds := []int{max(0, fileCount-maxFiles), max(0, lspCount-maxLSPs), max(0, mcpCount-maxMCPs), max(0, skillCount-maxSkills)}

	for remainingHeight > 0 {
		allocated := false
		for i, section := range sectionValues {
			if remainingHeight == 0 {
				break
			}
			if sectionNeeds[i] == 0 || *section >= sectionCaps[i] {
				continue
			}
			*section = *section + 1
			sectionNeeds[i]--
			remainingHeight--
			allocated = true
		}
		if !allocated {
			break
		}
	}

	for remainingHeight > 0 {
		allocated := false
		for i, section := range sectionValues {
			if remainingHeight == 0 {
				break
			}
			if *section >= sectionCaps[i] {
				continue
			}
			*section = *section + 1
			remainingHeight--
			allocated = true
		}
		if !allocated {
			break
		}
	}

	return maxFiles, maxLSPs, maxMCPs, maxSkills
}

// swarmAddressCopiedMessage is the footer alert shown after a click on the
// sidebar's swarm address copies it to the clipboard.
const swarmAddressCopiedMessage = "Swarm address copied to clipboard"

// handleSwarmAddressClick copies the session's swarm address when the click
// lands on the sidebar row that renders it. It reports whether the click
// was consumed. Row/address are recorded by drawSidebar each frame, so a
// click is only honored against the last drawn layout.
func (m *UI) handleSwarmAddressClick(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	if m.swarmAddrRow < 0 || m.swarmAddr == "" {
		return nil, false
	}
	pt := image.Pt(msg.X, msg.Y)
	if !pt.In(m.layout.sidebar) || msg.Y != m.swarmAddrRow {
		return nil, false
	}
	return common.CopyToClipboard(m.swarmAddr, swarmAddressCopiedMessage), true
}

// sidebar renders the chat sidebar containing session title, working
// directory, model info, file list, LSP status, and MCP status.
func (m *UI) drawSidebar(scr uv.Screen, area uv.Rectangle) {
	m.swarmAddrRow = -1
	m.swarmAddr = ""
	if m.session == nil {
		return
	}

	const logoHeightBreakpoint = 30

	t := m.com.Styles
	width := area.Dx()
	height := area.Dy()

	// sess is the session whose stats this sidebar renders: the previewed
	// session while a live preview is shown, otherwise the committed one
	// (see sidebarSession). Everything session-specific below reads from
	// sess so the sidebar tracks the chat's live preview.
	sess := m.sidebarSession()

	title := m.renderSessionTitle(t.Sidebar.SessionTitle, width)

	// Swarm identity line: "<colorblock> <color-animal>" shown above
	// the session title so the session's addressable name is visible
	// alongside its summary. Empty when the session has no assigned
	// identity yet.
	var swarmLine string
	if sess != nil && sess.Color != "" && sess.Animal != "" {
		square := common.SwarmSquare(sess.Color)
		addr := swarm.FormatAddress(
			swarm.Identity{Color: sess.Color, Animal: sess.Animal},
			sess.ID,
		)
		m.swarmAddr = addr
		label := t.Sidebar.WorkingDir.Render(addr)
		if square != "" {
			swarmLine = square + " " + label
		} else {
			swarmLine = label
		}
		swarmLine = ansi.Truncate(swarmLine, width, "…")
	}

	// Config root (always the .crush/ project root) with wrench icon.
	cfgRootPath := m.com.Workspace.BaseDir()
	cfgRoot := common.PrettyPath(t, styles.WrenchIcon+" "+cfgRootPath, width)

	// Effective working dir for this session. Prefer the session's own
	// recorded working dir so attaching to a session that was started in
	// a different worktree shows that worktree, not this client's launch
	// cwd. Falls back to the client's launch cwd when unrecorded.
	// Only shown when different from the config root (e.g. linked worktrees).
	effectivePath := m.com.Workspace.EffectiveWorkingDir()
	if sess != nil && sess.WorkingDir != "" {
		effectivePath = sess.WorkingDir
	}
	var cwdLine string
	if effectivePath != cfgRootPath {
		cwdLine = common.PrettyPath(t, styles.FolderIcon+" "+effectivePath, width)
	}

	sidebarLogo := m.sidebarLogo
	if height < logoHeightBreakpoint {
		sidebarLogo = logo.SmallRender(m.com.Styles, width, logo.Opts{
			Hyper: m.com.IsHyper(),
		})
	}

	// Build git/worktree info line with git symbol prefix.
	var gitInfo string
	var activeWorktree *worktree.Worktree
	if sess != nil && m.com.Workspace.WorktreesEnabled() {
		activeWorktree, _ = m.com.Workspace.GetActiveWorktree(context.Background(), sess.ID)
	}
	if activeWorktree != nil {
		gitInfo = t.Sidebar.WorkingDir.Render("⑂ " + activeWorktree.Name)
	} else if branch := m.com.Workspace.GitBranchForDir(effectivePath); branch != "" {
		gitInfo = t.Sidebar.WorkingDir.Render(styles.GitBranchIcon + " " + branch)
	}

	blocks := []string{
		sidebarLogo,
	}
	if swarmLine != "" {
		blocks = append(blocks, swarmLine)
	}
	// Connection status, shown just above the session title (between
	// the identity block and the title) only while something needs
	// attention (starting up or reconnecting), so it is noticed
	// without adding clutter once healthy. Read live from the
	// workspace so the sidebar and header share one source of truth.
	if connLine := connectionStatusLine(t, m.com.Workspace.ConnectionState(), width); connLine != "" {
		blocks = append(blocks, connLine)
	}
	blocks = append(
		blocks,
		title,
		"",
		cfgRoot,
	)
	if cwdLine != "" {
		blocks = append(blocks, cwdLine)
	}
	if gitInfo != "" {
		blocks = append(blocks, gitInfo)
	}
	blocks = append(
		blocks,
		"",
		m.modelInfo(width),
		"",
	)
	// Embedding backfill progress, shown under the model and above
	// modified files, only while a backfill is running.
	if embProgress := m.embeddingProgress(width); embProgress != "" {
		blocks = append(blocks, embProgress, "")
	}

	sidebarHeader := lipgloss.JoinVertical(
		lipgloss.Left,
		blocks...,
	)

	// Render all sections at their full size, then virtual-scroll the
	// joined content. Give a very large available height so nothing is
	// truncated by the per-section limits.
	const maxAvailableHeight = 100000
	sidebarFiles := m.sidebarFiles()
	filesCount := 0
	for _, f := range sidebarFiles {
		if f.Additions == 0 && f.Deletions == 0 {
			continue
		}
		filesCount++
	}

	lspsCount := len(m.lspStates)

	mcpsCount := 0
	for _, mcpCfg := range m.com.Config().MCP.Sorted() {
		if _, ok := m.mcpStates[mcpCfg.Name]; ok {
			mcpsCount++
		}
	}

	skillsCount := len(m.skillStatusItems())

	maxFiles, maxLSPs, maxMCPs, maxSkills := getDynamicHeightLimits(maxAvailableHeight, filesCount, lspsCount, mcpsCount, skillsCount)

	lspSection := m.lspInfo(width, maxLSPs, true)
	mcpSection := m.mcpInfo(width, maxMCPs, true)
	skillsSection := m.skillsInfo(width, maxSkills, true)
	filesSection := m.filesInfo(sidebarFiles, m.com.Workspace.WorkingDir(), width, maxFiles, true)

	// Build the full sidebar content.
	fullContent := lipgloss.JoinVertical(
		lipgloss.Left,
		sidebarHeader,
		filesSection,
		"",
		lspSection,
		"",
		mcpSection,
		"",
		skillsSection,
	)

	// Split into lines for virtual scrolling and update scroll bookkeeping.
	lines := strings.Split(fullContent, "\n")
	totalLines := len(lines)
	m.rightSidebarScrollable = totalLines > height
	m.rightSidebarMaxOffsetVal = max(0, totalLines-height)

	// Clamp the offset in case content shrank since the last frame.
	if m.rightSidebarOffset > m.rightSidebarMaxOffsetVal {
		m.rightSidebarOffset = m.rightSidebarMaxOffsetVal
	}

	// The swarm line is the first block after the logo, so its index in
	// lines is the logo's height. Translate through the scroll offset to
	// an absolute screen row; -1 when scrolled out of view.
	if swarmLine != "" {
		swarmRow := lipgloss.Height(sidebarLogo) - m.rightSidebarOffset
		if swarmRow >= 0 && swarmRow < height {
			m.swarmAddrRow = area.Min.Y + swarmRow
		}
	}

	end := min(m.rightSidebarOffset+height, totalLines)
	visibleStr := strings.Join(lines[m.rightSidebarOffset:end], "\n")

	// Show the scrollbar only while the sidebar is focused and scrollable.
	scrollbarVisible := m.rightSidebarScrollable && m.focus == uiFocusRightSidebar

	contentWidth := width
	if scrollbarVisible {
		contentWidth = width - 1
	}

	uv.NewStyledString(
		lipgloss.NewStyle().
			MaxWidth(contentWidth).
			MaxHeight(height).
			Render(visibleStr),
	).Draw(scr, area)

	if scrollbarVisible {
		scrollbar := common.Scrollbar(m.com.Styles, height, totalLines, height, m.rightSidebarOffset)
		if scrollbar != "" {
			scrollbarArea := image.Rectangle{
				Min: image.Point{X: area.Max.X - 1, Y: area.Min.Y},
				Max: area.Max,
			}
			uv.NewStyledString(scrollbar).Draw(scr, scrollbarArea)
		}
	}
}
