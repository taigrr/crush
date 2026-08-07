package model

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/fork"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
)

func (m *UI) openDialog(id string) tea.Cmd {
	var cmds []tea.Cmd
	switch id {
	case dialog.SessionsID:
		if cmd := m.openSessionsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ModelsID:
		if cmd := m.openModelsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.CommandsID:
		if cmd := m.openCommandsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ReasoningID:
		if cmd := m.openReasoningDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ThemeID:
		if cmd := m.openThemeDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.NotificationsID:
		if cmd := m.openNotificationsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.EmbeddingsID:
		if cmd := m.openEmbeddingsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.FilePickerID:
		if cmd := m.openFilesDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.MilestonesID:
		if cmd := m.openMilestonesDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.QuitID:
		if cmd := m.openQuitDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	default:
		// Unknown dialog
		break
	}
	return tea.Batch(cmds...)
}

// openQuitDialog opens the quit confirmation dialog.
func (m *UI) openQuitDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.QuitID) {
		// Bring to front
		m.dialog.BringToFront(dialog.QuitID)
		return nil
	}

	quitDialog := dialog.NewQuit(m.com)
	m.dialog.OpenDialog(quitDialog)
	return nil
}

// openModelsDialog opens the models dialog.
func (m *UI) openModelsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.ModelsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.ModelsID)
		return nil
	}

	isOnboarding := m.state == uiOnboarding
	modelsDialog, err := dialog.NewModels(m.com, isOnboarding)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(modelsDialog)

	return nil
}

// openCommandsDialog opens the commands dialog.
func (m *UI) openCommandsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.CommandsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.CommandsID)
		return nil
	}

	var sessionID string
	hasSession := m.session != nil
	if hasSession {
		sessionID = m.session.ID
	}
	hasTodos := hasSession && hasIncompleteTodos(m.session.Todos)
	hasQueue := m.promptQueue > 0

	commands, err := dialog.NewCommands(m.com, sessionID, hasSession, hasTodos, hasQueue, m.customCommands, m.mcpPrompts)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(commands)

	return commands.InitialCmd()
}

// openReasoningDialog opens the reasoning effort dialog.
func (m *UI) openReasoningDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.ReasoningID) {
		m.dialog.BringToFront(dialog.ReasoningID)
		return nil
	}

	reasoningDialog, err := dialog.NewReasoning(m.com)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(reasoningDialog)
	return nil
}

// openThemeDialog opens the theme picker dialog. It captures the current
// styles so an esc/cancel can restore them after live previews.
func (m *UI) openThemeDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.ThemeID) {
		m.dialog.BringToFront(dialog.ThemeID)
		return nil
	}

	themeDialog, err := dialog.NewTheme(m.com)
	if err != nil {
		return util.ReportError(err)
	}

	original := *m.com.Styles
	m.themePreviewOriginal = &original
	m.dialog.OpenDialog(themeDialog)
	return nil
}

// openNotificationsDialog opens the notification style picker dialog.
func (m *UI) openNotificationsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.NotificationsID) {
		m.dialog.BringToFront(dialog.NotificationsID)
		return nil
	}

	notificationsDialog := dialog.NewNotifications(m.com)
	m.dialog.OpenDialog(notificationsDialog)
	return nil
}

// openEmbeddingsDialog opens the embedding model picker dialog.
func (m *UI) openEmbeddingsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.EmbeddingsID) {
		m.dialog.BringToFront(dialog.EmbeddingsID)
		return nil
	}

	embeddingsDialog := dialog.NewEmbeddings(m.com)
	m.dialog.OpenDialog(embeddingsDialog)
	return nil
}

// openSessionsDialog opens the sessions dialog. If the dialog is already open,
// it brings it to the front. Otherwise, it will list all the sessions and open
// the dialog.
func (m *UI) openSessionsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SessionsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.SessionsID)
		return nil
	}

	selectedSessionID := ""
	if m.session != nil {
		selectedSessionID = m.session.ID
	}

	dialog, err := dialog.NewSessions(m.com, selectedSessionID)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(dialog)
	return nil
}

// openMilestonesDialog opens the milestones dialog.
func (m *UI) openMilestonesDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.MilestonesID) {
		m.dialog.BringToFront(dialog.MilestonesID)
		return nil
	}

	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}

	d, err := dialog.NewMilestones(m.com, sessionID)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(d)
	return nil
}

// openFilesDialog opens the file picker dialog.
func (m *UI) openFilesDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.FilePickerID) {
		// Bring to front
		m.dialog.BringToFront(dialog.FilePickerID)
		return nil
	}

	filePicker, cmd := dialog.NewFilePicker(m.com)
	filePicker.SetImageCapabilities(&m.caps)
	m.dialog.OpenDialog(filePicker)

	return cmd
}

// openSnapshotsDialog opens the snapshots dialog.
func (m *UI) openSnapshotsDialog(sessionID string) tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SnapshotsID) {
		m.dialog.BringToFront(dialog.SnapshotsID)
		return nil
	}

	snapshotsDialog, err := dialog.NewSnapshots(m.com, sessionID)
	if err != nil {
		return util.ReportError(err)
	}
	m.dialog.OpenDialog(snapshotsDialog)
	return nil
}

// restoreSnapshot restores the filesystem to a specific snapshot.
func (m *UI) restoreSnapshot(snapshotID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.RestoreSnapshot(context.Background(), snapshotID); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg("Snapshot restored")
	}
}

// openWorktreesDialog opens the worktrees dialog.
func (m *UI) openWorktreesDialog(sessionID string) tea.Cmd {
	if m.dialog.ContainsDialog(dialog.WorktreesID) {
		m.dialog.BringToFront(dialog.WorktreesID)
		return nil
	}

	worktreesDialog, err := dialog.NewWorktrees(m.com, sessionID)
	if err != nil {
		return util.ReportError(err)
	}
	m.dialog.OpenDialog(worktreesDialog)
	return nil
}

// createWorktree creates a new worktree.
func (m *UI) createWorktree(sessionID, name, fromSnapshotID string) tea.Cmd {
	return func() tea.Msg {
		wt, err := m.com.Workspace.CreateWorktree(context.Background(), sessionID, name, fromSnapshotID)
		if err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg("Worktree created: " + wt.Name)
	}
}

// switchWorktree switches to a different worktree.
func (m *UI) switchWorktree(sessionID, worktreeID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.SwitchWorktree(context.Background(), sessionID, worktreeID); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg("Switched worktree")
	}
}

// runSnapshotGC runs garbage collection on snapshots.
func (m *UI) runSnapshotGC() tea.Cmd {
	return func() tea.Msg {
		freed, err := m.com.Workspace.SnapshotGC(context.Background())
		if err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(fmt.Sprintf("Garbage collection freed %s", formatBytes(freed)))
	}
}

// openMergeWorktreeDialog opens the merge worktree dialog.
func (m *UI) openMergeWorktreeDialog(worktreeID, worktreeName string) tea.Cmd {
	if m.dialog.ContainsDialog(dialog.MergeWorktreeID) {
		m.dialog.BringToFront(dialog.MergeWorktreeID)
		return nil
	}

	mergeDialog, err := dialog.NewMergeWorktree(m.com, worktreeID, worktreeName)
	if err != nil {
		return util.ReportError(err)
	}
	m.dialog.OpenDialog(mergeDialog)
	return nil
}

// mergeWorktree merges a worktree onto a target branch.
func (m *UI) mergeWorktree(worktreeID, targetBranch string, rebase bool) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.MergeWorktree(context.Background(), worktreeID, targetBranch, rebase); err != nil {
			return util.ReportError(err)()
		}
		action := "Merged"
		if rebase {
			action = "Rebased"
		}
		return util.NewInfoMsg(fmt.Sprintf("%s worktree onto %s", action, targetBranch))
	}
}

// openForkDialog opens the fork dialog for a specific message.
func (m *UI) openForkDialog(sessionID, messageID string) tea.Cmd {
	if m.dialog.ContainsDialog(dialog.ForkID) {
		m.dialog.BringToFront(dialog.ForkID)
		return nil
	}

	// Generate default title from current session.
	defaultTitle := "Fork"
	if m.session != nil && m.session.Title != "" {
		defaultTitle = m.session.Title + " (fork)"
	}

	forkDialog := dialog.NewFork(m.com, sessionID, messageID, defaultTitle)
	m.dialog.OpenDialog(forkDialog)
	return nil
}

// forkConversation forks the conversation from a specific message.
func (m *UI) forkConversation(sessionID, messageID, newTitle string, createWorktree bool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		result, err := m.com.Workspace.ForkConversation(ctx, fork.ForkParams{
			SessionID:      sessionID,
			MessageID:      messageID,
			CreateWorktree: createWorktree,
			Title:          newTitle,
		})
		if err != nil {
			return forkFailedMsg{err: fmt.Errorf("fork failed: %w", err)}
		}

		// Return a message to switch to the new session.
		return forkCompletedMsg{
			newSession:  result.NewSession,
			worktree:    result.Worktree,
			prefillText: result.PrefillText,
		}
	}
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// openPermissionsDialog opens the permissions dialog for a permission request.
func (m *UI) openPermissionsDialog(perm permission.PermissionRequest) tea.Cmd {
	// Close any existing permissions dialog first.
	m.dialog.CloseDialog(dialog.PermissionsID)

	// Get diff mode from config.
	var opts []dialog.PermissionsOption
	if cfg := m.com.Config(); cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil {
		if diffMode := cfg.Options.TUI.DiffMode; diffMode != "" {
			opts = append(opts, dialog.WithDiffMode(diffMode == "split"))
		}
	}

	permDialog := dialog.NewPermissions(m.com, perm, opts...)
	m.dialog.OpenDialogWithGrace(permDialog)
	return nil
}

// syncPermissionDialogForSession reconciles the permissions dialog with
// the currently active session. It must be called whenever the active
// session changes. It closes an open permissions dialog that belongs to
// a different session, and re-surfaces the cached pending request when
// it belongs to the now-active session. This prevents a prompt for one
// session from being shown — and acted on — while the user is viewing
// another.
func (m *UI) syncPermissionDialogForSession() tea.Cmd {
	activeID := ""
	if m.session != nil {
		activeID = m.session.ID
	}

	// Close a stale dialog that belongs to a different session.
	if d := m.dialog.Dialog(dialog.PermissionsID); d != nil {
		if perm, ok := d.(*dialog.Permissions); ok && perm.SessionID() != activeID {
			m.dialog.CloseDialog(dialog.PermissionsID)
		}
	}

	// Re-surface the pending request if it belongs to the active session
	// and no dialog is currently shown for it.
	if m.pendingPermission == nil || activeID == "" || m.pendingPermission.SessionID != activeID {
		return nil
	}
	if d := m.dialog.Dialog(dialog.PermissionsID); d != nil {
		if perm, ok := d.(*dialog.Permissions); ok && perm.ToolCallID() == m.pendingPermission.ToolCallID {
			return nil // already showing it
		}
	}
	return m.openPermissionsDialog(*m.pendingPermission)
}

// openQuestionDialog opens the question dialog for a question request.
func (m *UI) openQuestionDialog(req question.Request) tea.Cmd {
	// Close any existing question dialog first.
	m.dialog.CloseDialog(dialog.QuestionID)

	m.dialog.OpenDialogWithGrace(dialog.NewQuestion(m.com, req))
	return nil
}

// syncQuestionDialogForSession is the question-tool analog of
// syncPermissionDialogForSession: it reconciles the question dialog
// with the currently active session, closing a stale dialog for a
// different session and re-surfacing the cached pending request when
// the user switches back to the session it belongs to.
func (m *UI) syncQuestionDialogForSession() tea.Cmd {
	activeID := ""
	if m.session != nil {
		activeID = m.session.ID
	}

	if d := m.dialog.Dialog(dialog.QuestionID); d != nil {
		if q, ok := d.(*dialog.Question); ok && q.SessionID() != activeID {
			m.dialog.CloseDialog(dialog.QuestionID)
		}
	}

	if m.pendingQuestion == nil || activeID == "" || m.pendingQuestion.SessionID != activeID {
		return nil
	}
	if d := m.dialog.Dialog(dialog.QuestionID); d != nil {
		if q, ok := d.(*dialog.Question); ok && q.ToolCallID() == m.pendingQuestion.ToolCallID {
			return nil // already showing it
		}
	}
	return m.openQuestionDialog(*m.pendingQuestion)
}

// handlePermissionNotification updates tool items when permission state changes.
