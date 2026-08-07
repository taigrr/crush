package dialog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/util"
)

// SessionsID is the identifier for the session selector dialog.
const SessionsID = "session"

type sessionsMode uint8

// Possible modes a session item can be in
const (
	sessionsModeNormal sessionsMode = iota
	sessionsModeDeleting
	sessionsModeUpdating
	sessionsModeArchiving
	sessionsModeUnarchiving
	// sessionsModeSelecting is vim-style multi-select mode: j/k/g/G move,
	// v toggles a contiguous visual sweep, space toggles the current row,
	// a bulk-archives the selection, esc exits and clears. It is a distinct
	// mode because the picker's normal mode feeds printable keys to the
	// filter input, so bare v/space/a cannot be used there.
	sessionsModeSelecting
)

// Session is a session selector dialog.
type Session struct {
	com                *common.Common
	help               help.Model
	list               *list.FilterableList
	input              textinput.Model
	selectedSessionInx int
	sessions           []session.Session
	archivedSessions   []session.Session
	archivedStartIndex int // index where archived section starts (-1 if no archived)

	sessionsMode sessionsMode

	// activeSessionID is the session currently open in the main pane. It is
	// excluded from bulk archive (archiving the active session is the job of
	// the sidebar's ctrl+x modal, not this picker).
	activeSessionID string
	// selection holds the vim-style multi-select state for sessionsModeSelecting.
	selection common.MultiSelect
	// activeItems mirrors the active-session list items (in order) so their
	// ✓ marks can be synced from the selection without rebuilding the list.
	activeItems []*SessionItem

	keyMap struct {
		Select           key.Binding
		Next             key.Binding
		Previous         key.Binding
		UpDown           key.Binding
		Delete           key.Binding
		Rename           key.Binding
		Archive          key.Binding
		Unarchive        key.Binding
		ConfirmRename    key.Binding
		CancelRename     key.Binding
		ConfirmDelete    key.Binding
		CancelDelete     key.Binding
		ConfirmArchive   key.Binding
		CancelArchive    key.Binding
		ConfirmUnarchive key.Binding
		CancelUnarchive  key.Binding
		Close            key.Binding
		// Multi-select (sessionsModeSelecting).
		EnterSelect   key.Binding
		VisualToggle  key.Binding
		ToggleSelect  key.Binding
		SelectUp      key.Binding
		SelectDown    key.Binding
		Top           key.Binding
		Bottom        key.Binding
		ArchiveSelect key.Binding
		ExitSelect    key.Binding
	}
}

var _ Dialog = (*Session)(nil)

// NewSessions creates a new Session dialog.
func NewSessions(com *common.Common, selectedSessionID string) (*Session, error) {
	s := new(Session)
	s.sessionsMode = sessionsModeNormal
	s.com = com
	s.archivedStartIndex = -1
	s.activeSessionID = selectedSessionID

	sessions, err := com.Workspace.ListSessions(context.TODO())
	if err != nil {
		return nil, err
	}
	s.sessions = sessions

	archivedSessions, err := com.Workspace.ListArchivedSessions(context.TODO())
	if err != nil {
		// Non-fatal: just show active sessions
		archivedSessions = nil
	}
	s.archivedSessions = archivedSessions

	for i, sess := range sessions {
		if sess.ID == selectedSessionID {
			s.selectedSessionInx = i
			break
		}
	}

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()

	s.help = help
	s.list = list.NewFilterableList(s.buildListItems(sessionsModeNormal)...)
	s.list.Focus()
	s.list.SetSelected(s.selectedSessionInx)

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
	s.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑↓", "choose"),
	)
	s.keyMap.Delete = key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "delete"),
	)
	s.keyMap.Rename = key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r", "rename"),
	)
	s.keyMap.Archive = key.NewBinding(
		key.WithKeys("ctrl+a"),
		key.WithHelp("ctrl+a", "archive"),
	)
	s.keyMap.Unarchive = key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "unarchive"),
	)
	s.keyMap.ConfirmRename = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	)
	s.keyMap.CancelRename = key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	)
	s.keyMap.ConfirmDelete = key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "delete"),
	)
	s.keyMap.CancelDelete = key.NewBinding(
		key.WithKeys("n", "esc"),
		key.WithHelp("n", "cancel"),
	)
	s.keyMap.ConfirmArchive = key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "archive"),
	)
	s.keyMap.CancelArchive = key.NewBinding(
		key.WithKeys("n", "esc"),
		key.WithHelp("n", "cancel"),
	)
	s.keyMap.ConfirmUnarchive = key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "unarchive"),
	)
	s.keyMap.CancelUnarchive = key.NewBinding(
		key.WithKeys("n", "esc"),
		key.WithHelp("n", "cancel"),
	)
	s.keyMap.Close = CloseKey

	// Multi-select mode. Entered with ctrl+v from normal mode (a chord so
	// it never collides with the filter input); once inside, the sidebar's
	// bare keys apply.
	s.keyMap.EnterSelect = key.NewBinding(
		key.WithKeys("ctrl+v"),
		key.WithHelp("ctrl+v", "select"),
	)
	s.keyMap.VisualToggle = key.NewBinding(
		key.WithKeys("v"),
		key.WithHelp("v", "visual"),
	)
	s.keyMap.ToggleSelect = key.NewBinding(
		key.WithKeys("space", " "),
		key.WithHelp("space", "toggle"),
	)
	s.keyMap.SelectUp = key.NewBinding(
		key.WithKeys("up", "ctrl+k", "k"),
		key.WithHelp("↑/k", "up"),
	)
	s.keyMap.SelectDown = key.NewBinding(
		key.WithKeys("down", "ctrl+j", "j"),
		key.WithHelp("↓/j", "down"),
	)
	s.keyMap.Top = key.NewBinding(
		key.WithKeys("g", "home"),
		key.WithHelp("g", "top"),
	)
	s.keyMap.Bottom = key.NewBinding(
		key.WithKeys("G", "end"),
		key.WithHelp("G", "bottom"),
	)
	s.keyMap.ArchiveSelect = key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "archive selected"),
	)
	s.keyMap.ExitSelect = key.NewBinding(
		key.WithKeys("esc", "ctrl+v"),
		key.WithHelp("esc", "exit select"),
	)

	return s, nil
}

// ID implements Dialog.
func (s *Session) ID() string {
	return SessionsID
}

// HandleMsg implements Dialog.
func (s *Session) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case pickerBulkArchivedMsg:
		return s.handleBulkArchived(msg)
	case tea.KeyPressMsg:
		switch s.sessionsMode {
		case sessionsModeDeleting:
			switch {
			case key.Matches(msg, s.keyMap.ConfirmDelete):
				action := s.confirmDeleteSession()
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
				s.list.SelectFirst()
				s.list.ScrollToSelected()
				return action
			case key.Matches(msg, s.keyMap.CancelDelete):
				s.sessionsMode = sessionsModeNormal
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
			}
		case sessionsModeUpdating:
			switch {
			case key.Matches(msg, s.keyMap.ConfirmRename):
				action := s.confirmRenameSession()
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
				return action
			case key.Matches(msg, s.keyMap.CancelRename):
				s.sessionsMode = sessionsModeNormal
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
			default:
				item := s.list.SelectedItem()
				if item == nil {
					return nil
				}
				if sessionItem, ok := item.(*SessionItem); ok {
					return sessionItem.HandleInput(msg)
				}
			}
		case sessionsModeArchiving:
			switch {
			case key.Matches(msg, s.keyMap.ConfirmArchive):
				action := s.confirmArchiveSession()
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
				s.list.SelectFirst()
				s.list.ScrollToSelected()
				return action
			case key.Matches(msg, s.keyMap.CancelArchive):
				s.sessionsMode = sessionsModeNormal
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
			}
		case sessionsModeUnarchiving:
			switch {
			case key.Matches(msg, s.keyMap.ConfirmUnarchive):
				action := s.confirmUnarchiveSession()
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
				s.list.SelectFirst()
				s.list.ScrollToSelected()
				return action
			case key.Matches(msg, s.keyMap.CancelUnarchive):
				s.sessionsMode = sessionsModeNormal
				s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
			}
		case sessionsModeSelecting:
			return s.handleSelectModeKey(msg)
		default:
			switch {
			case key.Matches(msg, s.keyMap.Close):
				return ActionClose{}
			case key.Matches(msg, s.keyMap.Rename):
				if s.isSelectedSeparator() || s.isSelectedArchived() {
					return nil
				}
				s.sessionsMode = sessionsModeUpdating
				s.list.SetItems(s.buildListItems(sessionsModeUpdating)...)
			case key.Matches(msg, s.keyMap.Delete):
				if s.isSelectedSeparator() {
					return nil
				}
				if s.isCurrentSessionBusy() {
					return ActionCmd{util.ReportWarn("Agent is busy, please wait...")}
				}
				s.sessionsMode = sessionsModeDeleting
				s.list.SetItems(s.buildListItems(sessionsModeDeleting)...)
			case key.Matches(msg, s.keyMap.Archive):
				if s.isSelectedSeparator() || s.isSelectedArchived() {
					return nil
				}
				if s.isCurrentSessionBusy() {
					return ActionCmd{util.ReportWarn("Agent is busy, please wait...")}
				}
				s.sessionsMode = sessionsModeArchiving
				s.list.SetItems(s.buildListItems(sessionsModeArchiving)...)
			case key.Matches(msg, s.keyMap.Unarchive):
				if !s.isSelectedArchived() {
					return nil
				}
				s.sessionsMode = sessionsModeUnarchiving
				s.list.SetItems(s.buildListItems(sessionsModeUnarchiving)...)
			case key.Matches(msg, s.keyMap.Previous):
				s.list.Focus()
				if s.list.IsSelectedFirst() {
					s.list.SelectLast()
				} else {
					s.list.SelectPrev()
				}
				// Skip separator
				if s.isSelectedSeparator() {
					s.list.SelectPrev()
				}
				s.list.ScrollToSelected()
				return s.previewAction()
			case key.Matches(msg, s.keyMap.Next):
				s.list.Focus()
				if s.list.IsSelectedLast() {
					s.list.SelectFirst()
				} else {
					s.list.SelectNext()
				}
				// Skip separator
				if s.isSelectedSeparator() {
					s.list.SelectNext()
				}
				s.list.ScrollToSelected()
				return s.previewAction()
			case key.Matches(msg, s.keyMap.EnterSelect):
				// Enter vim-style multi-select mode (only when there are
				// active, non-archived sessions to select).
				if len(s.sessions) == 0 {
					return nil
				}
				s.enterSelectMode()
			case key.Matches(msg, s.keyMap.Select):
				if s.isSelectedSeparator() {
					return nil
				}
				if item := s.list.SelectedItem(); item != nil {
					if sessionItem, ok := item.(*SessionItem); ok {
						return ActionSelectSession{sessionItem.Session}
					}
				}
			default:
				var cmd tea.Cmd
				s.input, cmd = s.input.Update(msg)
				value := s.input.Value()
				s.list.SetFilter(value)
				s.list.ScrollToTop()
				s.list.SetSelected(0)
				return ActionCmd{cmd}
			}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (s *Session) Cursor() *tea.Cursor {
	return InputCursor(s.com.Styles, s.input.Cursor())
}

// Draw implements [Dialog].
func (s *Session) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
	s.input.SetWidth(max(0, innerWidth-t.Dialog.InputPrompt.GetHorizontalFrameSize()-1)) // (1) cursor padding
	listHeight := height - heightOffset
	listTotalHeight := s.list.TotalHeight()
	listWidth := max(0, innerWidth-3) // Reserve space for scrollbar.
	s.list.SetSize(listWidth, listHeight)
	s.help.SetWidth(innerWidth)

	// This makes it so we do not scroll the list if we don't have to
	start, end := s.list.VisibleItemIndices()

	// if selected index is outside visible range, scroll to it
	if s.selectedSessionInx < start || s.selectedSessionInx > end {
		s.list.ScrollToSelected()
	}

	var cur *tea.Cursor
	rc := NewRenderContext(t, width)
	rc.Title = "Sessions"
	switch s.sessionsMode {
	case sessionsModeDeleting:
		rc.TitleStyle = t.Dialog.Sessions.DeletingTitle
		rc.TitleGradientFromColor = t.Dialog.Sessions.DeletingTitleGradientFromColor
		rc.TitleGradientToColor = t.Dialog.Sessions.DeletingTitleGradientToColor
		rc.ViewStyle = t.Dialog.Sessions.DeletingView
		rc.AddPart(t.Dialog.Sessions.DeletingMessage.Render("Delete this session?"))
	case sessionsModeArchiving:
		rc.TitleStyle = t.Dialog.Sessions.ArchivingTitle
		rc.TitleGradientFromColor = t.Dialog.Sessions.ArchivingTitleGradientFromColor
		rc.TitleGradientToColor = t.Dialog.Sessions.ArchivingTitleGradientToColor
		rc.ViewStyle = t.Dialog.Sessions.ArchivingView
		rc.AddPart(t.Dialog.Sessions.ArchivingMessage.Render("Archive this session?"))
	case sessionsModeSelecting:
		// Multi-select mode: no filter input; show a count hint. Reuse the
		// archiving title styling for a consistent "bulk archive" affordance.
		rc.TitleStyle = t.Dialog.Sessions.ArchivingTitle
		rc.TitleGradientFromColor = t.Dialog.Sessions.ArchivingTitleGradientFromColor
		rc.TitleGradientToColor = t.Dialog.Sessions.ArchivingTitleGradientToColor
		rc.ViewStyle = t.Dialog.Sessions.ArchivingView
		hint := "space toggle · v visual · a archive · esc cancel"
		if n := s.selection.Count(); n > 0 {
			hint = fmt.Sprintf("%d selected · %s", n, hint)
		}
		rc.AddPart(t.Dialog.Sessions.ArchivingMessage.Render(hint))
	case sessionsModeUpdating:
		rc.TitleStyle = t.Dialog.Sessions.RenamingingTitle
		rc.TitleGradientFromColor = t.Dialog.Sessions.RenamingTitleGradientFromColor
		rc.TitleGradientToColor = t.Dialog.Sessions.RenamingTitleGradientToColor
		rc.ViewStyle = t.Dialog.Sessions.RenamingView
		message := t.Dialog.Sessions.RenamingingMessage.Render("Rename this session?")
		rc.AddPart(message)
		item := s.selectedSessionItem()
		if item == nil {
			return nil
		}
		cur = item.Cursor()
		if cur == nil {
			break
		}

		start, end := s.list.VisibleItemIndices()
		selectedIndex := s.list.Selected()

		titleStyle := t.Dialog.Sessions.RenamingingTitle
		dialogStyle := t.Dialog.Sessions.RenamingView
		inputStyle := t.Dialog.InputPrompt

		// Adjust cursor position to account for dialog layout + message
		cur.X += inputStyle.GetBorderLeftSize() +
			inputStyle.GetMarginLeft() +
			inputStyle.GetPaddingLeft() +
			dialogStyle.GetBorderLeftSize() +
			dialogStyle.GetPaddingLeft() +
			dialogStyle.GetMarginLeft()
		cur.Y += titleStyle.GetVerticalFrameSize() +
			inputStyle.GetBorderTopSize() +
			inputStyle.GetMarginTop() +
			inputStyle.GetPaddingTop() +
			inputStyle.GetBorderBottomSize() +
			inputStyle.GetMarginBottom() +
			inputStyle.GetPaddingBottom() +
			dialogStyle.GetPaddingTop() +
			dialogStyle.GetBorderTopSize() +
			lipgloss.Height(message) - 1

		// move the cursor by one down until we see the selectedIndex
		for ; start <= end && start != selectedIndex && selectedIndex > -1; start++ {
			cur.Y += 1
		}
	default:
		inputView := t.Dialog.InputPrompt.Render(s.input.View())
		cur = s.Cursor()
		rc.AddPart(inputView)
	}
	listView := t.Dialog.List.Height(s.list.Height()).Render(s.list.Render())
	scrollbar := common.Scrollbar(t, listHeight, listTotalHeight, listHeight, s.list.Offset())
	if scrollbar != "" {
		listView = lipgloss.JoinHorizontal(lipgloss.Top, listView, scrollbar)
	}
	rc.AddPart(listView)
	rc.Help = s.help.View(s)

	view := rc.Render()

	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// previewAction returns an ActionPreviewSession for the currently selected
// session so the model can live-preview it, or nil when the selection is not
// a session (separator/none). All picker sessions are current-workspace, so
// no workspace scoping is needed here.
func (s *Session) previewAction() Action {
	if s.isSelectedSeparator() {
		return nil
	}
	item := s.list.SelectedItem()
	if item == nil {
		return nil
	}
	si, ok := item.(*SessionItem)
	if !ok {
		return nil
	}
	return ActionPreviewSession{SessionID: si.ID()}
}

func (s *Session) selectedSessionItem() *SessionItem {
	if item := s.list.SelectedItem(); item != nil {
		return item.(*SessionItem)
	}
	return nil
}

// enterSelectMode switches into vim-style multi-select. The filter is
// cleared so the visible list order matches s.sessions (the active region)
// and no separator sits mid-region, and the cursor is snapped onto the first
// active session.
func (s *Session) enterSelectMode() {
	s.sessionsMode = sessionsModeSelecting
	s.selection.Clear()
	s.input.SetValue("")
	s.list.SetFilter("")
	s.list.SetItems(s.buildListItems(sessionsModeSelecting)...)
	if s.list.Selected() < 0 || s.list.Selected() >= len(s.sessions) {
		s.list.SetSelected(0)
	}
	s.list.ScrollToSelected()
	s.syncSelectionMarks()
}

// exitSelectMode leaves multi-select, clears the selection, and returns to
// normal (filter) mode.
func (s *Session) exitSelectMode() {
	s.selection.Clear()
	s.sessionsMode = sessionsModeNormal
	s.list.SetItems(s.buildListItems(sessionsModeNormal)...)
	s.syncSelectionMarks()
}

// activeSessionOrder returns the active (non-archived) session IDs in list
// order, used as the contiguous domain for visual sweeps.
func (s *Session) activeSessionOrder() []string {
	ids := make([]string, len(s.sessions))
	for i, sess := range s.sessions {
		ids[i] = sess.ID
	}
	return ids
}

// currentSelectID returns the session ID under the cursor while in select
// mode, or "" if the cursor is not on an active session row.
func (s *Session) currentSelectID() string {
	idx := s.list.Selected()
	if idx < 0 || idx >= len(s.sessions) {
		return ""
	}
	return s.sessions[idx].ID
}

// activeCount is the number of active (non-archived) sessions, i.e. the
// upper bound (exclusive) for the select-mode cursor.
func (s *Session) activeCount() int {
	return len(s.sessions)
}

// handleSelectModeKey processes keys while in vim-style multi-select mode.
func (s *Session) handleSelectModeKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, s.keyMap.ExitSelect):
		s.exitSelectMode()
		return nil
	case key.Matches(msg, s.keyMap.VisualToggle):
		s.selection.ToggleVisual(s.currentSelectID())
		s.syncSelectionMarks()
		return nil
	case key.Matches(msg, s.keyMap.ToggleSelect):
		s.selection.Toggle(s.currentSelectID())
		s.syncSelectionMarks()
		return nil
	case key.Matches(msg, s.keyMap.ArchiveSelect):
		return s.archiveSelected()
	case key.Matches(msg, s.keyMap.Top):
		s.list.SetSelected(0)
		s.list.ScrollToSelected()
		s.selection.ExtendVisual(s.activeSessionOrder(), s.currentSelectID())
		s.syncSelectionMarks()
		return nil
	case key.Matches(msg, s.keyMap.Bottom):
		if n := s.activeCount(); n > 0 {
			s.list.SetSelected(n - 1)
			s.list.ScrollToSelected()
		}
		s.selection.ExtendVisual(s.activeSessionOrder(), s.currentSelectID())
		s.syncSelectionMarks()
		return nil
	case key.Matches(msg, s.keyMap.SelectUp):
		if s.list.Selected() > 0 {
			s.list.SelectPrev()
			s.list.ScrollToSelected()
		}
		s.selection.ExtendVisual(s.activeSessionOrder(), s.currentSelectID())
		s.syncSelectionMarks()
		return nil
	case key.Matches(msg, s.keyMap.SelectDown):
		if s.list.Selected() < s.activeCount()-1 {
			s.list.SelectNext()
			s.list.ScrollToSelected()
		}
		s.selection.ExtendVisual(s.activeSessionOrder(), s.currentSelectID())
		s.syncSelectionMarks()
		return nil
	}
	return nil
}

// syncSelectionMarks prunes the selection to currently-known active sessions
// (dropping stale ids from a re-anchored visual sweep or a background
// refresh, so phantom ids never reach archivableSelection or the count
// hint) and pushes the selection set onto the active list items' ✓ marks.
func (s *Session) syncSelectionMarks() {
	known := make(map[string]bool, len(s.sessions))
	for _, sess := range s.sessions {
		known[sess.ID] = true
	}
	s.selection.Retain(known)
	for _, it := range s.activeItems {
		it.SetMarked(s.selection.Selected(it.ID()))
	}
}

// archivableSelection returns the selected session IDs eligible for bulk
// archive, deterministically sorted, plus the number skipped. A session is
// skipped when it is the active session (archiving it is the sidebar ctrl+x
// modal's job). All picker sessions come from ListSessions (current
// workspace only), so workspace scoping is inherently satisfied; unknown ids
// are also filtered defensively.
func (s *Session) archivableSelection() (ids []string, skipped int) {
	known := make(map[string]bool, len(s.sessions))
	for _, sess := range s.sessions {
		known[sess.ID] = true
	}
	for _, id := range s.selection.IDs() {
		if id == s.activeSessionID || !known[id] {
			skipped++
			continue
		}
		ids = append(ids, id)
	}
	return ids, skipped
}

// pickerBulkArchivedMsg carries the outcome of a bulk archive back into the
// dialog so it can reconcile local state: succeeded ids move to the archived
// section, failed ids stay active and are re-selected for retry. The dialog
// receives this because Overlay.Update forwards every message (not just key
// presses) to the front dialog's HandleMsg while it is open.
type pickerBulkArchivedMsg struct {
	succeeded []string
	failed    []string
	skipped   int
}

// archiveSelected bulk-archives the current selection. Unlike the optimistic
// single-archive path, it does NOT move sessions locally up front: the
// archive command runs, and the resulting pickerBulkArchivedMsg reconciles
// state (succeeded → archived, failed stay active + re-selected). This keeps
// failed archives visible and retryable rather than silently vanishing.
func (s *Session) archiveSelected() Action {
	toArchive, skipped := s.archivableSelection()
	if len(toArchive) == 0 {
		s.exitSelectMode()
		if skipped > 0 {
			return ActionCmd{util.ReportWarn("Selected sessions can't be archived from here; nothing to archive")}
		}
		return ActionCmd{util.ReportInfo("No sessions selected")}
	}
	// Busy guard checks the sessions actually being archived (not the
	// arbitrary cursor row). If any is mid-run, warn and stay in select mode
	// with the selection intact so the user can retry once it settles
	// (intentionally asymmetric with the empty-selection branch, which has
	// nothing to retry).
	if busy := s.firstBusy(toArchive); busy != "" {
		return ActionCmd{util.ReportWarn("A selected session is busy, please wait...")}
	}
	return ActionCmd{s.archiveSessionsCmd(toArchive, skipped)}
}

// firstBusy returns the first id in ids whose agent is currently running, or
// "" if none are busy.
func (s *Session) firstBusy(ids []string) string {
	if !s.com.Workspace.AgentIsReady() {
		return ""
	}
	for _, id := range ids {
		if s.com.Workspace.AgentIsSessionBusy(id) {
			return id
		}
	}
	return ""
}

// archiveSessionsCmd archives every id (attempt-all, collecting failures)
// and returns a pickerBulkArchivedMsg so the dialog can reconcile.
func (s *Session) archiveSessionsCmd(ids []string, skipped int) tea.Cmd {
	return func() tea.Msg {
		var succeeded, failed []string
		for _, id := range ids {
			if err := s.com.Workspace.ArchiveSession(context.TODO(), id); err != nil {
				failed = append(failed, id)
				continue
			}
			succeeded = append(succeeded, id)
		}
		return pickerBulkArchivedMsg{succeeded: succeeded, failed: failed, skipped: skipped}
	}
}

// handleBulkArchived reconciles a completed bulk archive: succeeded sessions
// move from the active list into the archived section (order preserved),
// failed sessions stay active and are re-selected so the user can retry, the
// cursor is snapped back into a valid active row, and the outcome is
// reported.
func (s *Session) handleBulkArchived(msg pickerBulkArchivedMsg) Action {
	succeededSet := make(map[string]bool, len(msg.succeeded))
	for _, id := range msg.succeeded {
		succeededSet[id] = true
	}
	// Partition the active list, preserving order in both the remaining and
	// the moved-to-archived blocks (avoids the reversed-order artifact of
	// per-id prepend).
	var remaining, moved []session.Session
	for _, sess := range s.sessions {
		if succeededSet[sess.ID] {
			moved = append(moved, sess)
		} else {
			remaining = append(remaining, sess)
		}
	}
	s.sessions = remaining
	s.archivedSessions = append(moved, s.archivedSessions...)

	// If the user already left select mode (pressed esc) before this async
	// result arrived, do NOT yank them back into it: just apply the
	// archived/active reconcile and drop the selection. Only re-select and
	// stay in select mode when still in select mode.
	stillSelecting := s.sessionsMode == sessionsModeSelecting
	switch {
	case stillSelecting && len(msg.failed) > 0:
		// Keep the failures selected for retry, staying in select mode.
		// SetSelection also clears any stale visual anchor, so a post-
		// archive j/k starts a fresh sweep rather than extending from an
		// anchor that may have been archived away.
		s.selection.SetSelection(msg.failed)
		s.list.SetItems(s.buildListItems(sessionsModeSelecting)...)
	default:
		// Full success while selecting, or the user already left select
		// mode: clear the selection and rebuild in the current mode.
		s.selection.Clear()
		if stillSelecting {
			s.sessionsMode = sessionsModeNormal
		}
		s.list.SetItems(s.buildListItems(s.sessionsMode)...)
	}
	// Snap the cursor onto a valid row. On partial failure prefer the first
	// still-selected (failed) row; otherwise the first active row. Never
	// leave it parked on the "Archived" separator — after archiving ALL
	// active sessions index 0 is the separator.
	failedForCursor := msg.failed
	if !stillSelecting {
		failedForCursor = nil // not re-selecting; just snap to first active
	}
	s.snapCursorAfterArchive(failedForCursor)
	s.syncSelectionMarks()

	switch {
	case len(msg.failed) > 0:
		return ActionCmd{util.ReportError(fmt.Errorf(
			"Archived %d session(s); %d failed", len(msg.succeeded), len(msg.failed),
		))}
	case msg.skipped > 0:
		return ActionCmd{util.ReportInfo(fmt.Sprintf(
			"Archived %d session(s); skipped %d (active or not in this workspace)", len(msg.succeeded), msg.skipped,
		))}
	default:
		return ActionCmd{util.ReportInfo(fmt.Sprintf(
			"Archived %d session(s)", len(msg.succeeded),
		))}
	}
}

// snapCursorAfterArchive positions the cursor on a sensible, valid row after
// a bulk archive rebuild. Preference order: the first still-selected (failed)
// active row, then the first active row, then the first non-separator row
// (an archived row when every active session was archived). It never leaves
// the cursor on the "Archived" separator.
func (s *Session) snapCursorAfterArchive(failed []string) {
	target := 0
	if len(failed) > 0 {
		failedSet := make(map[string]bool, len(failed))
		for _, id := range failed {
			failedSet[id] = true
		}
		for i, sess := range s.sessions {
			if failedSet[sess.ID] {
				target = i
				break
			}
		}
	}
	s.list.SetSelected(target)
	// If that landed on the separator (e.g. no active sessions remain so
	// index 0 is the separator), advance to the next non-separator row.
	if s.isSelectedSeparator() {
		s.list.SelectNext()
		if s.isSelectedSeparator() {
			// Degenerate: only a separator; fall back to first.
			s.list.SelectFirst()
		}
	}
	s.list.ScrollToSelected()
}

func (s *Session) confirmDeleteSession() Action {
	sessionItem := s.selectedSessionItem()
	s.sessionsMode = sessionsModeNormal
	if sessionItem == nil {
		return nil
	}

	s.removeSession(sessionItem.ID())
	return ActionCmd{s.deleteSessionCmd(sessionItem.ID())}
}

func (s *Session) removeSession(id string) {
	var newSessions []session.Session
	for _, sess := range s.sessions {
		if sess.ID == id {
			continue
		}
		newSessions = append(newSessions, sess)
	}
	s.sessions = newSessions
}

func (s *Session) deleteSessionCmd(id string) tea.Cmd {
	return func() tea.Msg {
		err := s.com.Workspace.DeleteSession(context.TODO(), id)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		return nil
	}
}

func (s *Session) confirmArchiveSession() Action {
	sessionItem := s.selectedSessionItem()
	s.sessionsMode = sessionsModeNormal
	if sessionItem == nil {
		return nil
	}

	s.removeSession(sessionItem.ID())
	// Move to archived sessions
	s.archivedSessions = append([]session.Session{sessionItem.Session}, s.archivedSessions...)
	return ActionCmd{s.archiveSessionCmd(sessionItem.ID())}
}

func (s *Session) archiveSessionCmd(id string) tea.Cmd {
	return func() tea.Msg {
		err := s.com.Workspace.ArchiveSession(context.TODO(), id)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		return nil
	}
}

func (s *Session) confirmUnarchiveSession() Action {
	sessionItem := s.selectedSessionItem()
	s.sessionsMode = sessionsModeNormal
	if sessionItem == nil {
		return nil
	}

	s.removeArchivedSession(sessionItem.ID())
	// Move to active sessions with updated timestamp
	sess := sessionItem.Session
	sess.UpdatedAt = time.Now().Unix()
	sess.ArchivedAt = 0
	s.sessions = append([]session.Session{sess}, s.sessions...)
	return ActionCmd{s.unarchiveSessionCmd(sessionItem.ID())}
}

func (s *Session) removeArchivedSession(id string) {
	var newSessions []session.Session
	for _, sess := range s.archivedSessions {
		if sess.ID == id {
			continue
		}
		newSessions = append(newSessions, sess)
	}
	s.archivedSessions = newSessions
}

func (s *Session) unarchiveSessionCmd(id string) tea.Cmd {
	return func() tea.Msg {
		err := s.com.Workspace.UnarchiveSession(context.TODO(), id)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		return nil
	}
}

func (s *Session) confirmRenameSession() Action {
	sessionItem := s.selectedSessionItem()
	s.sessionsMode = sessionsModeNormal
	if sessionItem == nil {
		return nil
	}

	newTitle := strings.TrimSpace(sessionItem.InputValue())
	if newTitle == "" {
		return nil
	}
	session := sessionItem.Session
	session.Title = newTitle
	s.updateSession(session)
	return ActionCmd{s.updateSessionCmd(session)}
}

func (s *Session) updateSession(session session.Session) {
	for existingID, sess := range s.sessions {
		if sess.ID == session.ID {
			s.sessions[existingID] = session
			break
		}
	}
}

func (s *Session) updateSessionCmd(session session.Session) tea.Cmd {
	return func() tea.Msg {
		_, err := s.com.Workspace.SaveSession(context.TODO(), session)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		return nil
	}
}

func (s *Session) isCurrentSessionBusy() bool {
	sessionItem := s.selectedSessionItem()
	if sessionItem == nil {
		return false
	}

	if !s.com.Workspace.AgentIsReady() {
		return false
	}

	return s.com.Workspace.AgentIsSessionBusy(sessionItem.ID())
}

// ShortHelp implements [help.KeyMap].
func (s *Session) ShortHelp() []key.Binding {
	switch s.sessionsMode {
	case sessionsModeDeleting:
		return []key.Binding{
			s.keyMap.ConfirmDelete,
			s.keyMap.CancelDelete,
		}
	case sessionsModeUpdating:
		return []key.Binding{
			s.keyMap.ConfirmRename,
			s.keyMap.CancelRename,
		}
	case sessionsModeArchiving:
		return []key.Binding{
			s.keyMap.ConfirmArchive,
			s.keyMap.CancelArchive,
		}
	case sessionsModeUnarchiving:
		return []key.Binding{
			s.keyMap.ConfirmUnarchive,
			s.keyMap.CancelUnarchive,
		}
	case sessionsModeSelecting:
		return []key.Binding{
			s.keyMap.UpDown,
			s.keyMap.VisualToggle,
			s.keyMap.ToggleSelect,
			s.keyMap.ArchiveSelect,
			s.keyMap.ExitSelect,
		}
	default:
		if s.isSelectedArchived() {
			return []key.Binding{
				s.keyMap.UpDown,
				s.keyMap.Unarchive,
				s.keyMap.Delete,
				s.keyMap.Select,
				s.keyMap.Close,
			}
		}
		return []key.Binding{
			s.keyMap.UpDown,
			s.keyMap.Rename,
			s.keyMap.Archive,
			s.keyMap.EnterSelect,
			s.keyMap.Delete,
			s.keyMap.Select,
			s.keyMap.Close,
		}
	}
}

// FullHelp implements [help.KeyMap].
func (s *Session) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		s.keyMap.UpDown,
		s.keyMap.Rename,
		s.keyMap.Archive,
		s.keyMap.Delete,
		s.keyMap.Select,
		s.keyMap.Close,
	}

	switch s.sessionsMode {
	case sessionsModeDeleting:
		slice = []key.Binding{
			s.keyMap.ConfirmDelete,
			s.keyMap.CancelDelete,
		}
	case sessionsModeUpdating:
		slice = []key.Binding{
			s.keyMap.ConfirmRename,
			s.keyMap.CancelRename,
		}
	case sessionsModeArchiving:
		slice = []key.Binding{
			s.keyMap.ConfirmArchive,
			s.keyMap.CancelArchive,
		}
	case sessionsModeUnarchiving:
		slice = []key.Binding{
			s.keyMap.ConfirmUnarchive,
			s.keyMap.CancelUnarchive,
		}
	case sessionsModeSelecting:
		slice = []key.Binding{
			s.keyMap.UpDown,
			s.keyMap.Top,
			s.keyMap.Bottom,
			s.keyMap.VisualToggle,
			s.keyMap.ToggleSelect,
			s.keyMap.ArchiveSelect,
			s.keyMap.ExitSelect,
		}
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

// buildListItems builds the list items including active sessions, separator, and archived sessions.
func (s *Session) buildListItems(mode sessionsMode) []list.FilterableItem {
	items := sessionItems(s.com.Styles, mode, s.sessions...)

	// Record the active-session item pointers (in order) so their ✓ marks
	// can be synced from the selection without rebuilding the list.
	s.activeItems = make([]*SessionItem, 0, len(items))
	for _, it := range items {
		if si, ok := it.(*SessionItem); ok {
			s.activeItems = append(s.activeItems, si)
		}
	}

	if len(s.archivedSessions) > 0 {
		// Add separator before archived sessions
		s.archivedStartIndex = len(items)
		items = append(items, NewSeparatorItem(s.com.Styles, "Archived"))

		// Add archived sessions
		archivedItems := sessionItems(s.com.Styles, mode, s.archivedSessions...)
		items = append(items, archivedItems...)
	} else {
		s.archivedStartIndex = -1
	}

	return items
}

// isSelectedArchived returns true if the currently selected item is an archived session.
func (s *Session) isSelectedArchived() bool {
	if s.archivedStartIndex < 0 {
		return false
	}
	selected := s.list.Selected()
	// Account for separator: archived items start at archivedStartIndex + 1
	return selected > s.archivedStartIndex
}

// isSelectedSeparator returns true if the currently selected item is the separator.
func (s *Session) isSelectedSeparator() bool {
	if s.archivedStartIndex < 0 {
		return false
	}
	return s.list.Selected() == s.archivedStartIndex
}
