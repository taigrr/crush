package dialog

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
)

// SessionDialogID is the identifier for the session selector dialog.
const SessionDialogID = "session"

// Session is a session selector dialog.
type Session struct {
	width, height int
	com           *common.Common
	help          help.Model
	list          *list.FilterableList
	input         textinput.Model

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*Session)(nil)

// SessionSelectedMsg is a message sent when a session is selected.
type SessionSelectedMsg struct {
	Session session.Session
}

// NewSessions creates a new Session dialog.
func NewSessions(com *common.Common, sessions ...session.Session) *Session {
	s := new(Session)
	s.com = com
	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()

	s.help = help
	s.list = list.NewFilterableList(sessionItems(com.Styles, sessions...)...)
	s.list.Focus()
	s.list.SetSelected(0)

	s.input = textinput.New()
	s.input.SetVirtualCursor(false)
	s.input.Placeholder = "Enter session name"
	s.input.SetStyles(com.Styles.TextInput)
	s.input.Focus()

	s.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "tab", "ctrl+y"),
		key.WithHelp("enter", "choose"),
	)
	s.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	s.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	s.keyMap.Close = CloseKey
	return s
}

// Cursor returns the cursor position relative to the dialog.
func (s *Session) Cursor() *tea.Cursor {
	return s.input.Cursor()
}

// SetSize sets the size of the dialog.
func (s *Session) SetSize(width, height int) {
	s.width = width
	s.height = height
	innerWidth := width - s.com.Styles.Dialog.View.GetHorizontalFrameSize()
	s.input.SetWidth(innerWidth - s.com.Styles.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	s.list.SetSize(innerWidth, height-6) // (1) title + (3) input + (1) padding + (1) help
	s.help.SetWidth(width)
}

// SelectedItem returns the currently selected item. It may be nil if no item
// is selected.
func (s *Session) SelectedItem() list.Item {
	return s.list.SelectedItem()
}

// ID implements Dialog.
func (s *Session) ID() string {
	return SessionDialogID
}

// Update implements Dialog.
func (s *Session) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Previous):
			s.list.Focus()
			s.list.SelectPrev()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Next):
			s.list.Focus()
			s.list.SelectNext()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Select):
			if item := s.list.SelectedItem(); item != nil {
				sessionItem := item.(*SessionItem)
				return SessionSelectCmd(sessionItem.Session)
			}
		default:
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			s.list.SetFilter(s.input.Value())
			return cmd
		}
	}
	return nil
}

// View implements [Dialog].
func (s *Session) View() string {
	titleStyle := s.com.Styles.Dialog.Title
	helpStyle := s.com.Styles.Dialog.HelpView
	dialogStyle := s.com.Styles.Dialog.View.Width(s.width)
	inputStyle := s.com.Styles.Dialog.InputPrompt
	helpStyle = helpStyle.Width(s.width - dialogStyle.GetHorizontalFrameSize())
	listContent := s.list.Render()
	if nlines := lipgloss.Height(listContent); nlines < s.list.Height() {
		// pad the list content to avoid jumping when navigating
		listContent += strings.Repeat("\n", max(0, s.list.Height()-nlines))
	}

	content := strings.Join([]string{
		titleStyle.Render(
			common.DialogTitle(
				s.com.Styles,
				"Switch Session",
				max(0, s.width-
					dialogStyle.GetHorizontalFrameSize()-
					titleStyle.GetHorizontalFrameSize()))),
		"",
		inputStyle.Render(s.input.View()),
		"",
		listContent,
		"",
		helpStyle.Render(s.help.View(s)),
	}, "\n")

	return dialogStyle.Render(content)
}

// ShortHelp implements [help.KeyMap].
func (s *Session) ShortHelp() []key.Binding {
	updown := key.NewBinding(
		key.WithKeys("down", "up"),
		key.WithHelp("↑↓", "choose"),
	)
	return []key.Binding{
		updown,
		s.keyMap.Select,
		s.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (s *Session) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		s.keyMap.Select,
		s.keyMap.Next,
		s.keyMap.Previous,
		s.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

// SessionSelectCmd creates a command that sends a SessionSelectMsg.
func SessionSelectCmd(s session.Session) tea.Cmd {
	return func() tea.Msg {
		return SessionSelectedMsg{Session: s}
	}
}
