package model

import (
	"github.com/charmbracelet/bubbles/v2/key"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/ui/common"
)

// ChatKeyMap defines key bindings for the chat model.
type ChatKeyMap struct {
	NewSession    key.Binding
	AddAttachment key.Binding
	Cancel        key.Binding
	Tab           key.Binding
	Details       key.Binding
}

// DefaultChatKeyMap returns the default key bindings for the chat model.
func DefaultChatKeyMap() ChatKeyMap {
	return ChatKeyMap{
		NewSession: key.NewBinding(
			key.WithKeys("ctrl+n"),
			key.WithHelp("ctrl+n", "new session"),
		),
		AddAttachment: key.NewBinding(
			key.WithKeys("ctrl+f"),
			key.WithHelp("ctrl+f", "add attachment"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc", "alt+esc"),
			key.WithHelp("esc", "cancel"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "change focus"),
		),
		Details: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "toggle details"),
		),
	}
}

// ChatModel represents the chat UI model.
type ChatModel struct {
	app *app.App
	com *common.Common

	keyMap ChatKeyMap
}

// NewChatModel creates a new instance of ChatModel.
func NewChatModel(com *common.Common, app *app.App) *ChatModel {
	return &ChatModel{
		app:    app,
		com:    com,
		keyMap: DefaultChatKeyMap(),
	}
}

// Init initializes the chat model.
func (m *ChatModel) Init() tea.Cmd {
	return nil
}

// Update handles incoming messages and updates the chat model state.
func (m *ChatModel) Update(msg tea.Msg) (*ChatModel, tea.Cmd) {
	// Handle messages here
	return m, nil
}

// View renders the chat model's view.
func (m *ChatModel) View() string {
	return "Chat Model View"
}

// ShortHelp returns a brief help view for the chat model.
func (m *ChatModel) ShortHelp() []key.Binding {
	return nil
}

// FullHelp returns a detailed help view for the chat model.
func (m *ChatModel) FullHelp() [][]key.Binding {
	return nil
}
