package dialog

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
)

// MilestonesID is the identifier for the milestones dialog.
const MilestonesID = "milestones"

// Milestones is a milestone list dialog (Ctrl+Q).
type Milestones struct {
	com        *common.Common
	help       help.Model
	list       *list.FilterableList
	sessionID  string
	milestones []workspace.Milestone

	keyMap struct {
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Select   key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*Milestones)(nil)

// NewMilestones creates a new Milestones dialog.
func NewMilestones(com *common.Common, sessionID string) (*Milestones, error) {
	m := &Milestones{
		com:       com,
		sessionID: sessionID,
	}

	milestones, err := com.Workspace.ListMilestones(context.TODO(), sessionID)
	if err != nil {
		return nil, err
	}
	m.milestones = milestones

	helpModel := help.New()
	helpModel.Styles = com.Styles.DialogHelpStyles()
	m.help = helpModel

	m.list = list.NewFilterableList(milestoneItems(com.Styles, milestones)...)
	m.list.Focus()
	if len(milestones) > 0 {
		m.list.SetSelected(len(milestones) - 1)
		m.list.ScrollToSelected()
	}

	m.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n", "j"),
		key.WithHelp("↓", "next"),
	)
	m.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p", "k"),
		key.WithHelp("↑", "previous"),
	)
	m.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "navigate"),
	)

	closeKey := CloseKey
	closeKey.SetHelp("esc", "close")
	m.keyMap.Close = closeKey
	m.keyMap.Select = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "go to turn"),
	)

	return m, nil
}

// ID implements Dialog.
func (m *Milestones) ID() string {
	return MilestonesID
}

// HandleMsg implements Dialog.
func (m *Milestones) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Select):
			if item, ok := m.list.SelectedItem().(*milestoneItem); ok {
				return ActionScrollToTurn{TurnNumber: int(item.milestone.TurnNumber)}
			}
		case key.Matches(msg, m.keyMap.Previous):
			if m.list.IsSelectedFirst() {
				m.list.SelectLast()
			} else {
				m.list.SelectPrev()
			}
			m.list.ScrollToSelected()
		case key.Matches(msg, m.keyMap.Next):
			if m.list.IsSelectedLast() {
				m.list.SelectFirst()
			} else {
				m.list.SelectNext()
			}
			m.list.ScrollToSelected()
		}
	}
	return nil
}

// Draw implements Dialog.
func (m *Milestones) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))

	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	m.list.SetSize(innerWidth, height-heightOffset)
	m.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Milestones"
	if len(m.milestones) == 0 {
		rc.TitleInfo = "No milestones yet"
	} else {
		rc.TitleInfo = fmt.Sprintf("%d entries", len(m.milestones))
	}
	listView := t.Dialog.List.Height(m.list.Height()).Render(m.list.Render())
	rc.AddPart(listView)
	rc.Help = m.help.View(m)

	view := rc.Render()
	DrawCenter(scr, area, view)
	return nil
}

// ShortHelp implements help.KeyMap.
func (m *Milestones) ShortHelp() []key.Binding {
	return []key.Binding{
		m.keyMap.UpDown,
		m.keyMap.Select,
		m.keyMap.Close,
	}
}

// FullHelp implements help.KeyMap.
func (m *Milestones) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.keyMap.Next, m.keyMap.Previous},
		{m.keyMap.Select, m.keyMap.Close},
	}
}

// milestoneItem represents a milestone in the list.
type milestoneItem struct {
	*list.Versioned
	t         *styles.Styles
	milestone workspace.Milestone
	m         fuzzy.Match
	cache     map[int]string
	focused   bool
}

var _ ListItem = &milestoneItem{Versioned: list.NewVersioned()}

// Filter returns the filterable value of the milestone.
func (i *milestoneItem) Filter() string {
	return i.milestone.ShortSummary
}

// Finished implements list.Item.
func (i *milestoneItem) Finished() bool {
	return true
}

// ID returns the unique identifier of the milestone.
func (i *milestoneItem) ID() string {
	return i.milestone.ID
}

// SetMatch sets the fuzzy match for the milestone item.
func (i *milestoneItem) SetMatch(m fuzzy.Match) {
	i.cache = nil
	i.m = m
	i.Bump()
}

// SetFocused sets the focus state of the milestone item.
func (i *milestoneItem) SetFocused(focused bool) {
	if i.focused != focused {
		i.cache = nil
		i.focused = focused
		i.Bump()
	}
}

// Render returns the string representation of the milestone item.
func (i *milestoneItem) Render(width int) string {
	title := i.milestone.ShortSummary
	info := fmt.Sprintf("turn %d", i.milestone.TurnNumber)
	styles := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(styles, title, info, i.focused, width, i.cache, &i.m)
}

func milestoneItems(styles *styles.Styles, milestones []workspace.Milestone) []list.FilterableItem {
	items := make([]list.FilterableItem, len(milestones))
	for i, ms := range milestones {
		items[i] = &milestoneItem{
			Versioned: list.NewVersioned(),
			t:         styles,
			milestone: ms,
		}
	}
	return items
}
