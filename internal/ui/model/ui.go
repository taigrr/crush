package model

import (
	"image"
	"math/rand"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/v2/help"
	"github.com/charmbracelet/bubbles/v2/key"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// uiState represents the current focus state of the UI.
type uiState uint8

// Possible uiState values.
const (
	uiChat uiState = iota
	uiEdit
)

// UI represents the main user interface model.
type UI struct {
	app *app.App
	com *common.Common

	state uiState

	keyMap KeyMap

	chat   *ChatModel
	editor *EditorModel
	dialog *dialog.Overlay
	help   help.Model

	layout layout

	// sendProgressBar instructs the TUI to send progress bar updates to the
	// terminal.
	sendProgressBar bool

	// QueryVersion instructs the TUI to query for the terminal version when it
	// starts.
	QueryVersion bool
}

// New creates a new instance of the [UI] model.
func New(com *common.Common, app *app.App) *UI {
	return &UI{
		app:    app,
		com:    com,
		dialog: dialog.NewOverlay(),
		keyMap: DefaultKeyMap(),
		editor: NewEditorModel(com, app),
		help:   help.New(),
	}
}

// Init initializes the UI model.
func (m *UI) Init() tea.Cmd {
	if m.QueryVersion {
		return tea.RequestTerminalVersion
	}

	return nil
}

// Update handles updates to the UI model.
func (m *UI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.EnvMsg:
		// Is this Windows Terminal?
		if !m.sendProgressBar {
			m.sendProgressBar = slices.Contains(msg, "WT_SESSION")
		}
	case tea.TerminalVersionMsg:
		termVersion := strings.ToLower(string(msg))
		// Only enable progress bar for the following terminals.
		if !m.sendProgressBar {
			m.sendProgressBar = strings.Contains(termVersion, "ghostty")
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.updateLayout(msg.Width, msg.Height)
		m.editor.SetSize(m.layout.editor.Dx(), m.layout.editor.Dy())
		m.help.Width = m.layout.help.Dx()
	case tea.KeyPressMsg:
		if m.dialog.HasDialogs() {
			m.updateDialogs(msg, &cmds)
		} else {
			switch {
			case key.Matches(msg, m.keyMap.Tab):
				if m.state == uiChat {
					m.state = uiEdit
					cmds = append(cmds, m.editor.Focus())
				} else {
					m.state = uiChat
					cmds = append(cmds, m.editor.Blur())
				}
			case key.Matches(msg, m.keyMap.Help):
				m.help.ShowAll = !m.help.ShowAll
				m.updateLayout(m.layout.area.Dx(), m.layout.area.Dy())
			case key.Matches(msg, m.keyMap.Quit):
				if !m.dialog.ContainsDialog(dialog.QuitDialogID) {
					m.dialog.AddDialog(dialog.NewQuit(m.com))
					return m, nil
				}
			default:
				m.updateFocused(msg, &cmds)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the UI model's view.
func (m *UI) View() tea.View {
	var v tea.View
	v.AltScreen = true

	layers := []*lipgloss.Layer{}

	// Determine the help key map based on focus
	helpKeyMap := m.focusedKeyMap()

	// The screen areas we're working with
	area := m.layout.area
	chatRect := m.layout.chat
	sideRect := m.layout.sidebar
	editRect := m.layout.editor
	helpRect := m.layout.help

	if m.dialog.HasDialogs() {
		if dialogView := m.dialog.View(); dialogView != "" {
			// If the dialog has its own help, use that instead
			if len(m.dialog.FullHelp()) > 0 || len(m.dialog.ShortHelp()) > 0 {
				helpKeyMap = m.dialog
			}

			dialogWidth, dialogHeight := lipgloss.Width(dialogView), lipgloss.Height(dialogView)
			dialogArea := common.CenterRect(area, dialogWidth, dialogHeight)
			layers = append(layers,
				lipgloss.NewLayer(dialogView).
					X(dialogArea.Min.X).
					Y(dialogArea.Min.Y).
					Z(99),
			)
		}
	}

	if m.state == uiEdit && m.editor.Focused() {
		cur := m.editor.Cursor()
		cur.X++ // Adjust for app margins
		cur.Y += editRect.Min.Y
		v.Cursor = cur
	}

	mainLayer := lipgloss.NewLayer("").X(area.Min.X).Y(area.Min.Y).
		Width(area.Dx()).Height(area.Dy()).
		AddLayers(
			lipgloss.NewLayer(
				lipgloss.NewStyle().Width(chatRect.Dx()).
					Height(chatRect.Dy()).
					Background(lipgloss.ANSIColor(rand.Intn(256))).
					Render(" Main View "),
			).X(chatRect.Min.X).Y(chatRect.Min.Y),
			lipgloss.NewLayer(
				lipgloss.NewStyle().Width(sideRect.Dx()).
					Height(sideRect.Dy()).
					Background(lipgloss.ANSIColor(rand.Intn(256))).
					Render(" Side View "),
			).X(sideRect.Min.X).Y(sideRect.Min.Y),
			lipgloss.NewLayer(m.editor.View()).
				X(editRect.Min.X).Y(editRect.Min.Y),
			lipgloss.NewLayer(m.help.View(helpKeyMap)).
				X(helpRect.Min.X).Y(helpRect.Min.Y),
		)

	layers = append(layers, mainLayer)

	v.Layer = lipgloss.NewCanvas(layers...)
	if m.sendProgressBar && m.app != nil && m.app.AgentCoordinator != nil && m.app.AgentCoordinator.IsBusy() {
		// HACK: use a random percentage to prevent ghostty from hiding it
		// after a timeout.
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarIndeterminate, rand.Intn(100))
	}

	return v
}

func (m *UI) focusedKeyMap() help.KeyMap {
	if m.state == uiChat {
		return m.chat
	}
	return m.editor
}

// updateDialogs updates the dialog overlay with the given message and appends
// any resulting commands to the cmds slice.
func (m *UI) updateDialogs(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	updatedDialog, cmd := m.dialog.Update(msg)
	m.dialog = updatedDialog
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
}

// updateFocused updates the focused model (chat or editor) with the given message
// and appends any resulting commands to the cmds slice.
func (m *UI) updateFocused(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	switch m.state {
	case uiChat:
		m.updateChat(msg, cmds)
	case uiEdit:
		m.updateEditor(msg, cmds)
	}
}

// updateChat updates the chat model with the given message and appends any
// resulting commands to the cmds slice.
func (m *UI) updateChat(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	updatedChat, cmd := m.chat.Update(msg)
	m.chat = updatedChat
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
}

// updateEditor updates the editor model with the given message and appends any
// resulting commands to the cmds slice.
func (m *UI) updateEditor(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	updatedEditor, cmd := m.editor.Update(msg)
	m.editor = updatedEditor
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
}

// updateLayout updates the layout based on the given terminal width and
// height given in cells.
func (m *UI) updateLayout(w, h int) {
	// The screen area we're working with
	area := image.Rect(0, 0, w, h)
	helpKeyMap := m.focusedKeyMap()
	helpHeight := 1
	if m.dialog.HasDialogs() && len(m.dialog.FullHelp()) > 0 && len(m.dialog.ShortHelp()) > 0 {
		helpKeyMap = m.dialog
	}
	if m.help.ShowAll {
		for _, row := range helpKeyMap.FullHelp() {
			helpHeight = max(helpHeight, len(row))
		}
	}

	// Add app margins
	mainRect := area
	mainRect.Min.X += 1
	mainRect.Min.Y += 1
	mainRect.Max.X -= 1
	mainRect.Max.Y -= 1

	mainRect, helpRect := uv.SplitVertical(mainRect, uv.Fixed(mainRect.Dy()-helpHeight))
	chatRect, sideRect := uv.SplitHorizontal(mainRect, uv.Fixed(mainRect.Dx()-40))
	chatRect, editRect := uv.SplitVertical(chatRect, uv.Fixed(mainRect.Dy()-5))

	// Add 1 line margin bottom of chatRect
	chatRect, _ = uv.SplitVertical(chatRect, uv.Fixed(chatRect.Dy()-1))
	// Add 1 line margin bottom of editRect
	editRect, _ = uv.SplitVertical(editRect, uv.Fixed(editRect.Dy()-1))

	m.layout = layout{
		area:    area,
		main:    mainRect,
		chat:    chatRect,
		editor:  editRect,
		sidebar: sideRect,
		help:    helpRect,
	}
}

// layout defines the positioning of UI elements.
type layout struct {
	// area is the overall available area.
	area uv.Rectangle

	// main is the main area excluding help.
	main uv.Rectangle

	// chat is the area for the chat pane.
	chat uv.Rectangle

	// editor is the area for the editor pane.
	editor uv.Rectangle

	// sidebar is the area for the sidebar.
	sidebar uv.Rectangle

	// help is the area for the help view.
	help uv.Rectangle
}
