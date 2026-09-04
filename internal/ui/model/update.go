package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"slices"
	"strings"
	"time"

	xstrings "github.com/charmbracelet/x/exp/strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/fork"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/ui/anim"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/completions"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/notification"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/workspace"
)

// Update handles updates to the UI model.
func (m *UI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.hasSession() && m.isAgentBusy() {
		queueSize := m.com.Workspace.AgentQueuedPrompts(m.session.ID)
		if queueSize != m.promptQueue {
			m.promptQueue = queueSize
			m.updateLayoutAndSize()
		}
	}
	// Update terminal capabilities
	m.caps.Update(msg)
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		isDark := msg.IsDark()
		if isDark != m.hasDarkBackground {
			m.hasDarkBackground = isDark
			m.applyConfiguredTheme()
			if themeDialog, ok := m.dialog.Dialog(dialog.ThemeID).(*dialog.Theme); ok {
				original := *m.com.Styles
				m.themePreviewOriginal = &original
				m.applyTheme(themeDialog.SetDarkBackground(isDark))
			}
		}
	case tea.EnvMsg:
		// Is this Windows Terminal?
		if !m.sendProgressBar {
			m.sendProgressBar = slices.Contains(msg, "WT_SESSION")
		}
		cmds = append(cmds, common.QueryCmd(uv.Environ(msg)))
	case serverVersionMsg:
		m.versionMismatch = msg.mismatch
		m.serverVersionStr = msg.version
	case versionCheckTickMsg:
		cmds = append(cmds, m.checkServerVersion(), m.scheduleVersionCheck())
	case assistantInfoTickMsg:
		// Refreshing the humanized "since" labels bumps item versions
		// when they drift, which invalidates the list memo so the next
		// View repaints with the current relative time.
		m.chat.RefreshAssistantInfoTimes(time.Now())
		cmds = append(cmds, m.scheduleAssistantInfoTick())
	case tea.ModeReportMsg:
		m.updateNotificationBackend()
	case uv.UnknownOscEvent:
		m.updateNotificationBackend()
	case tea.FocusMsg:
		m.notifyWindowFocused = true
		// Terminals report their background color only when asked, so a
		// light/dark switch made while we were unfocused would otherwise go
		// unnoticed. Re-ask so the theme variant catches up.
		cmds = append(cmds, tea.RequestBackgroundColor)
	case tea.BlurMsg:
		m.notifyWindowFocused = false
	case pubsub.Event[notify.Notification]:
		if cmd := m.handleAgentNotification(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// A run finished somewhere: refresh the session navigator so its
		// busy/unread markers stay current. Skip while it is focused: a
		// rebuild reorders rows (busy/unread/recent) and would move the
		// cursor out from under the user mid-navigation, causing enter to
		// act on the wrong session. Live markers refresh on next open.
		if m.leftSidebarVisible && m.focus != uiFocusLeftSidebar {
			cmds = append(cmds, m.loadWorkspaceOverviews())
		}
	case previewTickMsg:
		if cmd := m.handlePreviewTick(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case previewLoadedMsg:
		if cmd := m.handlePreviewLoaded(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case previewLoadFailedMsg:
		if cmd := m.handlePreviewLoadFailed(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case previewRestoreMsg:
		if cmd := m.handlePreviewRestore(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case loadSessionMsg:
		cmds = append(cmds, m.handleLoadSession(msg)...)

	case loadSessionAndSwitchWorktreeMsg:
		cmds = append(cmds, m.handleLoadSessionAndSwitchWorktree(msg)...)

	case sessionFilesUpdatesMsg:
		cmds = append(cmds, m.applySessionFilesUpdate(msg))

	case sendMessageMsg:
		cmds = append(cmds, m.sendMessage(msg.Content, msg.Attachments...))

	case openSessionImportMsg:
		if len(msg.sources) == 0 {
			cmds = append(cmds, util.ReportInfo("No supported coding-agent sessions found"))
			break
		}
		m.dialog.OpenDialog(dialog.NewSessionImport(m.com, msg.sources))
	case userCommandsLoadedMsg:
		m.customCommands = msg.Commands
		dia := m.dialog.Dialog(dialog.CommandsID)
		if dia == nil {
			break
		}

		commands, ok := dia.(*dialog.Commands)
		if ok {
			commands.SetCustomCommands(m.customCommands)
		}

	case lspStateChangedMsg:
		m.lspStates = msg.states
	case mcpStateChangedMsg:
		m.mcpStates = msg.states
	case mcpPromptsLoadedMsg:
		m.mcpPrompts = msg.Prompts
		dia := m.dialog.Dialog(dialog.CommandsID)
		if dia == nil {
			break
		}

		commands, ok := dia.(*dialog.Commands)
		if ok {
			commands.SetMCPPrompts(m.mcpPrompts)
		}

	case promptHistoryLoadedMsg:
		m.promptHistory.messages = msg.messages
		m.promptHistory.index = -1
		m.promptHistory.draft = ""

	case closeDialogMsg:
		m.dialog.CloseFrontDialog()

	case pubsub.Event[session.Session]:
		// Keep the navigator (and landing screen, which renders the
		// same overviews) in sync with the set of sessions. Only
		// structural changes — a session created or deleted — or a
		// rename of the *currently viewed* session warrant a refresh.
		// We deliberately do NOT refresh on every UpdatedEvent:
		// sessions are re-saved on every cost/token/message-count
		// bump during a live turn, and loadWorkspaceOverviews is a
		// blocking cross-workspace fetch (a REST round-trip in client
		// mode) — refreshing per bump would hammer the server. The
		// navigator is skipped while focused so a rebuild does not
		// move the cursor out from under the user mid-navigation (it
		// refreshes on next open).
		refreshNav := func() {
			if (m.leftSidebarVisible && m.focus != uiFocusLeftSidebar) || m.state == uiLanding {
				cmds = append(cmds, m.loadWorkspaceOverviews())
			}
		}
		if msg.Type == pubsub.CreatedEvent {
			refreshNav()
		}
		if msg.Type == pubsub.DeletedEvent {
			refreshNav()
			if m.session != nil && m.session.ID == msg.Payload.ID {
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			break
		}
		if m.session != nil && msg.Payload.ID == m.session.ID {
			prevHasInProgress := hasInProgressTodo(m.session.Todos)
			prevTitle := m.session.Title
			m.session = &msg.Payload
			if msg.Payload.Title != "" && msg.Payload.Title != prevTitle {
				cmds = append(cmds, m.startTitleAnimation(msg.Payload.Title))
				// The visible session was renamed; the navigator
				// shows titles, so refresh it too.
				refreshNav()
			}
			if !prevHasInProgress && hasInProgressTodo(m.session.Todos) {
				m.todoIsSpinning = true
				cmds = append(cmds, m.todoSpinner.Tick)
				m.updateLayoutAndSize()
			}
			m.autoExpandPillsIfReasonable()
		}
	case pubsub.Event[message.Message]:
		// Check if this is a child session message for an agent tool.
		if m.session == nil {
			break
		}
		if msg.Payload.SessionID != m.session.ID {
			// This might be a child session message from an agent tool.
			// Skip visible-chat mutation while previewing (nested tool
			// items would otherwise render over the preview); the event is
			// persisted server-side and picked up on restore.
			if !m.previewing() {
				if cmd := m.handleChildSessionMessage(msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			break
		}
		switch msg.Type {
		case pubsub.CreatedEvent:
			// While a live preview is shown, the chat view belongs to the
			// previewed (read-only) session, NOT the committed one. Skip the
			// visible-chat mutation so the committed session's incoming
			// messages don't clobber the preview; they are persisted
			// server-side and picked up when the preview is cancelled and
			// the committed session is reloaded.
			if !m.previewing() {
				cmds = append(cmds, m.appendSessionMessage(msg.Payload))
			}
			// Refresh prompt history once the user/shell message is
			// actually persisted. Reloading here (rather than concurrently
			// with the send) avoids a race where ListUserMessages reads the
			// DB before the new message lands, which made the latest entry
			// show up only after the next submission.
			if msg.Payload.Role == message.User || msg.Payload.Role == message.Shell {
				cmds = append(cmds, m.loadPromptHistory())
			}
		case pubsub.UpdatedEvent:
			if !m.previewing() {
				cmds = append(cmds, m.updateSessionMessage(msg.Payload))
			}
		case pubsub.DeletedEvent:
			if !m.previewing() {
				m.chat.RemoveMessage(msg.Payload.ID)
			}
		}
		// start the spinner if there is a new message
		if hasInProgressTodo(m.session.Todos) && m.isAgentBusy() && !m.todoIsSpinning {
			m.todoIsSpinning = true
			cmds = append(cmds, m.todoSpinner.Tick)
		}
		// stop the spinner if the agent is not busy anymore
		if m.todoIsSpinning && !m.isAgentBusy() {
			m.todoIsSpinning = false
		}
		// there is a number of things that could change the pills here so we want to re-render
		m.renderPills()
	case pubsub.Event[history.File]:
		cmds = append(cmds, m.handleFileEvent(msg.Payload))
	case pubsub.Event[fork.ForkProgress]:
		// Drive the fork progress dialog's bar. The terminal Done event is
		// handled by forkCompletedMsg (which closes the dialog), so we only
		// update the in-flight bar here.
		if d := m.dialog.Dialog(dialog.ForkProgressID); d != nil {
			if fp, ok := d.(*dialog.ForkProgress); ok {
				fp.SetProgress(msg.Payload.Stage, msg.Payload.Percent)
			}
		}
	case pubsub.Event[workspace.LSPEvent]:
		m.lspStates = m.com.Workspace.LSPGetStates()
	case pubsub.Event[workspace.ConnectionEvent]:
		// The connection state itself lives in the workspace (single
		// source of truth, read live by the header and sidebar). This
		// event just wakes the UI so those surfaces re-render with the
		// new state.
	case pubsub.Event[workspace.HeldPromptsEvent]:
		// Prompts parked during a server update have been redelivered
		// to the replacement server (or failed to be). A failed one is
		// handed back to the editor so the text is not lost.
		var cmds []tea.Cmd
		if msg.Payload.Sent > 0 {
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf("Server updated; sent %d held message(s).", msg.Payload.Sent)))
		}
		for _, f := range msg.Payload.Failed {
			cmds = append(cmds, m.restoreUnsentPrompt(f.Prompt, f.Attachments,
				fmt.Errorf("failed to resend a message held during the server update: %w", f.Err)))
		}
		if n := msg.Payload.KeptElsewhere; n > 0 {
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf("%d held message(s) belong to another workspace and will be sent when you switch back.", n)))
		}
		return m, tea.Batch(cmds...)
	case pubsub.Event[skills.Event]:
		m.skillStates = msg.Payload.States
	case pubsub.Event[mcp.Event]:
		switch msg.Payload.Type {
		case mcp.EventStateChanged:
			return m, tea.Batch(
				m.handleStateChanged(),
				m.loadMCPrompts,
			)
		case mcp.EventPromptsListChanged:
			return m, handleMCPPromptsEvent(m.com.Workspace, msg.Payload.Name)
		case mcp.EventToolsListChanged:
			return m, handleMCPToolsEvent(m.com.Workspace, msg.Payload.Name)
		case mcp.EventResourcesListChanged:
			return m, handleMCPResourcesEvent(m.com.Workspace, msg.Payload.Name)
		}
	case pubsub.Event[permission.PermissionRequest]:
		// Cache the request so we can re-surface it if the user switches
		// away and back. Only pop the dialog when it belongs to the
		// session the user is currently viewing: showing another
		// session's prompt over the active one would let allow/deny (and
		// "allow for session") be applied to the wrong session.
		perm := msg.Payload
		m.pendingPermissions[perm.SessionID] = &perm
		m.leftSidebar.SetPendingSessions(m.pendingSessionIDs())
		if m.session != nil && perm.SessionID == m.session.ID {
			if cmd := m.openPermissionsDialog(perm); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if cmd := m.sendNotification(notification.Notification{
			Title:   "Crush is waiting...",
			Message: fmt.Sprintf("Permission required to execute \"%s\"", msg.Payload.ToolName),
		}); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[permission.PermissionNotification]:
		m.handlePermissionNotification(msg.Payload)
	case pubsub.Event[question.Request]:
		// Mirrors the permission.PermissionRequest case above: cache
		// the request so it can be re-surfaced on session switch, and
		// only pop the dialog for the session the user is currently
		// viewing.
		q := msg.Payload
		m.pendingQuestions[q.SessionID] = &q
		m.leftSidebar.SetPendingSessions(m.pendingSessionIDs())
		if m.session != nil && q.SessionID == m.session.ID {
			if cmd := m.openQuestionDialog(q); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if cmd := m.sendNotification(notification.Notification{
			Title:   "Crush is waiting...",
			Message: "The agent has a question for you.",
		}); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[question.Notification]:
		m.handleQuestionNotification(msg.Payload)
	case pubsub.Event[proto.AttentionEvent]:
		if cmd := m.handleAttentionEvent(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case cancelTimerExpiredMsg:
		m.handleCancelTimerExpired(msg)
	case workspaceOverviewsMsg:
		m.leftSidebar.SetOverviews(msg.overviews)
		m.leftSidebar.SetCurrentRoot(m.com.Workspace.BaseDir())
		if m.session != nil {
			m.leftSidebar.SetActiveSession(m.session.ID)
		}
	case sessionsArchivedMsg:
		// Only touch the sidebar's live selection/cursor while it is still
		// visible and focused. If the user closed it after pressing `a`,
		// the round-1 close-clears-selection rule already dropped the
		// selection; re-populating it here (on partial failure) would
		// resurrect a hidden selection that a later `a` could bulk-archive.
		sidebarActive := m.leftSidebarVisible && m.focus == uiFocusLeftSidebar
		if msg.overviews != nil {
			m.leftSidebar.SetOverviews(msg.overviews)
			m.leftSidebar.SetCurrentRoot(m.com.Workspace.BaseDir())
			if m.session != nil {
				m.leftSidebar.SetActiveSession(m.session.ID)
			}
		}
		if sidebarActive {
			// Keep only the sessions that failed to archive selected so the
			// user can retry them; a fully successful archive clears it.
			m.leftSidebar.SetSelection(msg.failed)
			if msg.survivorID != "" {
				m.leftSidebar.FocusSessionID(msg.survivorID)
			}
		} else {
			m.leftSidebar.ClearSelection()
		}
		switch {
		case len(msg.failed) > 0:
			cmds = append(cmds, util.ReportError(fmt.Errorf(
				"Archived %d session(s); %d failed", msg.succeeded, len(msg.failed),
			)))
		case msg.succeeded > 0 && msg.skipped > 0:
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf(
				"Archived %d session(s); skipped %d (active or busy)", msg.succeeded, msg.skipped,
			)))
		case msg.succeeded > 0:
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf(
				"Archived %d session(s)", msg.succeeded,
			)))
		}
	case sessionsMarkedReadMsg:
		// Refresh the sidebar so derived unread state (green dots) updates.
		if msg.overviews != nil {
			m.leftSidebar.SetOverviews(msg.overviews)
			m.leftSidebar.SetCurrentRoot(m.com.Workspace.BaseDir())
			if m.session != nil {
				m.leftSidebar.SetActiveSession(m.session.ID)
			}
		}
		// No destructive concern: clear the selection and exit visual mode
		// unconditionally; the cursor stays where it is (sessions remain in
		// the list).
		m.leftSidebar.ClearSelection()
		switch {
		case len(msg.failed) > 0:
			cmds = append(cmds, util.ReportError(fmt.Errorf(
				"Marked %d read; %d failed", msg.succeeded, len(msg.failed),
			)))
		case msg.succeeded > 0:
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf(
				"Marked %d read", msg.succeeded,
			)))
		}
	case favoriteToggledMsg:
		if msg.err != nil {
			cmds = append(cmds, util.ReportError(fmt.Errorf("Failed to update favorite: %w", msg.err)))
			break
		}
		// Refresh the sidebar so the session moves in/out of the Favorite
		// section. SetOverviews keeps the cursor on the same session.
		if msg.overviews != nil {
			m.leftSidebar.SetOverviews(msg.overviews)
			m.leftSidebar.SetCurrentRoot(m.com.Workspace.BaseDir())
			if m.session != nil {
				m.leftSidebar.SetActiveSession(m.session.ID)
			}
		}
		if msg.favorite {
			cmds = append(cmds, util.ReportInfo("Favorited session"))
		} else {
			cmds = append(cmds, util.ReportInfo("Unfavorited session"))
		}
	case activeSessionArchivedMsg:
		if msg.err != nil {
			cmds = append(cmds, util.ReportError(msg.err))
			break
		}
		// Never keep the user viewing the session they just archived:
		// switch to the most-recent remaining session in the workspace, or
		// drop to the empty landing state (newSession clears m.session and
		// returns to uiLanding — it does not manufacture a fresh session).
		if msg.nextSessionID != "" {
			cmds = append(cmds, m.loadSession(msg.nextSessionID))
		} else if cmd := m.newSession(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, util.ReportInfo("Session archived"))
		if m.leftSidebarVisible {
			cmds = append(cmds, m.loadWorkspaceOverviews())
		}
	case workspaceSwitchedMsg:
		// The client re-targeted a new workspace; refresh the cached yolo
		// indicator from the newly-attached workspace's own skip-requests
		// flag (fetched alongside the switch itself, off this goroutine)
		// before doing anything else — each workspace's flag is
		// independent, so leaving the previous workspace's cached value in
		// place would show the wrong state (e.g. still "on" after
		// switching into a workspace that never had yolo enabled).
		m.setEditorPrompt(msg.yolo)
		cmds = append(cmds, m.loadSession(msg.sessionID))
	case backfillCountMsg:
		if msg.err != nil {
			cmds = append(cmds, util.ReportError(msg.err))
		} else if msg.count == 0 {
			cmds = append(cmds, util.ReportInfo("Nothing to embed; history is already indexed (or no embedder is configured)."))
		} else {
			model := "the active model"
			if cfg := m.com.Config(); cfg != nil && cfg.Embedding != nil {
				model = cfg.Embedding.Provider + "/" + cfg.Embedding.Model
			}
			m.dialog.OpenDialog(dialog.NewBackfillConfirm(m.com, msg.count, model))
			// Seed the total so the sidebar bar is meaningful from the
			// first frame, before the first status poll returns.
			m.backfillStatus = proto.EmbeddingStatus{Enabled: true, Total: msg.count}
		}
	case backfillDoneMsg:
		m.backfillActive = false
		if msg.err != nil {
			cmds = append(cmds, util.ReportError(msg.err))
		} else {
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf("Embedded %d message(s).", msg.count)))
		}
	case embeddingStatusMsg:
		if msg.err == nil {
			m.backfillStatus = msg.status
		}
		// Keep polling while the backfill is still running.
		if m.backfillActive {
			cmds = append(cmds, m.pollEmbeddingStatusCmd())
		}
	case tea.TerminalVersionMsg:
		termVersion := strings.ToLower(msg.Name)
		// Only enable progress bar for the following terminals.
		if !m.sendProgressBar {
			m.sendProgressBar = xstrings.ContainsAnyOf(termVersion, "ghostty", "iterm2", "rio")
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.updateLayoutAndSize()
		if m.state == uiChat && m.chat.Follow() {
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case tea.KeyboardEnhancementsMsg:
		m.keyenh = msg
		if msg.SupportsKeyDisambiguation() {
			m.keyMap.Models.SetHelp("ctrl+m", "models")
			m.keyMap.Editor.Newline.SetHelp("shift+enter", "newline")
		}
	case copyChatHighlightMsg:
		cmds = append(cmds, m.copyChatHighlight())
	case DelayedClickMsg:
		// Handle delayed single-click action (e.g., expansion).
		m.chat.HandleDelayedClick(msg)
	case tea.MouseClickMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}

		// Left session navigator click-to-open. Handle before the other
		// click routers so a sidebar click isn't misrouted to the chat.
		if cmd, handled := m.handleLeftSidebarClick(msg); handled {
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		if cmd := m.handleClickFocus(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

		if m.handleAttachmentClick(msg) {
			return m, tea.Batch(cmds...)
		}

		switch m.state {
		case uiChat:
			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			if !image.Pt(msg.X, msg.Y).In(m.layout.sidebar) {
				if handled, cmd := m.chat.HandleMouseDown(x, y); handled {
					m.lastClickTime = time.Now()
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}

	case tea.MouseMotionMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}

		switch m.state {
		case uiChat:
			if msg.Y <= 0 {
				if cmd := m.chat.ScrollByAndAnimate(-1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectPrev()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			} else if msg.Y >= m.chat.Height()-1 {
				if cmd := m.chat.ScrollByAndAnimate(1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectNext()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}

			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			m.chat.HandleMouseDrag(x, y)
		}

	case tea.MouseReleaseMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}

		switch m.state {
		case uiChat:
			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			if m.chat.HandleMouseUp(x, y) && m.chat.HasHighlight() {
				cmds = append(cmds, tea.Tick(doubleClickThreshold, func(t time.Time) tea.Msg {
					if time.Since(m.lastClickTime) >= doubleClickThreshold {
						return copyChatHighlightMsg{}
					}
					return nil
				}))
			}
		}
	case tea.MouseWheelMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}

		// Otherwise handle mouse wheel for chat.
		switch m.state {
		case uiChat:
			// Scroll the right info sidebar when the pointer is over it.
			if m.rightSidebarScrollable && !m.isCompact && !m.chatFullscreen &&
				image.Pt(msg.X, msg.Y).In(m.layout.sidebar) {
				switch msg.Button {
				case tea.MouseWheelUp:
					m.rightSidebarOffset = max(0, m.rightSidebarOffset-MouseScrollThreshold)
				case tea.MouseWheelDown:
					m.rightSidebarOffset = min(m.rightSidebarOffset+MouseScrollThreshold, m.rightSidebarMaxOffsetVal)
				}
				break
			}
			switch msg.Button {
			case tea.MouseWheelUp:
				if cmd := m.chat.ScrollByAndAnimate(-MouseScrollThreshold); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectPrev()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			case tea.MouseWheelDown:
				if cmd := m.chat.ScrollByAndAnimate(MouseScrollThreshold); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					if m.chat.AtBottom() {
						m.chat.SelectLast()
					} else {
						m.chat.SelectNext()
					}
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}
	case shellCommandFinishedMsg:
		if m.shellCancel != nil {
			m.shellCancel = nil
		}
		// Report a genuine failure, but stay quiet on context cancellation
		// (the user pressed cancel) and on a non-zero exit, which is normal
		// shell behavior already reflected in the persisted output.
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			cmds = append(cmds, util.ReportError(fmt.Errorf("shell: %w", msg.err)))
		}
	case anim.StepMsg:
		if m.state == uiChat {
			if cmd := m.chat.Animate(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if m.chat.Follow() {
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	case titleAnimTickMsg:
		if cmd := m.handleTitleAnimTick(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case spinner.TickMsg:
		if m.dialog.HasDialogs() {
			// route to dialog
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if m.state == uiChat && m.hasSession() && hasInProgressTodo(m.session.Todos) && m.todoIsSpinning {
			var cmd tea.Cmd
			m.todoSpinner, cmd = m.todoSpinner.Update(msg)
			if cmd != nil {
				m.renderPills()
				cmds = append(cmds, cmd)
			}
		}

	case tea.KeyPressMsg:
		if cmd := m.handleKeyPressMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.PasteMsg:
		if cmd := m.handlePasteMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case openEditorMsg:
		prevHeight := m.textarea.Height()
		m.textarea.SetValue(msg.Text)
		m.textarea.MoveToEnd()
		cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))
	case hyperRefreshDoneMsg:
		if cmd := m.handleSelectModel(msg.action); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case creditsUpdatedMsg:
		m.hyperCredits = &msg.credits
	case searchDebounceMsg:
		if cmd := m.handleSearchDebounce(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case searchResultMsg:
		if cmd := m.handleSearchResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case forkCompletedMsg:
		// Fork finished — close the progress dialog and switch to the new
		// session.
		m.dialog.CloseDialog(dialog.ForkProgressID)
		infoText := fmt.Sprintf("Forked to session: %s", msg.newSession.Title)
		if msg.worktree != nil {
			infoText = fmt.Sprintf("Forked to session: %s (worktree: %s)", msg.newSession.Title, msg.worktree.Name)
		}
		cmds = append(cmds, m.loadSession(msg.newSession.ID), util.ReportInfo(infoText))
		// Prepopulate the input bar with the fork-point message so the user
		// can edit and re-send it.
		if msg.prefillText != "" {
			prevHeight := m.textarea.Height()
			m.textarea.SetValue(msg.prefillText)
			m.textarea.MoveToEnd()
			cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))
		}
	case forkFailedMsg:
		// Fork failed — close the progress dialog and surface the error.
		m.dialog.CloseDialog(dialog.ForkProgressID)
		cmds = append(cmds, util.ReportError(msg.err))
	case dialog.ActionOpenForkDialog:
		// Handle fork dialog action from user message key handler.
		if cmd := m.openForkDialog(msg.SessionID, msg.MessageID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case util.InfoMsg:
		if msg.Type == util.InfoTypeError {
			slog.Error("Error reported", "error", msg.Msg)
		}
		m.status.SetInfoMsg(msg)
		ttl := msg.TTL
		if ttl <= 0 {
			ttl = DefaultStatusTTL
		}
		cmds = append(cmds, clearInfoMsgCmd(ttl))
	case app.UpdateAvailableMsg:
		text := fmt.Sprintf("Crush update available: v%s → v%s.", msg.CurrentVersion, msg.LatestVersion)
		if msg.IsDevelopment {
			text = fmt.Sprintf("This is a development version of Crush. The latest version is v%s.", msg.LatestVersion)
		}
		ttl := 10 * time.Second
		m.status.SetInfoMsg(util.InfoMsg{
			Type: util.InfoTypeUpdate,
			Msg:  text,
			TTL:  ttl,
		})
		cmds = append(cmds, clearInfoMsgCmd(ttl))
	case util.ClearStatusMsg:
		m.status.ClearInfoMsg()
	case completions.CompletionItemsLoadedMsg:
		if m.completionsOpen {
			m.completions.SetItems(msg.Files, msg.Resources)
		}
	case uv.KittyGraphicsEvent:
		if !bytes.HasPrefix(msg.Payload, []byte("OK")) {
			slog.Warn("Unexpected Kitty graphics response",
				"response", string(msg.Payload),
				"options", msg.Options)
		}
	default:
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// This logic gets triggered on any message type, but should it?
	switch m.focus {
	case uiFocusMain:
	case uiFocusEditor:
		// Textarea placeholder logic
		if m.isAgentBusy() {
			m.textarea.Placeholder = m.workingPlaceholder
		} else {
			m.textarea.Placeholder = m.readyPlaceholder
		}
		if m.yoloMode {
			m.textarea.Placeholder = "Yolo mode!"
		}
	}

	// at this point this can only handle [message.Attachment] message, and we
	// should return all cmds anyway.
	_ = m.attachments.Update(msg)
	return m, tea.Batch(cmds...)
}
