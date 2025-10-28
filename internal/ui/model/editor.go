package model

import (
	"math/rand"

	"github.com/charmbracelet/bubbles/v2/key"
	"github.com/charmbracelet/bubbles/v2/textarea"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/ui/common"
)

type EditorKeyMap struct {
	AddFile     key.Binding
	SendMessage key.Binding
	OpenEditor  key.Binding
	Newline     key.Binding

	// Attachments key maps
	AttachmentDeleteMode key.Binding
	Escape               key.Binding
	DeleteAllAttachments key.Binding
}

func DefaultEditorKeyMap() EditorKeyMap {
	return EditorKeyMap{
		AddFile: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "add file"),
		),
		SendMessage: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "send"),
		),
		OpenEditor: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "open editor"),
		),
		Newline: key.NewBinding(
			key.WithKeys("shift+enter", "ctrl+j"),
			// "ctrl+j" is a common keybinding for newline in many editors. If
			// the terminal supports "shift+enter", we substitute the help text
			// to reflect that.
			key.WithHelp("ctrl+j", "newline"),
		),
		AttachmentDeleteMode: key.NewBinding(
			key.WithKeys("ctrl+r"),
			key.WithHelp("ctrl+r+{i}", "delete attachment at index i"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc", "alt+esc"),
			key.WithHelp("esc", "cancel delete mode"),
		),
		DeleteAllAttachments: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("ctrl+r+r", "delete all attachments"),
		),
	}
}

// EditorModel represents the editor UI model.
type EditorModel struct {
	com *common.Common
	app *app.App

	keyMap   EditorKeyMap
	textarea *textarea.Model

	attachments []any // TODO: Implement attachments

	readyPlaceholder   string
	workingPlaceholder string
}

// NewEditorModel creates a new instance of EditorModel.
func NewEditorModel(com *common.Common, app *app.App) *EditorModel {
	ta := textarea.New()
	ta.SetStyles(com.Styles.TextArea)
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.Focus()
	e := &EditorModel{
		com:      com,
		app:      app,
		keyMap:   DefaultEditorKeyMap(),
		textarea: ta,
	}

	e.setEditorPrompt()
	e.randomizePlaceholders()
	e.textarea.Placeholder = e.readyPlaceholder

	return e
}

// Init initializes the editor model.
func (m *EditorModel) Init() tea.Cmd {
	return nil
}

// Update handles updates to the editor model.
func (m *EditorModel) Update(msg tea.Msg) (*EditorModel, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	// Textarea placeholder logic
	if m.app.AgentCoordinator != nil && m.app.AgentCoordinator.IsBusy() {
		m.textarea.Placeholder = m.workingPlaceholder
	} else {
		m.textarea.Placeholder = m.readyPlaceholder
	}
	if m.app.Permissions.SkipRequests() {
		m.textarea.Placeholder = "Yolo mode!"
	}

	// TODO: Add attachments

	return m, tea.Batch(cmds...)
}

// View renders the editor model.
func (m *EditorModel) View() string {
	return m.textarea.View()
}

// ShortHelp returns the short help view for the editor model.
func (m *EditorModel) ShortHelp() []key.Binding {
	k := m.keyMap
	binds := []key.Binding{
		k.AddFile,
		k.SendMessage,
		k.OpenEditor,
		k.Newline,
	}

	if len(m.attachments) > 0 {
		binds = append(binds,
			k.AttachmentDeleteMode,
			k.DeleteAllAttachments,
			k.Escape,
		)
	}

	return binds
}

// FullHelp returns the full help view for the editor model.
func (m *EditorModel) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		m.ShortHelp(),
	}
}

// Cursor returns the relative cursor position of the editor.
func (m *EditorModel) Cursor() *tea.Cursor {
	return m.textarea.Cursor()
}

// Blur implements Container.
func (m *EditorModel) Blur() tea.Cmd {
	m.textarea.Blur()
	return nil
}

// Focus implements Container.
func (m *EditorModel) Focus() tea.Cmd {
	return m.textarea.Focus()
}

// Focused returns whether the editor is focused.
func (m *EditorModel) Focused() bool {
	return m.textarea.Focused()
}

// SetSize sets the size of the editor.
func (m *EditorModel) SetSize(width, height int) {
	m.textarea.SetWidth(width)
	m.textarea.SetHeight(height)
}

func (m *EditorModel) setEditorPrompt() {
	if m.app.Permissions.SkipRequests() {
		m.textarea.SetPromptFunc(4, m.yoloPromptFunc)
		return
	}
	m.textarea.SetPromptFunc(4, m.normalPromptFunc)
}

func (m *EditorModel) normalPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		return "  > "
	}
	if info.Focused {
		return t.EditorPromptNormalFocused.Render()
	}
	return t.EditorPromptNormalBlurred.Render()
}

func (m *EditorModel) yoloPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		if info.Focused {
			return t.EditorPromptYoloIconFocused.Render()
		} else {
			return t.EditorPromptYoloIconBlurred.Render()
		}
	}
	if info.Focused {
		return t.EditorPromptYoloDotsFocused.Render()
	}
	return t.EditorPromptYoloDotsBlurred.Render()
}

var readyPlaceholders = [...]string{
	"Ready!",
	"Ready...",
	"Ready?",
	"Ready for instructions",
}

var workingPlaceholders = [...]string{
	"Working!",
	"Working...",
	"Brrrrr...",
	"Prrrrrrrr...",
	"Processing...",
	"Thinking...",
}

func (m *EditorModel) randomizePlaceholders() {
	m.workingPlaceholder = workingPlaceholders[rand.Intn(len(workingPlaceholders))]
	m.readyPlaceholder = readyPlaceholders[rand.Intn(len(readyPlaceholders))]
}
