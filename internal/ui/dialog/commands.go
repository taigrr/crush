package dialog

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/uicmd"
	"github.com/charmbracelet/crush/internal/uiutil"
)

// CommandsID is the identifier for the commands dialog.
const CommandsID = "commands"

// SendMsg represents a message to send a chat message.
// TODO: Move to chat package?
type SendMsg struct {
	Text        string
	Attachments []message.Attachment
}

// Messages for commands
type (
	SwitchSessionsMsg      struct{}
	NewSessionsMsg         struct{}
	SwitchModelMsg         struct{}
	QuitMsg                struct{}
	OpenFilePickerMsg      struct{}
	ToggleHelpMsg          struct{}
	ToggleCompactModeMsg   struct{}
	ToggleThinkingMsg      struct{}
	OpenReasoningDialogMsg struct{}
	OpenExternalEditorMsg  struct{}
	ToggleYoloModeMsg      struct{}
	CompactMsg             struct {
		SessionID string
	}
)

// Commands represents a dialog that shows available commands.
type Commands struct {
	com    *common.Common
	keyMap struct {
		Select,
		Next,
		Previous,
		Tab,
		Close key.Binding
	}

	sessionID  string // can be empty for non-session-specific commands
	selected   uicmd.CommandType
	userCmds   []uicmd.Command
	mcpPrompts *csync.Slice[uicmd.Command]

	help          help.Model
	input         textinput.Model
	list          *list.FilterableList
	width, height int
}

var _ Dialog = (*Commands)(nil)

// NewCommands creates a new commands dialog.
func NewCommands(com *common.Common, sessionID string) (*Commands, error) {
	commands, err := uicmd.LoadCustomCommandsFromConfig(com.Config())
	if err != nil {
		return nil, err
	}

	mcpPrompts := csync.NewSlice[uicmd.Command]()
	mcpPrompts.SetSlice(uicmd.LoadMCPPrompts())

	c := &Commands{
		com:        com,
		userCmds:   commands,
		selected:   uicmd.SystemCommands,
		mcpPrompts: mcpPrompts,
		sessionID:  sessionID,
	}

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()

	c.help = help

	c.list = list.NewFilterableList()
	c.list.Focus()
	c.list.SetSelected(0)

	c.input = textinput.New()
	c.input.SetVirtualCursor(false)
	c.input.Placeholder = "Type to filter"
	c.input.SetStyles(com.Styles.TextInput)
	c.input.Focus()

	c.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	c.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	c.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	c.keyMap.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch selection"),
	)
	closeKey := CloseKey
	closeKey.SetHelp("esc", "cancel")
	c.keyMap.Close = closeKey

	// Set initial commands
	c.setCommandType(c.selected)

	return c, nil
}

// SetSize sets the size of the dialog.
func (c *Commands) SetSize(width, height int) {
	c.width = width
	c.height = height
	innerWidth := width - c.com.Styles.Dialog.View.GetHorizontalFrameSize()
	c.input.SetWidth(innerWidth - c.com.Styles.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	c.list.SetSize(innerWidth, height-6) // (1) title + (3) input + (1) padding + (1) help
	c.help.SetWidth(width)
}

// ID implements Dialog.
func (c *Commands) ID() string {
	return CommandsID
}

// Update implements Dialog.
func (c *Commands) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, c.keyMap.Previous):
			c.list.Focus()
			c.list.SelectPrev()
			c.list.ScrollToSelected()
		case key.Matches(msg, c.keyMap.Next):
			c.list.Focus()
			c.list.SelectNext()
			c.list.ScrollToSelected()
		case key.Matches(msg, c.keyMap.Select):
			if selectedItem := c.list.SelectedItem(); selectedItem != nil {
				if item, ok := selectedItem.(*CommandItem); ok && item != nil {
					return item.Cmd.Handler(item.Cmd) // Huh??
				}
			}
		case key.Matches(msg, c.keyMap.Tab):
			if len(c.userCmds) > 0 || c.mcpPrompts.Len() > 0 {
				c.selected = c.nextCommandType()
				c.setCommandType(c.selected)
			}
		default:
			var cmd tea.Cmd
			c.input, cmd = c.input.Update(msg)
			// Update the list filter
			c.list.SetFilter(c.input.Value())
			return cmd
		}
	}
	return nil
}

// ReloadMCPPrompts reloads the MCP prompts.
func (c *Commands) ReloadMCPPrompts() tea.Cmd {
	c.mcpPrompts.SetSlice(uicmd.LoadMCPPrompts())
	// If we're currently viewing MCP prompts, refresh the list
	if c.selected == uicmd.MCPPrompts {
		c.setCommandType(uicmd.MCPPrompts)
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (c *Commands) Cursor() *tea.Cursor {
	return c.input.Cursor()
}

// View implements [Dialog].
func (c *Commands) View() string {
	t := c.com.Styles
	selectedFn := func(t uicmd.CommandType) string {
		if t == c.selected {
			return "◉ " + t.String()
		}
		return "○ " + t.String()
	}

	parts := []string{
		selectedFn(uicmd.SystemCommands),
	}
	if len(c.userCmds) > 0 {
		parts = append(parts, selectedFn(uicmd.UserCommands))
	}
	if c.mcpPrompts.Len() > 0 {
		parts = append(parts, selectedFn(uicmd.MCPPrompts))
	}

	radio := strings.Join(parts, " ")
	radio = t.Dialog.Commands.CommandTypeSelector.Render(radio)
	if len(c.userCmds) > 0 || c.mcpPrompts.Len() > 0 {
		radio = " " + radio
	}

	titleStyle := t.Dialog.Title
	helpStyle := t.Dialog.HelpView
	dialogStyle := t.Dialog.View.Width(c.width)
	inputStyle := t.Dialog.InputPrompt
	helpStyle = helpStyle.Width(c.width - dialogStyle.GetHorizontalFrameSize())

	headerOffset := lipgloss.Width(radio) + titleStyle.GetHorizontalFrameSize() + dialogStyle.GetHorizontalFrameSize()
	header := common.DialogTitle(t, "Commands", c.width-headerOffset) + radio
	title := titleStyle.Render(header)
	help := helpStyle.Render(c.help.View(c))
	listContent := c.list.Render()
	if nlines := lipgloss.Height(listContent); nlines < c.list.Height() {
		// pad the list content to avoid jumping when navigating
		listContent += strings.Repeat("\n", max(0, c.list.Height()-nlines))
	}

	content := strings.Join([]string{
		title,
		"",
		inputStyle.Render(c.input.View()),
		"",
		c.list.Render(),
		"",
		help,
	}, "\n")

	return dialogStyle.Render(content)
}

// ShortHelp implements [help.KeyMap].
func (c *Commands) ShortHelp() []key.Binding {
	upDown := key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	return []key.Binding{
		c.keyMap.Tab,
		upDown,
		c.keyMap.Select,
		c.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (c *Commands) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{c.keyMap.Select, c.keyMap.Next, c.keyMap.Previous, c.keyMap.Tab},
		{c.keyMap.Close},
	}
}

func (c *Commands) nextCommandType() uicmd.CommandType {
	switch c.selected {
	case uicmd.SystemCommands:
		if len(c.userCmds) > 0 {
			return uicmd.UserCommands
		}
		if c.mcpPrompts.Len() > 0 {
			return uicmd.MCPPrompts
		}
		fallthrough
	case uicmd.UserCommands:
		if c.mcpPrompts.Len() > 0 {
			return uicmd.MCPPrompts
		}
		fallthrough
	case uicmd.MCPPrompts:
		return uicmd.SystemCommands
	default:
		return uicmd.SystemCommands
	}
}

func (c *Commands) setCommandType(commandType uicmd.CommandType) {
	c.selected = commandType

	var commands []uicmd.Command
	switch c.selected {
	case uicmd.SystemCommands:
		commands = c.defaultCommands()
	case uicmd.UserCommands:
		commands = c.userCmds
	case uicmd.MCPPrompts:
		commands = slices.Collect(c.mcpPrompts.Seq())
	}

	commandItems := []list.FilterableItem{}
	for _, cmd := range commands {
		commandItems = append(commandItems, NewCommandItem(c.com.Styles, cmd))
	}

	c.list.SetItems(commandItems...)
	// Reset selection and filter
	c.list.SetSelected(0)
	c.input.SetValue("")
}

// TODO: Rethink this
func (c *Commands) defaultCommands() []uicmd.Command {
	commands := []uicmd.Command{
		{
			ID:          "new_session",
			Title:       "New Session",
			Description: "start a new session",
			Shortcut:    "ctrl+n",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(NewSessionsMsg{})
			},
		},
		{
			ID:          "switch_session",
			Title:       "Switch Session",
			Description: "Switch to a different session",
			Shortcut:    "ctrl+s",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(SwitchSessionsMsg{})
			},
		},
		{
			ID:          "switch_model",
			Title:       "Switch Model",
			Description: "Switch to a different model",
			Shortcut:    "ctrl+l",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(SwitchModelMsg{})
			},
		},
	}

	// Only show compact command if there's an active session
	if c.sessionID != "" {
		commands = append(commands, uicmd.Command{
			ID:          "Summarize",
			Title:       "Summarize Session",
			Description: "Summarize the current session and create a new one with the summary",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(CompactMsg{
					SessionID: c.sessionID,
				})
			},
		})
	}

	// Add reasoning toggle for models that support it
	cfg := c.com.Config()
	if agentCfg, ok := cfg.Agents[config.AgentCoder]; ok {
		providerCfg := cfg.GetProviderForModel(agentCfg.Model)
		model := cfg.GetModelByType(agentCfg.Model)
		if providerCfg != nil && model != nil && model.CanReason {
			selectedModel := cfg.Models[agentCfg.Model]

			// Anthropic models: thinking toggle
			if providerCfg.Type == catwalk.TypeAnthropic {
				status := "Enable"
				if selectedModel.Think {
					status = "Disable"
				}
				commands = append(commands, uicmd.Command{
					ID:          "toggle_thinking",
					Title:       status + " Thinking Mode",
					Description: "Toggle model thinking for reasoning-capable models",
					Handler: func(cmd uicmd.Command) tea.Cmd {
						return uiutil.CmdHandler(ToggleThinkingMsg{})
					},
				})
			}

			// OpenAI models: reasoning effort dialog
			if len(model.ReasoningLevels) > 0 {
				commands = append(commands, uicmd.Command{
					ID:          "select_reasoning_effort",
					Title:       "Select Reasoning Effort",
					Description: "Choose reasoning effort level (low/medium/high)",
					Handler: func(cmd uicmd.Command) tea.Cmd {
						return uiutil.CmdHandler(OpenReasoningDialogMsg{})
					},
				})
			}
		}
	}
	// Only show toggle compact mode command if window width is larger than compact breakpoint (90)
	// TODO: Get. Rid. Of. Magic. Numbers!
	if c.width > 120 && c.sessionID != "" {
		commands = append(commands, uicmd.Command{
			ID:          "toggle_sidebar",
			Title:       "Toggle Sidebar",
			Description: "Toggle between compact and normal layout",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(ToggleCompactModeMsg{})
			},
		})
	}
	if c.sessionID != "" {
		cfg := c.com.Config()
		agentCfg := cfg.Agents[config.AgentCoder]
		model := cfg.GetModelByType(agentCfg.Model)
		if model.SupportsImages {
			commands = append(commands, uicmd.Command{
				ID:          "file_picker",
				Title:       "Open File Picker",
				Shortcut:    "ctrl+f",
				Description: "Open file picker",
				Handler: func(cmd uicmd.Command) tea.Cmd {
					return uiutil.CmdHandler(OpenFilePickerMsg{})
				},
			})
		}
	}

	// Add external editor command if $EDITOR is available
	// TODO: Use [tea.EnvMsg] to get environment variable instead of os.Getenv
	if os.Getenv("EDITOR") != "" {
		commands = append(commands, uicmd.Command{
			ID:          "open_external_editor",
			Title:       "Open External Editor",
			Shortcut:    "ctrl+o",
			Description: "Open external editor to compose message",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(OpenExternalEditorMsg{})
			},
		})
	}

	return append(commands, []uicmd.Command{
		{
			ID:          "toggle_yolo",
			Title:       "Toggle Yolo Mode",
			Description: "Toggle yolo mode",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(ToggleYoloModeMsg{})
			},
		},
		{
			ID:          "toggle_help",
			Title:       "Toggle Help",
			Shortcut:    "ctrl+g",
			Description: "Toggle help",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(ToggleHelpMsg{})
			},
		},
		{
			ID:          "init",
			Title:       "Initialize Project",
			Description: fmt.Sprintf("Create/Update the %s memory file", config.Get().Options.InitializeAs),
			Handler: func(cmd uicmd.Command) tea.Cmd {
				initPrompt, err := agent.InitializePrompt(*c.com.Config())
				if err != nil {
					return uiutil.ReportError(err)
				}
				return uiutil.CmdHandler(SendMsg{
					Text: initPrompt,
				})
			},
		},
		{
			ID:          "quit",
			Title:       "Quit",
			Description: "Quit",
			Shortcut:    "ctrl+c",
			Handler: func(cmd uicmd.Command) tea.Cmd {
				return uiutil.CmdHandler(QuitMsg{})
			},
		},
	}...)
}
