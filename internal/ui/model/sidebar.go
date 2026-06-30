package model

import (
	"cmp"
	"context"
	"fmt"
	"image"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/logo"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
	"github.com/taigrr/crush/internal/worktree"
)

// modelInfo renders the current model information including reasoning
// settings and context usage/cost for the sidebar.
func (m *UI) modelInfo(width int) string {
	model := m.selectedLargeModel()
	providerName := ""
	reasoningInfo := ""

	if model != nil {
		if providerConfig, ok := m.com.Config().Providers.Get(model.ModelCfg.Provider); ok {
			providerName = providerConfig.Name
			reasoningInfo = reasoningInfoFor(model)
		}
	}

	var modelContext *common.ModelContextInfo
	if model != nil && m.session != nil {
		modelContext = &common.ModelContextInfo{
			ContextUsed:  m.session.CompletionTokens + m.session.PromptTokens,
			Cost:         m.session.Cost,
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

// sidebar renders the chat sidebar containing session title, working
// directory, model info, file list, LSP status, and MCP status.
func (m *UI) drawSidebar(scr uv.Screen, area uv.Rectangle) {
	if m.session == nil {
		return
	}

	const logoHeightBreakpoint = 30

	t := m.com.Styles
	width := area.Dx()
	height := area.Dy()

	title := m.renderSessionTitle(t.Sidebar.SessionTitle, width)

	// Config root (always the .crush/ project root) with wrench icon.
	cfgRootPath := m.com.Workspace.BaseDir()
	cfgRoot := common.PrettyPath(t, styles.WrenchIcon+" "+cfgRootPath, width)

	// Effective working dir (user's launch cwd) with folder icon.
	// Only shown when different from the config root (e.g. linked worktrees).
	effectivePath := m.com.Workspace.EffectiveWorkingDir()
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
	if m.com.Workspace.WorktreesEnabled() {
		activeWorktree, _ = m.com.Workspace.GetActiveWorktree(context.Background(), m.session.ID)
	}
	if activeWorktree != nil {
		gitInfo = t.Sidebar.WorkingDir.Render("⑂ " + activeWorktree.Name)
	} else if branch := m.com.Workspace.GitBranch(); branch != "" {
		gitInfo = t.Sidebar.WorkingDir.Render(styles.GitBranchIcon + " " + branch)
	}

	blocks := []string{
		sidebarLogo,
		title,
		"",
		cfgRoot,
	}
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

	var remainingHeightArea image.Rectangle
	layout.Vertical(
		layout.Len(lipgloss.Height(sidebarHeader)),
		layout.Fill(1),
	).Split(m.layout.sidebar).Assign(new(image.Rectangle), &remainingHeightArea)
	remainingHeight := remainingHeightArea.Dy() - 6
	filesCount := 0
	for _, f := range m.sessionFiles {
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

	maxFiles, maxLSPs, maxMCPs, maxSkills := getDynamicHeightLimits(remainingHeight, filesCount, lspsCount, mcpsCount, skillsCount)

	lspSection := m.lspInfo(width, maxLSPs, true)
	mcpSection := m.mcpInfo(width, maxMCPs, true)
	skillsSection := m.skillsInfo(width, maxSkills, true)
	filesSection := m.filesInfo(m.com.Workspace.WorkingDir(), width, maxFiles, true)

	uv.NewStyledString(
		lipgloss.NewStyle().
			MaxWidth(width).
			MaxHeight(height).
			Render(
				lipgloss.JoinVertical(
					lipgloss.Left,
					sidebarHeader,
					filesSection,
					"",
					lspSection,
					"",
					mcpSection,
					"",
					skillsSection,
				),
			),
	).Draw(scr, area)
}
