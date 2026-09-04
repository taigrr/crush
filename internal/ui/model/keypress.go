package model

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/completions"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
)

func (m *UI) handleKeyPressMsg(msg tea.KeyPressMsg) tea.Cmd {
	var cmds []tea.Cmd

	handleGlobalKeys := func(msg tea.KeyPressMsg) bool {
		switch {
		case key.Matches(msg, m.keyMap.Help):
			m.status.ToggleHelp()
			m.updateLayoutAndSize()
			return true
		case key.Matches(msg, m.keyMap.Commands):
			if cmd := m.openCommandsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Models):
			if cmd := m.openModelsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Sessions):
			if cmd := m.toggleLeftSidebar(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.PinSessions):
			if cmd := m.toggleLeftSidebarPin(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Search):
			if cmd := m.openSearchPaletteDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Fullscreen):
			// In compact mode there is no persistent right sidebar for
			// chatFullscreen to hide (the compact layout branch never
			// reads it), which made ctrl+f silently do nothing whenever
			// compact mode was forced via the "Toggle Sidebar" command.
			// Delegate to the compact mode's own panel toggle (ctrl+d)
			// instead so ctrl+f always does something.
			if m.isCompact {
				m.detailsOpen = !m.detailsOpen
				m.updateLayoutAndSize()
				return true
			}
			// Fullscreen chat: hide both the left navigator and the right
			// info sidebar. Leaving fullscreen restores the right sidebar
			// and, when pinned, the left navigator; an unpinned navigator
			// stays closed (reopen with ctrl+s).
			m.chatFullscreen = !m.chatFullscreen
			if m.chatFullscreen && m.leftSidebarVisible {
				m.leftSidebarVisible = false
				if m.focus == uiFocusLeftSidebar {
					m.setFocusAfterSidebarClose()
				}
				if cmd := m.cancelPreview(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			} else if !m.chatFullscreen && m.leftSidebarPinned && !m.leftSidebarVisible {
				m.leftSidebarVisible = true
				cmds = append(cmds, m.loadWorkspaceOverviews())
			}
			m.updateLayoutAndSize()
			return true
		case key.Matches(msg, m.keyMap.Milestones):
			if m.hasSession() {
				if cmd := m.openMilestonesDialog(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return true
		case key.Matches(msg, m.keyMap.ArchiveSession):
			// ctrl+x archives the ACTIVE session. Suppress it while the left
			// navigator is focused: there, `a` archives the multi-selection
			// (which deliberately excludes the active session), so letting
			// ctrl+x archive the active one would be surprising.
			if m.focus == uiFocusLeftSidebar {
				return true
			}
			if m.hasSession() {
				if cmd := m.openArchiveConfirmDialog(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return true
		case key.Matches(msg, m.keyMap.ToggleYolo):
			yolo := !m.com.Workspace.PermissionSkipRequests()
			m.com.Workspace.PermissionSetSkipRequests(yolo)
			m.setEditorPrompt(yolo)
			status := "disabled"
			if yolo {
				status = "enabled"
			}
			cmds = append(cmds, util.ReportInfo("Yolo mode "+status))
			return true
		case key.Matches(msg, m.keyMap.Chat.Details) && m.isCompact:
			m.detailsOpen = !m.detailsOpen
			m.updateLayoutAndSize()
			return true
		case key.Matches(msg, m.keyMap.Chat.TogglePills):
			if m.state == uiChat && m.hasSession() {
				if cmd := m.togglePillsExpanded(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Chat.PillLeft):
			if m.state == uiChat && m.hasSession() && m.pillsExpanded && m.focus != uiFocusEditor {
				if cmd := m.switchPillSection(-1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Chat.PillRight):
			if m.state == uiChat && m.hasSession() && m.pillsExpanded && m.focus != uiFocusEditor {
				if cmd := m.switchPillSection(1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Suspend):
			// Suspend (Ctrl+Z) is always safe: the agent runs in the
			// separate server process, so backgrounding the TUI client
			// does not interrupt an in-flight turn. The client reconciles
			// any events missed while suspended from the event stream (and
			// the RunComplete backstop) on resume.
			cmds = append(cmds, tea.Suspend)
			return true
		case key.Matches(msg, m.keyMap.ToggleYolo):
			yolo := !m.com.Workspace.PermissionSkipRequests()
			m.com.Workspace.PermissionSetSkipRequests(yolo)
			m.setEditorPrompt(yolo)
			status := "disabled"
			if yolo {
				status = "enabled"
			}
			cmds = append(cmds, util.ReportInfo("Yolo mode "+status))
			return true
		}
		return false
	}

	if key.Matches(msg, m.keyMap.Quit) && !m.dialog.ContainsDialog(dialog.QuitID) {
		// Always handle quit keys first
		if cmd := m.openQuitDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}

		return tea.Batch(cmds...)
	}

	// Route all messages to dialog if one is open.
	if m.dialog.HasDialogs() {
		return m.handleDialogMsg(msg)
	}

	// When the left session navigator is focused, route navigation and
	// selection keys to it. Unconsumed keys (e.g. ctrl+s to toggle, ctrl+c
	// to quit) fall through to the global handlers below.
	if m.leftSidebarVisible && m.focus == uiFocusLeftSidebar {
		if cmd, consumed := m.handleLeftSidebarKey(msg); consumed {
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return tea.Batch(cmds...)
		}
	}

	// Handle cancel key when agent is busy.
	if key.Matches(msg, m.keyMap.Chat.Cancel) {
		// Cancel a running bang-mode shell command first, if any.
		if m.shellCancel != nil {
			m.shellCancel()
			m.shellCancel = nil
			return tea.Batch(cmds...)
		}
		if m.isAgentBusy() {
			if cmd := m.cancelAgent(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return tea.Batch(cmds...)
		}
	}

	// Background the running bash command. Only consumed while one is
	// actually in flight; otherwise the key keeps its usual meaning in
	// the focused component (word-backward in the editor).
	if key.Matches(msg, m.keyMap.Chat.BackgroundTool) {
		if cmd := m.backgroundRunningBash(); cmd != nil {
			cmds = append(cmds, cmd)
			return tea.Batch(cmds...)
		}
	}

	switch m.state {
	case uiOnboarding:
		return tea.Batch(cmds...)
	case uiInitialize:
		cmds = append(cmds, m.updateInitializeView(msg)...)
		return tea.Batch(cmds...)
	case uiChat, uiLanding:
		switch m.focus {
		case uiFocusEditor:
			// Handle completions if open. An argument popup with nothing
			// typed yet is a suggestion, not a selection: Enter there runs
			// the slash command as typed ("/model" alone shows the current
			// model) instead of inserting the first suggestion.
			if m.completionsOpen && m.completionsTrigger == argCompletionTrigger &&
				m.completionsQuery == "" && !m.completions.Navigated() &&
				key.Matches(msg, m.keyMap.Editor.SendMessage) {
				m.closeCompletions()
			}
			if m.completionsOpen {
				if msg, ok := m.completions.Update(msg); ok {
					switch msg := msg.(type) {
					case completions.SelectionMsg[completions.FileCompletionValue]:
						cmds = append(cmds, m.insertFileCompletion(msg.Value.Path))
						if !msg.KeepOpen {
							m.closeCompletions()
						}
					case completions.SelectionMsg[completions.ResourceCompletionValue]:
						cmds = append(cmds, m.insertMCPResourceCompletion(msg.Value))
						if !msg.KeepOpen {
							m.closeCompletions()
						}
					case completions.SelectionMsg[completions.CommandCompletionValue]:
						cmds = append(cmds, m.insertCommandCompletion(msg.Value.Name))
						if !msg.KeepOpen {
							m.closeCompletions()
							m.maybeOpenArgCompletions()
						}
					case completions.SelectionMsg[completions.ArgCompletionValue]:
						cmds = append(cmds, m.insertArgCompletion(msg.Value.Text))
						if !msg.KeepOpen {
							m.closeCompletions()
							if msg.Value.Continue {
								m.maybeOpenArgCompletions()
							}
						}
					case completions.ClosedMsg:
						m.completionsOpen = false
					}
					return tea.Batch(cmds...)
				}
			}

			if ok := m.attachments.Update(msg); ok {
				return tea.Batch(cmds...)
			}

			switch {
			case key.Matches(msg, m.keyMap.Editor.AddImage):
				if !m.currentModelSupportsImages() {
					break
				}
				if cmd := m.openFilesDialog(); cmd != nil {
					cmds = append(cmds, cmd)
				}

			case key.Matches(msg, m.keyMap.Editor.PasteImage):
				if !m.currentModelSupportsImages() {
					break
				}
				// Reserve the paste index on the Update goroutine;
				// pasteImageFromClipboard runs as a command off-loop.
				idx := m.pasteIdx()
				cmds = append(cmds, func() tea.Msg {
					return m.pasteImageFromClipboard(idx)
				})

			case key.Matches(msg, m.keyMap.Editor.SendMessage), key.Matches(msg, m.keyMap.Editor.Steer):
				steer := key.Matches(msg, m.keyMap.Editor.Steer)
				prevHeight := m.textarea.Height()
				value := m.textarea.Value()
				if before, ok := strings.CutSuffix(value, "\\"); ok {
					// If the last character is a backslash, remove it and add a newline.
					m.textarea.SetValue(before)
					if cmd := m.handleTextareaHeightChange(prevHeight); cmd != nil {
						cmds = append(cmds, cmd)
					}
					break
				}

				consumePrompt := func() {
					m.textarea.Reset()
					if cmd := m.handleTextareaHeightChange(prevHeight); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}

				value = strings.TrimSpace(value)
				if value == "exit" || value == "quit" {
					consumePrompt()
					return m.openQuitDialog()
				}

				if command, ok := strings.CutPrefix(value, "!"); ok && command != "" {
					consumePrompt()
					m.randomizePlaceholders()
					m.historyReset()
					return m.runShellCommand(command)
				}

				if cmd, handled, consume := m.dispatchSlash(value); handled {
					if consume {
						consumePrompt()
					}
					return cmd
				}

				consumePrompt()
				attachments := m.attachments.List()
				m.attachments.Reset()
				if len(value) == 0 && !message.ContainsTextAttachment(attachments) {
					return nil
				}

				m.randomizePlaceholders()
				m.historyReset()

				if steer {
					return m.steerOrSend(value, attachments...)
				}
				return m.sendMessage(value, attachments...)
			case key.Matches(msg, m.keyMap.Chat.NewSession):
				if !m.hasSession() {
					break
				}
				// Do not block on busy: runs continue on the server
				// independently of the viewing client, so starting a new
				// session never interrupts an in-flight run and the busy
				// session stays reachable from the sessions list.
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Tab):
				if m.state != uiLanding {
					m.setState(m.state, uiFocusMain)
					m.textarea.Blur()
					m.chat.Focus()
					m.chat.SetSelected(m.chat.Len() - 1)
				}
			case key.Matches(msg, m.keyMap.Editor.Stash):
				cmds = append(cmds, m.toggleStash())
			case key.Matches(msg, m.keyMap.Editor.OpenEditor):
				if m.isAgentBusy() {
					cmds = append(cmds, util.ReportWarn("Agent is working, please wait..."))
					break
				}
				cmds = append(cmds, m.openEditor(m.textarea.Value()))
			case key.Matches(msg, m.keyMap.Editor.Newline):
				prevHeight := m.textarea.Height()
				m.textarea.InsertRune('\n')
				m.closeCompletions()
				cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))
			case key.Matches(msg, m.keyMap.Editor.HistoryPrev):
				cmd := m.handleHistoryUp(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.HistoryNext):
				cmd := m.handleHistoryDown(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.Escape):
				cmd := m.handleHistoryEscape(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			default:
				if handleGlobalKeys(msg) {
					// Handle global keys first before passing to textarea.
					break
				}

				// Check for completion triggers before passing to textarea.
				curValue := m.textarea.Value()
				curIdx := len(curValue)

				// Trigger file/resource completions on @.
				if msg.String() == "@" && !m.completionsOpen {
					// Only show if beginning of prompt or after whitespace.
					if curIdx == 0 || (curIdx > 0 && isWhitespace(curValue[curIdx-1])) {
						m.completionsOpen = true
						m.completionsTrigger = '@'
						m.completionsQuery = ""
						m.completionsStartIndex = curIdx
						m.completionsPositionStart = m.completionsPosition()
						depth, limit := m.com.Config().Options.TUI.Completions.Limits()
						cmds = append(cmds, m.completions.Open(depth, limit))
					}
				}

				// Trigger slash command completions on a leading /. Slash
				// commands only dispatch at the start of the prompt, so the
				// completions only open there too.
				if msg.String() == "/" && !m.completionsOpen && curIdx == 0 {
					m.completionsOpen = true
					m.completionsTrigger = '/'
					m.completionsQuery = ""
					m.completionsStartIndex = curIdx
					m.completionsPositionStart = m.completionsPosition()
					m.completions.SetCommands(slashCommandCompletions())
				}

				// remove the details if they are open when user starts typing
				if m.detailsOpen {
					m.detailsOpen = false
					m.updateLayoutAndSize()
				}

				prevHeight := m.textarea.Height()
				cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))

				// Any text modification becomes the current draft.
				m.updateHistoryDraft(curValue)

				// After updating textarea, check if we need to filter
				// completions. Skip filtering on the initial @ keystroke since
				// items are loading async.
				if !m.completionsOpen && msg.String() == "space" {
					m.maybeOpenArgCompletions()
				} else if m.completionsOpen && msg.String() != "@" {
					newValue := m.textarea.Value()
					newIdx := len(newValue)

					// Close completions if cursor moved before start.
					if newIdx <= m.completionsStartIndex {
						m.closeCompletions()
					} else if msg.String() == "space" {
						// Close on space; a slash command with argument
						// completion reopens the popup for its next argument.
						m.closeCompletions()
						m.maybeOpenArgCompletions()
					} else {
						// Extract current word and filter against the trigger.
						word := m.textareaWord()
						switch m.completionsTrigger {
						case '/':
							if strings.HasPrefix(word, "/") {
								m.completionsQuery = word
								m.completions.Filter(m.completionsQuery)
							} else {
								m.closeCompletions()
							}
						case argCompletionTrigger:
							m.completionsQuery = word
							m.completions.Filter(m.completionsQuery)
							// An argument the list cannot complete is still a
							// valid argument (a fuzzy model ref, a path), so
							// give the keys back to the editor.
							if !m.completions.HasItems() {
								m.closeCompletions()
							}
						default:
							if strings.HasPrefix(word, "@") {
								m.completionsQuery = word[1:]
								m.completions.Filter(m.completionsQuery)
							} else {
								m.closeCompletions()
							}
						}
					}
				}
			}
		case uiFocusMain:
			switch {
			case key.Matches(msg, m.keyMap.Tab):
				m.focus = uiFocusEditor
				cmds = append(cmds, m.textarea.Focus())
				m.chat.Blur()
			case key.Matches(msg, m.keyMap.Chat.FocusRightSidebar):
				if m.state == uiChat && !m.isCompact && !m.chatFullscreen && m.hasSession() && m.rightSidebarScrollable {
					m.focus = uiFocusRightSidebar
					m.chat.Blur()
				}
			case key.Matches(msg, m.keyMap.Chat.NewSession):
				if !m.hasSession() {
					break
				}
				m.focus = uiFocusEditor
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.Expand):
				m.chat.ToggleExpandedSelectedItem()
			case key.Matches(msg, m.keyMap.Chat.Up):
				if cmd := m.chat.ScrollByAndAnimate(-1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectPrev()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			case key.Matches(msg, m.keyMap.Chat.Down):
				if cmd := m.chat.ScrollByAndAnimate(1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectNext()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			case key.Matches(msg, m.keyMap.Chat.UpOneItem):
				m.chat.SelectPrev()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.DownOneItem):
				m.chat.SelectNext()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.PrevUserMessage):
				m.chat.SelectPrevUserMessage()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.NextUserMessage):
				m.chat.SelectNextUserMessage()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.HalfPageUp):
				if cmd := m.chat.ScrollByAndAnimate(-m.chat.Height() / 2); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectFirstInView()
			case key.Matches(msg, m.keyMap.Chat.HalfPageDown):
				if cmd := m.chat.ScrollByAndAnimate(m.chat.Height() / 2); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectLastInView()
			case key.Matches(msg, m.keyMap.Chat.PageUp):
				if cmd := m.chat.ScrollByAndAnimate(-m.chat.Height()); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectFirstInView()
			case key.Matches(msg, m.keyMap.Chat.PageDown):
				if cmd := m.chat.ScrollByAndAnimate(m.chat.Height()); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectLastInView()
			case key.Matches(msg, m.keyMap.Chat.Home):
				if cmd := m.chat.ScrollToTopAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectFirst()
			case key.Matches(msg, m.keyMap.Chat.End):
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectLast()
			default:
				if ok, cmd := m.chat.HandleKeyMsg(msg); ok {
					cmds = append(cmds, cmd)
				} else {
					handleGlobalKeys(msg)
				}
			}
		case uiFocusRightSidebar:
			if m.state != uiChat || m.isCompact || m.chatFullscreen || !m.hasSession() || !m.rightSidebarScrollable {
				m.focus = uiFocusMain
				m.chat.Focus()
				break
			}
			switch {
			case key.Matches(msg, m.keyMap.Chat.Up):
				m.rightSidebarOffset = max(0, m.rightSidebarOffset-4)
			case key.Matches(msg, m.keyMap.Chat.Down):
				if m.rightSidebarOffset < m.rightSidebarMaxOffsetVal {
					m.rightSidebarOffset = min(m.rightSidebarOffset+4, m.rightSidebarMaxOffsetVal)
				}
			case key.Matches(msg, m.keyMap.Chat.Home):
				m.rightSidebarOffset = 0
			case key.Matches(msg, m.keyMap.Chat.End):
				m.rightSidebarOffset = m.rightSidebarMaxOffsetVal
			case key.Matches(msg, m.keyMap.Chat.FocusChat):
				m.focus = uiFocusMain
				m.chat.Focus()
			case key.Matches(msg, m.keyMap.Tab):
				m.focus = uiFocusEditor
				cmds = append(cmds, m.textarea.Focus())
				m.chat.Blur()
			default:
				handleGlobalKeys(msg)
			}
		default:
			handleGlobalKeys(msg)
		}
	default:
		handleGlobalKeys(msg)
	}

	return tea.Sequence(cmds...)
}
