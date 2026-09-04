package model

import "charm.land/bubbles/v2/key"

func (m *UI) ShortHelp() []key.Binding {
	var binds []key.Binding
	k := &m.keyMap
	tab := k.Tab
	commands := k.Commands
	if m.focus == uiFocusEditor && m.textarea.Value() == "" {
		commands.SetHelp("/ or ctrl+p", "commands")
	}

	switch m.state {
	case uiInitialize:
		binds = append(binds, k.Quit)
	case uiChat:
		// Show cancel binding if agent is busy.
		if m.isAgentBusy() {
			cancelBinding := k.Chat.Cancel
			if m.isCanceling {
				cancelBinding.SetHelp("esc", "press again to cancel")
			} else if m.pillsExpanded && m.com.Workspace.AgentQueuedPrompts(m.session.ID) > 0 {
				cancelBinding.SetHelp("esc", "clear queue")
			}
			binds = append(binds, cancelBinding)
			if m.hasRunningBash() {
				binds = append(binds, k.Chat.BackgroundTool)
			}
		}

		if m.focus == uiFocusEditor {
			tab.SetHelp("tab", "focus chat")
		} else {
			tab.SetHelp("tab", "focus editor")
		}

		binds = append(
			binds,
			tab,
			commands,
			k.Models,
		)

		switch m.focus {
		case uiFocusEditor:
			binds = append(
				binds,
				k.Editor.Newline,
			)
			if m.isAgentBusy() {
				binds = append(binds, k.Editor.Steer)
			}
		case uiFocusMain:
			binds = append(
				binds,
				k.Chat.UpDown,
				k.Chat.UpDownOneItem,
				k.Chat.PrevNextUserMessage,
				k.Chat.PageUp,
				k.Chat.PageDown,
				k.Chat.Copy,
			)
			if m.pillsExpanded && hasIncompleteTodos(m.session.Todos) && m.promptQueue > 0 {
				binds = append(binds, k.Chat.PillLeft)
			}
			if m.rightSidebarScrollable && !m.isCompact && !m.chatFullscreen {
				binds = append(binds, k.Chat.FocusRightSidebar)
			}
		case uiFocusRightSidebar:
			binds = append(
				binds,
				k.Chat.UpDown,
				k.Chat.FocusChat,
			)
		case uiFocusLeftSidebar:
			binds = append(
				binds,
				k.Sidebar.UpDown,
				k.Sidebar.Open,
				k.Sidebar.Resize,
				k.Sidebar.Close,
			)
		}
	default:
		// TODO: other states
		// if m.session == nil {
		// no session selected
		binds = append(
			binds,
			commands,
			k.Models,
			k.Editor.Newline,
		)
	}

	binds = append(
		binds,
		k.Quit,
		k.Help,
	)

	return binds
}

// FullHelp implements [help.KeyMap].
func (m *UI) FullHelp() [][]key.Binding {
	var binds [][]key.Binding
	k := &m.keyMap
	help := k.Help
	help.SetHelp("ctrl+g", "less")
	hasAttachments := len(m.attachments.List()) > 0
	hasSession := m.hasSession()
	commands := k.Commands
	if m.focus == uiFocusEditor && m.textarea.Value() == "" {
		commands.SetHelp("/ or ctrl+p", "commands")
	}

	// When the left session navigator is focused, surface its vim-style
	// navigation and multi-select/bulk-archive keys alongside the global
	// keys that still work while it is open.
	if m.leftSidebarVisible && m.focus == uiFocusLeftSidebar {
		binds = append(
			binds,
			[]key.Binding{
				commands,
				k.Sessions,
			},
			[]key.Binding{
				k.Chat.UpDown,
				k.Chat.Home,
				k.Chat.End,
				k.SessionSidebar.PrevSection,
				k.SessionSidebar.NextSection,
			},
			[]key.Binding{
				k.SessionSidebar.VisualSelect,
				k.SessionSidebar.ToggleSelect,
				k.SessionSidebar.ArchiveSelect,
				k.SessionSidebar.MarkRead,
				k.SessionSidebar.Favorite,
				k.SessionSidebar.Inbox,
				k.SessionSidebar.Search,
				k.SessionSidebar.Pin,
			},
			[]key.Binding{
				help,
				k.Quit,
			},
		)
		return binds
	}

	switch m.state {
	case uiInitialize:
		binds = append(binds,
			[]key.Binding{
				k.Quit,
			})
	case uiChat:
		// Show cancel binding if agent is busy.
		if m.isAgentBusy() {
			cancelBinding := k.Chat.Cancel
			if m.isCanceling {
				cancelBinding.SetHelp("esc", "press again to cancel")
			} else if m.pillsExpanded && m.com.Workspace.AgentQueuedPrompts(m.session.ID) > 0 {
				cancelBinding.SetHelp("esc", "clear queue")
			}
			busyBinds := []key.Binding{cancelBinding}
			if m.hasRunningBash() {
				busyBinds = append(busyBinds, k.Chat.BackgroundTool)
			}
			binds = append(binds, busyBinds)
		}

		mainBinds := []key.Binding{}
		tab := k.Tab
		if m.focus == uiFocusEditor {
			tab.SetHelp("tab", "focus chat")
		} else {
			tab.SetHelp("tab", "focus editor")
		}

		mainBinds = append(
			mainBinds,
			tab,
			commands,
			k.Models,
			k.Sessions,
			k.PinSessions,
			k.Search,
			k.Milestones,
			k.ToggleYolo,
			k.Fullscreen,
		)
		if hasSession {
			mainBinds = append(mainBinds, k.Chat.NewSession, k.ArchiveSession)
		}

		binds = append(binds, mainBinds)

		switch m.focus {
		case uiFocusEditor:
			editorBinds := []key.Binding{
				k.Editor.Newline,
				k.Editor.Steer,
				k.Editor.MentionFile,
				k.Editor.Commands,
				k.Editor.OpenEditor,
				k.Editor.Stash,
			}
			if m.currentModelSupportsImages() {
				editorBinds = append(editorBinds, k.Editor.AddImage, k.Editor.PasteImage)
			}
			binds = append(binds, editorBinds)
			if hasAttachments {
				binds = append(
					binds,
					[]key.Binding{
						k.Editor.AttachmentDeleteMode,
						k.Editor.DeleteAllAttachments,
						k.Editor.Escape,
					},
				)
			}
		case uiFocusMain:
			binds = append(
				binds,
				[]key.Binding{
					k.Chat.UpDown,
					k.Chat.UpDownOneItem,
					k.Chat.PrevNextUserMessage,
					k.Chat.PageUp,
					k.Chat.PageDown,
				},
				[]key.Binding{
					k.Chat.HalfPageUp,
					k.Chat.HalfPageDown,
					k.Chat.Home,
					k.Chat.End,
				},
				[]key.Binding{
					k.Chat.Copy,
					k.Chat.ClearHighlight,
				},
			)
			if m.pillsExpanded && hasIncompleteTodos(m.session.Todos) && m.promptQueue > 0 {
				binds = append(binds, []key.Binding{k.Chat.PillLeft})
			}
		case uiFocusRightSidebar:
			binds = append(
				binds,
				[]key.Binding{k.Chat.UpDown},
				[]key.Binding{k.Chat.FocusChat},
			)
		case uiFocusLeftSidebar:
			binds = append(
				binds,
				[]key.Binding{
					k.Sidebar.UpDown,
					k.Sidebar.Open,
				},
				[]key.Binding{
					k.Sidebar.Resize,
					k.Sidebar.Close,
				},
			)
		}
	default:
		if m.session == nil {
			// no session selected
			binds = append(
				binds,
				[]key.Binding{
					commands,
					k.Models,
					k.Sessions,
					k.ToggleYolo,
				},
			)
			editorBinds := []key.Binding{
				k.Editor.Newline,
				k.Editor.MentionFile,
				k.Editor.Commands,
				k.Editor.OpenEditor,
				k.Editor.Stash,
			}
			if m.currentModelSupportsImages() {
				editorBinds = append(editorBinds, k.Editor.AddImage, k.Editor.PasteImage)
			}
			binds = append(binds, editorBinds)
			if hasAttachments {
				binds = append(
					binds,
					[]key.Binding{
						k.Editor.AttachmentDeleteMode,
						k.Editor.DeleteAllAttachments,
						k.Editor.Escape,
					},
				)
			}
		}
	}

	binds = append(
		binds,
		[]key.Binding{
			help,
			k.Quit,
		},
	)

	return binds
}
