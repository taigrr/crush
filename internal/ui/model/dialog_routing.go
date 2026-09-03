package model

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	fimage "github.com/taigrr/crush/internal/ui/image"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/anim"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
)

func (m *UI) handleDialogMsg(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	action := m.dialog.Update(msg)
	if action == nil {
		return tea.Batch(cmds...)
	}

	isOnboarding := m.state == uiOnboarding

	switch msg := action.(type) {
	// Generic dialog messages
	case dialog.ActionClose:
		if isOnboarding && m.dialog.ContainsDialog(dialog.ModelsID) {
			break
		}

		// If the theme picker is closing without a selection, restore the
		// styles captured when it opened so live previews don't stick.
		if m.themePreviewOriginal != nil && m.dialog.ContainsDialog(dialog.ThemeID) {
			m.applyTheme(*m.themePreviewOriginal)
			m.themePreviewOriginal = nil
		}

		if m.dialog.ContainsDialog(dialog.FilePickerID) {
			defer fimage.ResetCache()
		}

		// Closing the session picker without committing discards any live
		// preview and restores the committed session.
		if m.dialog.ContainsDialog(dialog.SessionsID) {
			if cmd := m.cancelPreview(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		// Same for the search palette: closing without committing discards
		// any live preview and restores the committed session view.
		if m.dialog.ContainsDialog(dialog.SearchPaletteID) {
			if cmd := m.cancelPreview(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		m.dialog.CloseFrontDialog()

		if isOnboarding {
			if cmd := m.openModelsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		if m.focus == uiFocusEditor {
			cmds = append(cmds, m.textarea.Focus())
		}
	case dialog.ActionCmd:
		if msg.Cmd != nil {
			cmds = append(cmds, msg.Cmd)
		}

	// Session dialog messages.
	case dialog.ActionSelectSession:
		m.dialog.CloseDialog(dialog.SessionsID)
		cmds = append(cmds, m.loadSession(msg.Session.ID))
	case dialog.ActionSessionImportComplete:
		m.dialog.CloseDialog(dialog.SessionImportID)
		imported, resynced, modified, unchanged := 0, 0, 0, 0
		for _, result := range msg.Results {
			switch {
			case result.Modified:
				modified++
			case result.AlreadyExist:
				unchanged++
			case result.Imported == result.Messages:
				imported++
			default:
				resynced++
			}
		}
		parts := []string{fmt.Sprintf("%d imported", imported)}
		if resynced > 0 {
			parts = append(parts, fmt.Sprintf("%d updated", resynced))
		}
		if unchanged > 0 {
			parts = append(parts, fmt.Sprintf("%d unchanged", unchanged))
		}
		if modified > 0 {
			parts = append(parts, fmt.Sprintf("%d modified in Crush (skipped)", modified))
		}
		cmds = append(cmds, util.ReportInfo("Session import: "+strings.Join(parts, ", ")))
		cmds = append(cmds, m.loadWorkspaceOverviews())
	case dialog.ActionPreviewSession:
		// Picker cursor moved: debounce a live preview (picker sessions are
		// always current-workspace).
		if cmd := m.schedulePreview(msg.SessionID, ""); cmd != nil {
			cmds = append(cmds, cmd)
		}

	// Search palette messages.
	case dialog.ActionSearchQueryChanged:
		if cmd := m.handleSearchQueryChanged(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionPreviewSearchResult:
		if cmd := m.previewSearchResult(msg.Hit); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectSearchResult:
		if cmd := m.commitSearchResult(msg.Hit); cmd != nil {
			cmds = append(cmds, cmd)
		}

	// Milestones dialog messages.
	case dialog.ActionScrollToTurn:
		m.dialog.CloseDialog(dialog.MilestonesID)
		// TurnNumber is 1-based index into session messages; map to chat
		// list index (0-based). Clamp to valid range.
		idx := max(0, msg.TurnNumber-1)
		if idx >= m.chat.Len() {
			idx = m.chat.Len() - 1
		}
		m.chat.SetSelected(idx)
		if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}

	// Open dialog message.
	case dialog.ActionOpenDialog:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.openDialog(msg.DialogID); cmd != nil {
			cmds = append(cmds, cmd)
		}

	// Command dialog messages.
	case dialog.ActionToggleYoloMode:
		yolo := !m.com.Workspace.PermissionSkipRequests()
		m.com.Workspace.PermissionSetSkipRequests(yolo)
		m.setEditorPrompt(yolo)
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleSysadminMode:
		enabled := !m.com.Workspace.PermissionSysadminMode()
		m.com.Workspace.PermissionSetSysadminMode(enabled)
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		cmds = append(cmds, util.ReportInfo("Sysadmin mode "+status))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionSelectNotificationStyle:
		cfg := m.com.Config()
		if cfg != nil && cfg.Options != nil {
			cfg.Options.NotificationStyle = msg.Style
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.notification_style", msg.Style); err != nil {
				cmds = append(cmds, util.ReportError(err))
			} else {
				cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Notifications set to: "+msg.Style)))
			}
			// Reinitialize notification backend with new style.
			m.notifyBackend = selectNotificationBackend(m.caps, cfg)
		}
		m.dialog.CloseDialog(dialog.NotificationsID)
	case dialog.ActionSelectEmbedding:
		cfg := m.com.Config()
		if cfg != nil {
			if msg.Choice.Provider == "" && msg.Choice.Model == "" {
				// Disable embeddings: remove the field entirely.
				changed := cfg.Embedding != nil
				if err := m.com.Workspace.RemoveConfigField(config.ScopeGlobal, "embedding"); err != nil {
					cmds = append(cmds, util.ReportError(err))
				} else if changed {
					cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Embeddings disabled (substring search only)")))
				} else {
					cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Embeddings already disabled")))
				}
			} else {
				ec := &config.EmbeddingConfig{
					Provider:   msg.Choice.Provider,
					Model:      msg.Choice.Model,
					Dimensions: msg.Choice.Dimensions,
					Normalize:  true,
				}
				changed := cfg.Embedding.Signature() != ec.Signature()
				if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "embedding", ec); err != nil {
					cmds = append(cmds, util.ReportError(err))
				} else if changed {
					cmds = append(cmds, util.CmdHandler(util.NewInfoMsg(fmt.Sprintf("Embedding model set to %s/%s (applies on restart)", ec.Provider, ec.Model))))
				} else {
					cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("No change to embedding model")))
				}
			}
		}
		m.dialog.CloseDialog(dialog.EmbeddingsID)
	case dialog.ActionStartBackfill:
		m.dialog.CloseDialog(dialog.CommandsID)
		ws := m.com.Workspace
		cmds = append(cmds, func() tea.Msg {
			n, err := ws.EmbedPendingCount(context.Background())
			return backfillCountMsg{count: n, err: err}
		})
	case dialog.ActionConfirmBackfill:
		m.dialog.CloseDialog(dialog.BackfillConfirmID)
		ws := m.com.Workspace
		// Show the sidebar progress bar and start polling status. The
		// total was seeded when the confirm dialog opened, so the bar is
		// meaningful immediately.
		m.backfillActive = true
		m.backfillStatus.Enabled = true
		cmds = append(cmds, util.ReportInfo("Embedding history in the background…"))
		cmds = append(cmds, m.pollEmbeddingStatusCmd())
		cmds = append(cmds, func() tea.Msg {
			n, err := ws.EmbedBackfill(context.Background())
			return backfillDoneMsg{count: n, err: err}
		})
	case dialog.ActionNewSession:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before starting a new session..."))
			break
		}
		if cmd := m.newSession(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionSummarize:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before summarizing session..."))
			break
		}
		cmds = append(cmds, func() tea.Msg {
			err := m.com.Workspace.AgentSummarize(context.Background(), msg.SessionID)
			if err != nil {
				return util.ReportError(err)()
			}
			return nil
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleHelp:
		m.status.ToggleHelp()
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionExternalEditor:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is working, please wait..."))
			break
		}
		cmds = append(cmds, m.openEditor(m.textarea.Value()))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleCompactMode:
		cmds = append(cmds, m.toggleCompactMode())
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionTogglePills:
		if cmd := m.togglePillsExpanded(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionArchiveSession:
		m.dialog.CloseDialog(dialog.ArchiveConfirmID)
		if cmd := m.archiveCurrentSession(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionToggleThinking:
		cmds = append(cmds, func() tea.Msg {
			cfg := m.com.Config()
			if cfg == nil {
				return util.ReportError(errors.New("configuration not found"))()
			}

			agentCfg, ok := cfg.Agents[config.AgentCoder]
			if !ok {
				return util.ReportError(errors.New("agent configuration not found"))()
			}

			currentModel := cfg.Models[agentCfg.Model]
			currentModel.Think = !currentModel.Think
			if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, agentCfg.Model, currentModel); err != nil {
				return util.ReportError(err)()
			}
			m.com.Workspace.UpdateAgentModel(context.TODO())
			status := "disabled"
			if currentModel.Think {
				status = "enabled"
			}
			return util.NewInfoMsg("Thinking mode " + status)
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleTransparentBackground:
		cmds = append(cmds, func() tea.Msg {
			cfg := m.com.Config()
			if cfg == nil {
				return util.ReportError(errors.New("configuration not found"))()
			}

			isTransparent := cfg.Options != nil && cfg.Options.TUI.Transparent != nil && *cfg.Options.TUI.Transparent
			newValue := !isTransparent
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.tui.transparent", newValue); err != nil {
				return util.ReportError(err)()
			}
			m.isTransparent = newValue

			status := "disabled"
			if newValue {
				status = "enabled"
			}
			return util.NewInfoMsg("Transparent background " + status)
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleLowBandwidth:
		cmds = append(cmds, func() tea.Msg {
			cfg := m.com.Config()
			if cfg == nil {
				return util.ReportError(errors.New("configuration not found"))()
			}
			newValue := !cfg.LowBandwidthEnabled()
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.tui.low_bandwidth", newValue); err != nil {
				return util.ReportError(err)()
			}
			// Update the package-level flag so any new spinners
			// (assistant message, tool call) created after this point
			// pick up the change. Existing spinners keep their original
			// mode until the next message.
			anim.SetDefaultLowBandwidth(newValue)

			status := "disabled"
			if newValue {
				status = "enabled. Restart Crush for the FPS change to take effect."
			}
			return util.NewInfoMsg("Low-bandwidth mode " + status)
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleSound:
		cmds = append(cmds, func() tea.Msg {
			cfg := m.com.Config()
			if cfg == nil {
				return util.ReportError(errors.New("configuration not found"))()
			}
			muted := cfg.Options != nil && cfg.Options.Sound != nil && cfg.Options.Sound.Disabled
			newValue := !muted
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.sound.disabled", newValue); err != nil {
				return util.ReportError(err)()
			}

			status := "unmuted"
			if newValue {
				status = "muted"
			}
			return util.NewInfoMsg("Sound effects " + status)
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionQuit:
		cmds = append(cmds, tea.Quit)
	case dialog.ActionEnableDockerMCP:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.enableDockerMCP)
	case dialog.ActionDisableDockerMCP:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.disableDockerMCP)

	// Snapshot/Worktree actions.
	case dialog.ActionOpenSnapshotsDialog:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.openSnapshotsDialog(msg.SessionID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionRestoreSnapshot:
		m.dialog.CloseDialog(dialog.SnapshotsID)
		cmds = append(cmds, m.restoreSnapshot(msg.SnapshotID))
	case dialog.ActionOpenWorktreesDialog:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.openWorktreesDialog(msg.SessionID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionCreateWorktree:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.createWorktree(msg.SessionID, msg.Name, msg.FromSnapshotID))
	case dialog.ActionSwitchWorktree:
		m.dialog.CloseDialog(dialog.WorktreesID)
		// First switch to the worktree's session if it's different from the current session.
		if m.session == nil || m.session.ID != msg.SessionID {
			cmds = append(cmds, m.loadSessionAndSwitchWorktree(msg.SessionID, msg.WorktreeID))
		} else {
			cmds = append(cmds, m.switchWorktree(msg.SessionID, msg.WorktreeID))
		}
	case dialog.ActionRunSnapshotGC:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.runSnapshotGC())
	case dialog.ActionOpenForkDialog:
		if cmd := m.openForkDialog(msg.SessionID, msg.MessageID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionForkConversation:
		m.dialog.CloseDialog(dialog.ForkID)
		// Show a progress dialog while the (blocking) fork RPC runs so the
		// UI doesn't appear frozen; it's driven by streamed ForkProgress
		// events and closed on completion.
		m.dialog.OpenDialog(dialog.NewForkProgress(m.com))
		cmds = append(cmds, m.forkConversation(msg.SessionID, msg.MessageID, msg.NewSessionTitle, msg.CreateWorktree))
	case dialog.ActionOpenMergeWorktreeDialog:
		m.dialog.CloseDialog(dialog.WorktreesID)
		if cmd := m.openMergeWorktreeDialog(msg.WorktreeID, msg.WorktreeName); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionMergeWorktree:
		m.dialog.CloseDialog(dialog.MergeWorktreeID)
		cmds = append(cmds, m.mergeWorktree(msg.WorktreeID, msg.TargetBranch, msg.Rebase))

	case dialog.ActionInitializeProject:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before summarizing session..."))
			break
		}
		cmds = append(cmds, m.initializeProject())
		m.dialog.CloseDialog(dialog.CommandsID)

	case dialog.ActionReloadConfig:
		cmds = append(cmds, m.reloadConfig())
		m.dialog.CloseDialog(dialog.CommandsID)

	case dialog.ActionSelectModel:
		if cmd := m.handleSelectModel(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectReasoningEffort:
		cfg := m.com.Config()
		if cfg == nil {
			cmds = append(cmds, util.ReportError(errors.New("configuration not found")))
			break
		}

		agentCfg, ok := cfg.Agents[config.AgentCoder]
		if !ok {
			cmds = append(cmds, util.ReportError(errors.New("agent configuration not found")))
			break
		}

		currentModel := cfg.Models[agentCfg.Model]
		currentModel.ReasoningEffort = msg.Effort
		if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, agentCfg.Model, currentModel); err != nil {
			cmds = append(cmds, util.ReportError(err))
			break
		}

		// If the agent is busy the server queues the live apply until it
		// finishes (see app.UpdateAgentModel -> UpdateModelsWhenIdle), so
		// fire the RPC regardless and tell the user when it will take effect.
		queued := m.isAgentBusy()
		cmds = append(cmds, func() tea.Msg {
			m.com.Workspace.UpdateAgentModel(context.TODO())
			if queued {
				return util.NewInfoMsg("Reasoning effort change queued; applies when the agent finishes")
			}
			return util.NewInfoMsg("Reasoning effort set to " + msg.Effort)
		})
		m.dialog.CloseDialog(dialog.ReasoningID)
	case dialog.ActionPreviewTheme:
		// Live preview as the picker selection moves; not persisted.
		m.applyTheme(msg.Styles)
	case dialog.ActionSelectTheme:
		// Confirm: persist the name (local config overrides global, so this
		// is also where per-workspace themes are written if a local config
		// exists) and keep the already-previewed styles.
		m.applyTheme(msg.Styles)
		m.themePreviewOriginal = nil
		name := msg.Name
		m.activeThemeName = name
		cmds = append(cmds, func() tea.Msg {
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.tui.theme", name); err != nil {
				return util.NewWarnMsg("Theme applied but not saved: " + err.Error())
			}
			return util.NewInfoMsg("Theme set to " + name)
		})
		m.dialog.CloseDialog(dialog.ThemeID)
	case dialog.ActionPermissionResponse:
		m.dialog.CloseDialog(dialog.PermissionsID)
		// Clear the cached pending request eagerly: this resolves the
		// request synchronously service-side, so a session switch before
		// the resolving notification round-trips must not re-surface a
		// now-stale ("zombie") prompt.
		if p := m.pendingPermissions[msg.Permission.SessionID]; p != nil && p.ToolCallID == msg.Permission.ToolCallID {
			delete(m.pendingPermissions, msg.Permission.SessionID)
			m.leftSidebar.SetPendingSessions(m.pendingSessionIDs())
		}
		switch msg.Action {
		case dialog.PermissionAllow:
			m.com.Workspace.PermissionGrant(msg.Permission)
		case dialog.PermissionAllowForSession:
			m.com.Workspace.PermissionGrantPersistent(msg.Permission)
		case dialog.PermissionDeny:
			m.com.Workspace.PermissionDeny(msg.Permission)
		}

	case dialog.ActionQuestionResponse:
		m.dialog.CloseDialog(dialog.QuestionID)
		// Same eager-clear rationale as ActionPermissionResponse above.
		if q := m.pendingQuestions[msg.Request.SessionID]; q != nil && q.ToolCallID == msg.Request.ToolCallID {
			delete(m.pendingQuestions, msg.Request.SessionID)
			m.leftSidebar.SetPendingSessions(m.pendingSessionIDs())
		}
		m.com.Workspace.QuestionAnswer(msg.Answer)

	case dialog.ActionFilePickerSelected:
		cmds = append(cmds, tea.Sequence(
			msg.Cmd(),
			func() tea.Msg {
				m.dialog.CloseDialog(dialog.FilePickerID)
				return nil
			},
			func() tea.Msg {
				fimage.ResetCache()
				return nil
			},
		))

	case dialog.ActionRunCustomCommand:
		if len(msg.Arguments) > 0 && msg.Args == nil {
			m.dialog.CloseFrontDialog()
			argsDialog := dialog.NewArguments(
				m.com,
				"Custom Command Arguments",
				"",
				msg.Arguments,
				msg, // Pass the action as the result
			)
			m.dialog.OpenDialog(argsDialog)
			break
		}
		content := msg.Content
		if msg.Args != nil {
			content = substituteArgs(content, msg.Args)
		}
		// If this is a skill command, format it using the skill's FormatInvocation method
		if msg.Skill != nil {
			content = msg.Skill.FormatInvocation()
		}
		cmds = append(cmds, m.sendMessage(content))
		m.dialog.CloseFrontDialog()
	case dialog.ActionAttachSkill:
		m.dialog.CloseFrontDialog()
		cmds = append(cmds, m.attachSkill(msg.ID, msg.Name))
	case dialog.ActionRunMCPPrompt:
		if len(msg.Arguments) > 0 && msg.Args == nil {
			m.dialog.CloseFrontDialog()
			title := cmp.Or(msg.Title, "MCP Prompt Arguments")
			argsDialog := dialog.NewArguments(
				m.com,
				title,
				msg.Description,
				msg.Arguments,
				msg, // Pass the action as the result
			)
			m.dialog.OpenDialog(argsDialog)
			break
		}
		cmds = append(cmds, m.runMCPPrompt(msg.ClientID, msg.PromptID, msg.Args))
	default:
		cmds = append(cmds, util.CmdHandler(msg))
	}

	return tea.Batch(cmds...)
}
