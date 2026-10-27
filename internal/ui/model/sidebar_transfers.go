package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/proto"
)

// transferPollInterval is how often move progress refreshes.
const transferPollInterval = 500 * time.Millisecond

type transfersMsg struct {
	transfers []proto.TransferStatus
}

type transferPollTickMsg struct{}

// startTransferPoll begins polling move progress for root ("" = the
// current workspace). Moves are blocking RPCs, so progress comes from a
// separate status endpoint on the sending node.
func (m *UI) startTransferPoll(root string) tea.Cmd {
	m.transferRoot = root
	m.movesInFlight++
	if m.transferPolling {
		return nil
	}
	m.transferPolling = true
	return m.fetchTransfers()
}

// endTransferPoll records a finished move; polling stops once none remain.
func (m *UI) endTransferPoll() {
	if m.movesInFlight > 0 {
		m.movesInFlight--
	}
	if m.movesInFlight == 0 {
		m.transfers = nil
	}
}

func (m *UI) fetchTransfers() tea.Cmd {
	ws, root := m.com.Workspace, m.transferRoot
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ts, _ := ws.Transfers(ctx, root)
		return transfersMsg{transfers: ts}
	}
}

func (m *UI) handleTransfers(msg transfersMsg) tea.Cmd {
	if m.movesInFlight == 0 {
		m.transferPolling = false
		m.transfers = nil
		return nil
	}
	m.transfers = msg.transfers
	if d, ok := m.dialog.Dialog(moveDialogID()).(interface {
		SetProgress(proto.TransferStatus)
	}); ok && len(msg.transfers) > 0 {
		d.SetProgress(msg.transfers[0])
	}
	return tea.Tick(transferPollInterval, func(time.Time) tea.Msg { return transferPollTickMsg{} })
}

func (m *UI) handleTransferPollTick() tea.Cmd {
	if m.movesInFlight == 0 {
		m.transferPolling = false
		return nil
	}
	return m.fetchTransfers()
}

// transferFraction is a move's completed share in [0,1].
func transferFraction(s proto.TransferStatus) float64 {
	if s.TotalBytes <= 0 {
		return 0
	}
	return min(1, float64(s.Bytes)/float64(s.TotalBytes))
}

// TransferSummary formats files and bytes for display.
func TransferSummary(s proto.TransferStatus) string {
	if s.TotalFiles == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d files · %s/%s", s.Files, s.TotalFiles, humanBytes(s.Bytes), humanBytes(s.TotalBytes))
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}

// transferProgress renders one sidebar section per move in flight.
func (m *UI) transferProgress(width int) string {
	if len(m.transfers) == 0 {
		return ""
	}
	t := m.com.Styles
	var sections []string
	for _, s := range m.transfers {
		title := "Moving"
		if s.TargetName != "" {
			title = "Moving to " + s.TargetName
		}
		var info []string
		if s.TotalFiles > 0 {
			info = append(info, fmt.Sprintf("%.0f%%", transferFraction(s)*100))
		}
		detail := s.Title
		if sum := TransferSummary(s); sum != "" {
			detail = sum
			if s.CurrentFile != "" {
				detail += "\n" + ansi.TruncateLeft(s.CurrentFile, max(0, ansi.StringWidth(s.CurrentFile)-width+1), "…")
			}
		} else if s.Stage != "" {
			detail = s.Stage + "…"
		}
		sections = append(sections, progressSection(t, title, info, detail, transferFraction(s), width))
	}
	return strings.Join(sections, "\n\n")
}
