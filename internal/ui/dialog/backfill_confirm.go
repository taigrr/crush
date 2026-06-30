package dialog

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/ui/common"
)

// BackfillConfirmID is the identifier for the embedding backfill
// confirmation dialog.
const BackfillConfirmID = "backfill_confirm"

// BackfillConfirm is a confirmation dialog shown before embedding
// existing conversation history (which makes API calls).
type BackfillConfirm struct {
	com        *common.Common
	count      int
	model      string
	selectedNo bool
	keyMap     struct {
		LeftRight,
		EnterSpace,
		Yes,
		No,
		Tab,
		Close key.Binding
	}
}

var _ Dialog = (*BackfillConfirm)(nil)

// NewBackfillConfirm creates a backfill confirmation dialog for count
// pending messages under the given model label.
func NewBackfillConfirm(com *common.Common, count int, model string) *BackfillConfirm {
	d := &BackfillConfirm{
		com:        com,
		count:      count,
		model:      model,
		selectedNo: true,
	}
	d.keyMap.LeftRight = key.NewBinding(
		key.WithKeys("left", "right"),
		key.WithHelp("←/→", "switch options"),
	)
	d.keyMap.EnterSpace = key.NewBinding(
		key.WithKeys("enter", " "),
		key.WithHelp("enter/space", "confirm"),
	)
	d.keyMap.Yes = key.NewBinding(
		key.WithKeys("y", "Y"),
		key.WithHelp("y", "yes"),
	)
	d.keyMap.No = key.NewBinding(
		key.WithKeys("n", "N"),
		key.WithHelp("n", "no"),
	)
	d.keyMap.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch options"),
	)
	d.keyMap.Close = CloseKey
	return d
}

// ID implements Dialog.
func (*BackfillConfirm) ID() string { return BackfillConfirmID }

// HandleMsg implements Dialog.
func (d *BackfillConfirm) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.LeftRight, d.keyMap.Tab):
			d.selectedNo = !d.selectedNo
		case key.Matches(msg, d.keyMap.EnterSpace):
			if !d.selectedNo {
				return ActionConfirmBackfill{}
			}
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Yes):
			return ActionConfirmBackfill{}
		case key.Matches(msg, d.keyMap.No):
			return ActionClose{}
		}
	}
	return nil
}

// Draw implements Dialog.
func (d *BackfillConfirm) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	baseStyle := d.com.Styles.Dialog.Quit.Content
	question := fmt.Sprintf("Embed %d message(s) with %s?", d.count, d.model)
	note := "This makes API calls and may cost money."
	buttonOpts := []common.ButtonOpts{
		{Text: "Embed", Selected: !d.selectedNo, Padding: 3},
		{Text: "Cancel", Selected: d.selectedNo, Padding: 3},
	}
	buttons := common.ButtonGroup(d.com.Styles, buttonOpts, " ")
	content := baseStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Center,
			question,
			note,
			"",
			buttons,
		),
	)
	view := d.com.Styles.Dialog.Quit.Frame.Render(content)
	DrawCenter(scr, area, view)
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *BackfillConfirm) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.LeftRight, d.keyMap.EnterSpace}
}

// FullHelp implements [help.KeyMap].
func (d *BackfillConfirm) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{d.keyMap.LeftRight, d.keyMap.EnterSpace, d.keyMap.Yes, d.keyMap.No},
		{d.keyMap.Tab, d.keyMap.Close},
	}
}
