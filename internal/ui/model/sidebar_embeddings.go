package model

import (
	"context"
	"strconv"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/taigrr/crush/internal/ui/common"
)

// embeddingPollInterval is how often the sidebar refreshes embedding
// backfill progress while a backfill is running.
const embeddingPollInterval = time.Second

// pollEmbeddingStatusCmd fetches the current embedding index status once.
// The handler reschedules it while backfillActive remains true.
func (m *UI) pollEmbeddingStatusCmd() tea.Cmd {
	ws := m.com.Workspace
	return tea.Tick(embeddingPollInterval, func(time.Time) tea.Msg {
		status, err := ws.EmbedStatus(context.Background())
		return embeddingStatusMsg{status: status, err: err}
	})
}

// embeddingProgress renders the backfill progress bar for the sidebar. It
// returns "" unless a backfill is active, so the section only appears
// while embedding history.
func (m *UI) embeddingProgress(width int) string {
	if !m.backfillActive {
		return ""
	}
	t := m.com.Styles
	s := m.backfillStatus

	percent := 0.0
	if s.Total > 0 {
		percent = float64(s.Embedded) / float64(s.Total)
		if percent > 1 {
			percent = 1
		}
	}

	// Build the bar from the current theme each render so it always
	// follows the active theme (colors would otherwise be captured once
	// and go stale after a theme switch).
	bar := progress.New(
		progress.WithColors(t.WorkingGradFromColor, t.WorkingGradToColor),
		progress.WithoutPercentage(),
	)
	bar.SetWidth(width)

	// Themed section header ("Embeddings" with a count), matching the
	// LSP/MCP/Skills sidebar sections.
	title := t.Resource.Heading.Render("Embeddings")
	var info []string
	if s.Total > 0 {
		info = append(info, strconv.Itoa(s.Embedded)+"/"+strconv.Itoa(s.Total))
	}
	header := common.Section(t, title, width, info...)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		bar.ViewAs(percent),
	)
}
