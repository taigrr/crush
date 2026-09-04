package model

import (
	"context"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/fsext"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
	"github.com/taigrr/crush/internal/worktree"
)

const (
	headerDiag           = "╱"
	minHeaderDiags       = 3
	leftPadding          = 1
	rightPadding         = 1
	diagToDetailsSpacing = 1 // space between diagonal pattern and details section
)

type header struct {
	// cached logo and compact logo
	logo        string
	compactLogo string

	com     *common.Common
	width   int
	compact bool
}

// newHeader creates a new header model.
func newHeader(com *common.Common) *header {
	h := &header{
		com: com,
	}
	h.refresh()
	return h
}

// refresh rebuilds cached logo strings using the current styles. Call
// after the theme changes.
func (h *header) refresh() {
	t := h.com.Styles
	isHyper := h.com.IsHyper()
	charm := "Charm™"
	if !isHyper {
		charm = " " + charm
	}
	name := "CRUSH"
	if isHyper {
		name = "HYPERCRUSH"
	}
	h.compactLogo = t.Header.Charm.Render(charm) + " " +
		styles.ApplyBoldForegroundGrad(t.Header.LogoGradCanvas, name, t.Header.LogoGradFromColor, t.Header.LogoGradToColor) + " "
	// Force drawHeader to re-render the wide logo on the next frame.
	h.width = 0
	h.logo = ""
}

// drawHeader draws the header for the given session.
func (h *header) drawHeader(
	scr uv.Screen,
	area uv.Rectangle,
	session *session.Session,
	compact bool,
	detailsOpen bool,
	width int,
	hyperCredits *int,
) {
	t := h.com.Styles
	if width != h.width || compact != h.compact {
		h.logo = renderLogo(h.com.Styles, compact, h.com.IsHyper(), width)
	}

	h.width = width
	h.compact = compact

	if !compact || session == nil {
		uv.NewStyledString(h.logo).Draw(scr, area)
		return
	}

	if session.ID == "" {
		return
	}

	var b strings.Builder
	b.WriteString(h.compactLogo)

	availDetailWidth := width - leftPadding - rightPadding - lipgloss.Width(b.String()) - minHeaderDiags - diagToDetailsSpacing
	lspErrorCount := 0
	for _, info := range h.com.Workspace.LSPGetStates() {
		lspErrorCount += info.DiagnosticCount
	}
	details := renderHeaderDetails(
		h.com,
		session,
		lspErrorCount,
		detailsOpen,
		availDetailWidth,
		hyperCredits,
	)

	remainingWidth := width -
		lipgloss.Width(b.String()) -
		lipgloss.Width(details) -
		leftPadding -
		rightPadding -
		diagToDetailsSpacing

	if remainingWidth > 0 {
		b.WriteString(t.Header.Diagonals.Render(
			strings.Repeat(headerDiag, max(minHeaderDiags, remainingWidth)),
		))
		b.WriteString(" ")
	}

	b.WriteString(details)

	view := uv.NewStyledString(
		t.Header.Wrapper.Padding(0, rightPadding, 0, leftPadding).Render(b.String()),
	)
	view.Draw(scr, area)
}

// renderHeaderDetails renders the details section of the header.
func renderHeaderDetails(
	com *common.Common,
	session *session.Session,
	lspErrorCount int,
	detailsOpen bool,
	availWidth int,
	hyperCredits *int,
) string {
	t := com.Styles

	var parts []string

	// Connection status, shown first (most attention-grabbing spot)
	// and only while there is something to report, mirroring the
	// lspErrorCount>0 convention below. Compact mode has no sidebar,
	// so this is the only surface for the indicator here.
	switch com.Workspace.ConnectionState() {
	case workspace.ConnectionStateConnecting:
		parts = append(parts, t.Resource.BusyIcon.String()+" connecting")
	case workspace.ConnectionStateReconnecting:
		parts = append(parts, t.Resource.ErrorIcon.String()+" reconnecting")
	case workspace.ConnectionStateUpdating:
		parts = append(parts, t.Resource.BusyIcon.String()+" server updating")
	}

	if lspErrorCount > 0 {
		parts = append(parts, t.LSP.ErrorDiagnostic.Render(fmt.Sprintf("%s%d", styles.LSPErrorIcon, lspErrorCount)))
	}

	// Show worktree name if active, otherwise git branch. Prefer the
	// session's own recorded working dir so attaching to a session that
	// began in a different worktree shows that worktree's branch rather
	// than this client's launch cwd.
	var activeWorktree *worktree.Worktree
	if com.Workspace.WorktreesEnabled() {
		activeWorktree, _ = com.Workspace.GetActiveWorktree(context.Background(), session.ID)
	}
	branchDir := com.Workspace.EffectiveWorkingDir()
	if session.WorkingDir != "" {
		branchDir = session.WorkingDir
	}
	if activeWorktree != nil {
		parts = append(parts, t.Header.WorkingDir.Render("⑂ "+activeWorktree.Name))
	} else if branch := com.Workspace.GitBranchForDir(branchDir); branch != "" {
		parts = append(parts, t.Header.WorkingDir.Render(styles.GitBranchIcon+" "+branch))
	}

	agentCfg := com.Config().Agents[config.AgentCoder]
	model := com.Config().GetModelByType(agentCfg.Model)
	if model != nil && model.ContextWindow > 0 {
		percentage := (float64(session.CompletionTokens+session.PromptTokens) / float64(model.ContextWindow)) * 100
		percentageText := fmt.Sprintf("%d%%", int(percentage))
		if session.EstimatedUsage {
			percentageText = "~" + percentageText
		}
		formattedPercentage := t.Header.Percentage.Render(percentageText)
		parts = append(parts, formattedPercentage)
	}

	if com.IsHyper() && hyperCredits != nil {
		hc := t.Header.Hypercredit.Render(styles.HypercreditIcon) + " " + t.Header.Percentage.Render(common.FormatCredits(*hyperCredits))
		parts = append(parts, hc)
	}

	const keystroke = "ctrl+d"
	if detailsOpen {
		parts = append(parts, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" close"))
	} else {
		parts = append(parts, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" open "))
	}

	dot := t.Header.Separator.Render(" • ")
	metadata := strings.Join(parts, dot)
	metadata = dot + metadata

	// Use BaseDir to show project root, not worktree path.
	const dirTrimLimit = 4
	cwd := fsext.DirTrim(fsext.PrettyPath(com.Workspace.BaseDir()), dirTrimLimit)
	cwd = t.Header.WorkingDir.Render(cwd)

	result := cwd + metadata
	return ansi.Truncate(result, max(0, availWidth), "…")
}
