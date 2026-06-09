package dialog

import (
	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/ui/common"
)

// ForkProgressID is the identifier for the fork progress dialog.
const ForkProgressID = "fork_progress"

const forkProgressDialogWidth = 50

// ForkProgress is a non-interactive dialog showing live fork progress. It is
// driven by ForkProgress events streamed from the server while the (blocking)
// fork RPC runs, so the UI shows a progress bar instead of appearing frozen.
type ForkProgress struct {
	com     *common.Common
	bar     progress.Model
	stage   string
	percent float64
}

var _ Dialog = (*ForkProgress)(nil)

// NewForkProgress creates a new fork progress dialog.
func NewForkProgress(com *common.Common) *ForkProgress {
	bar := progress.New(
		progress.WithColors(
			com.Styles.WorkingGradFromColor,
			com.Styles.WorkingGradToColor,
		),
		progress.WithoutPercentage(),
	)
	return &ForkProgress{
		com:   com,
		bar:   bar,
		stage: "starting",
	}
}

// ID implements Dialog.
func (f *ForkProgress) ID() string { return ForkProgressID }

// SetProgress updates the displayed stage and completion fraction.
func (f *ForkProgress) SetProgress(stage string, percent float64) {
	if stage != "" {
		f.stage = stage
	}
	f.percent = percent
}

// HandleMsg implements Dialog. The fork operation cannot be cancelled, so all
// input is absorbed; the dialog closes itself when the fork completes.
func (f *ForkProgress) HandleMsg(tea.Msg) Action { return nil }

// Cursor implements Dialog.
func (f *ForkProgress) Cursor() *tea.Cursor { return nil }

// Draw implements Dialog.
func (f *ForkProgress) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := f.com.Styles
	width := max(0, min(forkProgressDialogWidth, area.Dx()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	f.bar.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Forking Conversation"

	stage := t.Dialog.SecondaryText.Render(f.stage + "…")
	bar := f.bar.ViewAs(f.percent)
	rc.AddPart(lipgloss.JoinVertical(lipgloss.Left, stage, bar))

	view := rc.Render()
	DrawCenter(scr, area, view)
	return nil
}
