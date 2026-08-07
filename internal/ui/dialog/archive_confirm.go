package dialog

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/ui/common"
)

// ArchiveConfirmID is the identifier for the archive-session confirmation
// dialog.
const ArchiveConfirmID = "archive_confirm"

// ArchiveConfirm is a confirmation dialog for archiving the current
// (active) session. It mirrors the quit dialog's look and structure:
// yes/no buttons switchable with arrows/tab and confirmable with
// enter/space, plus ctrl+y and a second ctrl+x to confirm (the latter
// mirroring how a second ctrl+c skips the quit confirmation).
type ArchiveConfirm struct {
	com        *common.Common
	selectedNo bool // true if "No" button is selected
	keyMap     struct {
		LeftRight,
		EnterSpace,
		Yes,
		No,
		Tab,
		Close,
		Confirm key.Binding
	}
}

var _ Dialog = (*ArchiveConfirm)(nil)

// NewArchiveConfirm creates a new archive-session confirmation dialog.
func NewArchiveConfirm(com *common.Common) *ArchiveConfirm {
	a := &ArchiveConfirm{
		com:        com,
		selectedNo: true,
	}
	a.keyMap.LeftRight = key.NewBinding(
		key.WithKeys("left", "right"),
		key.WithHelp("←/→", "switch options"),
	)
	a.keyMap.EnterSpace = key.NewBinding(
		key.WithKeys("enter", " "),
		key.WithHelp("enter/space", "confirm"),
	)
	a.keyMap.Yes = key.NewBinding(
		key.WithKeys("y", "Y", "ctrl+y"),
		key.WithHelp("y/ctrl+y", "yes"),
	)
	a.keyMap.No = key.NewBinding(
		key.WithKeys("n", "N"),
		key.WithHelp("n/N", "no"),
	)
	a.keyMap.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch options"),
	)
	a.keyMap.Close = CloseKey
	// A second ctrl+x confirms, matching the "press the open-key again to
	// skip confirmation" convention the quit dialog uses for ctrl+c.
	a.keyMap.Confirm = key.NewBinding(
		key.WithKeys("ctrl+x"),
		key.WithHelp("ctrl+x", "archive"),
	)
	return a
}

// ID implements [Model].
func (*ArchiveConfirm) ID() string {
	return ArchiveConfirmID
}

// HandleMsg implements [Model].
func (a *ArchiveConfirm) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, a.keyMap.Confirm, a.keyMap.Yes):
			return ActionArchiveSession{}
		case key.Matches(msg, a.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, a.keyMap.LeftRight, a.keyMap.Tab):
			a.selectedNo = !a.selectedNo
		case key.Matches(msg, a.keyMap.EnterSpace):
			if !a.selectedNo {
				return ActionArchiveSession{}
			}
			return ActionClose{}
		case key.Matches(msg, a.keyMap.No):
			return ActionClose{}
		}
	}

	return nil
}

// Draw implements [Dialog].
func (a *ArchiveConfirm) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	const question = "Archive this session?"
	baseStyle := a.com.Styles.Dialog.Quit.Content
	buttonOpts := []common.ButtonOpts{
		{Text: "Yep!", Selected: !a.selectedNo, Padding: 3},
		{Text: "Nope", Selected: a.selectedNo, Padding: 3},
	}
	buttons := common.ButtonGroup(a.com.Styles, buttonOpts, " ")
	content := baseStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Center,
			question,
			"",
			buttons,
		),
	)

	view := a.com.Styles.Dialog.Quit.Frame.Render(content)
	DrawCenter(scr, area, view)
	return nil
}

// ShortHelp implements [help.KeyMap].
func (a *ArchiveConfirm) ShortHelp() []key.Binding {
	return []key.Binding{
		a.keyMap.LeftRight,
		a.keyMap.EnterSpace,
	}
}

// FullHelp implements [help.KeyMap].
func (a *ArchiveConfirm) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{a.keyMap.LeftRight, a.keyMap.EnterSpace, a.keyMap.Yes, a.keyMap.No},
		{a.keyMap.Tab, a.keyMap.Close},
	}
}
