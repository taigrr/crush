package model

import (
	"charm.land/bubbles/v2/key"
	"github.com/taigrr/crush/internal/ui/chat"
)

type KeyMap struct {
	Editor struct {
		AddFile     key.Binding
		SendMessage key.Binding
		// Steer sends the prompt as a mid-turn aside while the agent is
		// busy: it is folded into the active turn at the next step
		// boundary instead of waiting in the queue for its own turn.
		// When the agent is idle it behaves like SendMessage.
		Steer       key.Binding
		OpenEditor  key.Binding
		Newline     key.Binding
		AddImage    key.Binding
		PasteImage  key.Binding
		MentionFile key.Binding
		Commands    key.Binding
		// Stash parks the drafted prompt (text and attachments) so a
		// different message can be sent first; pressing it again with an
		// empty editor restores the draft, with a non-empty editor swaps.
		Stash key.Binding

		// Attachments key maps
		AttachmentDeleteMode key.Binding
		Escape               key.Binding
		DeleteAllAttachments key.Binding

		// History navigation
		HistoryPrev key.Binding
		HistoryNext key.Binding
	}

	Chat struct {
		NewSession    key.Binding
		AddAttachment key.Binding
		Cancel        key.Binding
		// BackgroundTool moves the running bash command to the
		// background so the tool returns a job id and the turn continues.
		// Only active while such a command is in flight; otherwise the
		// key falls through to the focused component.
		BackgroundTool      key.Binding
		Tab                 key.Binding
		Details             key.Binding
		TogglePills         key.Binding
		PillLeft            key.Binding
		PillRight           key.Binding
		Down                key.Binding
		Up                  key.Binding
		UpDown              key.Binding
		DownOneItem         key.Binding
		UpOneItem           key.Binding
		UpDownOneItem       key.Binding
		PrevUserMessage     key.Binding
		NextUserMessage     key.Binding
		PrevNextUserMessage key.Binding
		PageDown            key.Binding
		PageUp              key.Binding
		HalfPageDown        key.Binding
		HalfPageUp          key.Binding
		Home                key.Binding
		End                 key.Binding
		Copy                key.Binding
		ClearHighlight      key.Binding
		Expand              key.Binding
		FocusRightSidebar   key.Binding
		FocusChat           key.Binding
	}

	Initialize struct {
		Yes,
		No,
		Enter,
		Switch key.Binding
	}

	// Sidebar holds the navigation keys active while the left session
	// navigator has focus.
	Sidebar struct {
		UpDown key.Binding
		Open   key.Binding
		Close  key.Binding
		Resize key.Binding
	}

	// SessionSidebar key maps drive the left session navigator's
	// vim-style multi-select and bulk actions.
	SessionSidebar struct {
		VisualSelect  key.Binding
		ToggleSelect  key.Binding
		ArchiveSelect key.Binding
		MarkRead      key.Binding
		Favorite      key.Binding
		Inbox         key.Binding
		Search        key.Binding
		PrevSection   key.Binding
		NextSection   key.Binding
		Pin           key.Binding
	}

	// Global key maps
	Quit     key.Binding
	Help     key.Binding
	Commands key.Binding
	Models   key.Binding
	Suspend  key.Binding
	Sessions key.Binding
	// PinSessions keeps the left session navigator open across session
	// switches (see UI.leftSidebarPinned).
	PinSessions key.Binding
	Search      key.Binding
	Milestones  key.Binding
	Tab         key.Binding
	ToggleYolo  key.Binding
	Fullscreen  key.Binding
	// ArchiveSession opens the confirmation modal to archive the current
	// (active) session from the main window.
	ArchiveSession key.Binding
}

func DefaultKeyMap() KeyMap {
	km := KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("ctrl+g", "more"),
		),
		Commands: key.NewBinding(
			key.WithKeys("ctrl+p"),
			key.WithHelp("ctrl+p", "commands"),
		),
		Models: key.NewBinding(
			key.WithKeys("ctrl+m", "ctrl+l"),
			key.WithHelp("ctrl+m", "models"),
		),
		Suspend: key.NewBinding(
			key.WithKeys("ctrl+z"),
			key.WithHelp("ctrl+z", "suspend"),
		),
		Sessions: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "sessions"),
		),
		// Bubble Tea enables Kitty key disambiguation, so terminals that
		// speak it (Ghostty, kitty, WezTerm, ...) deliver ctrl+shift+s
		// distinctly; alt+s is the fallback for those that collapse it
		// onto ctrl+s.
		PinSessions: key.NewBinding(
			key.WithKeys("alt+s", "ctrl+shift+s"),
			key.WithHelp("alt+s", "pin sessions"),
		),
		Search: key.NewBinding(
			key.WithKeys("ctrl+b"),
			key.WithHelp("ctrl+b", "search"),
		),
		Milestones: key.NewBinding(
			key.WithKeys("ctrl+q"),
			key.WithHelp("ctrl+q", "milestones"),
		),
		ToggleYolo: key.NewBinding(
			key.WithKeys("ctrl+y"),
			key.WithHelp("ctrl+y", "toggle yolo"),
		),
		Fullscreen: key.NewBinding(
			key.WithKeys("ctrl+f"),
			key.WithHelp("ctrl+f", "fullscreen"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "change focus"),
		),
		ArchiveSession: key.NewBinding(
			key.WithKeys("ctrl+x"),
			key.WithHelp("ctrl+x", "archive session"),
		),
	}

	km.Editor.Stash = key.NewBinding(
		key.WithKeys("ctrl+shift+z", "alt+z"),
		key.WithHelp("alt+z", "stash prompt"),
	)
	km.Editor.AddFile = key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "add file"),
	)
	km.Editor.SendMessage = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "send"),
	)
	km.Editor.Steer = key.NewBinding(
		key.WithKeys("alt+enter"),
		key.WithHelp("alt+enter", "steer (send mid-turn)"),
	)
	km.Editor.OpenEditor = key.NewBinding(
		key.WithKeys("ctrl+o"),
		key.WithHelp("ctrl+o", "open editor"),
	)
	km.Editor.Newline = key.NewBinding(
		key.WithKeys("shift+enter", "ctrl+j"),
		// "ctrl+j" is a common keybinding for newline in many editors. If
		// the terminal supports "shift+enter", we substitute the help tex
		// to reflect that.
		key.WithHelp("ctrl+j", "newline"),
	)
	km.Editor.AddImage = key.NewBinding(
		key.WithKeys("ctrl+i"),
		key.WithHelp("ctrl+i", "add image"),
	)
	km.Editor.PasteImage = key.NewBinding(
		key.WithKeys("ctrl+v"),
		key.WithHelp("ctrl+v", "paste image from clipboard"),
	)
	km.Editor.MentionFile = key.NewBinding(
		key.WithKeys("@"),
		key.WithHelp("@", "mention file"),
	)
	km.Editor.Commands = key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "commands"),
	)
	km.Editor.AttachmentDeleteMode = key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r+{i}", "delete attachment at index i"),
	)
	km.Editor.Escape = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "cancel delete mode"),
	)
	km.Editor.DeleteAllAttachments = key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("ctrl+r+r", "delete all attachments"),
	)
	km.Editor.HistoryPrev = key.NewBinding(
		key.WithKeys("up"),
	)
	km.Editor.HistoryNext = key.NewBinding(
		key.WithKeys("down"),
	)

	km.Chat.NewSession = key.NewBinding(
		key.WithKeys("ctrl+n"),
		key.WithHelp("ctrl+n", "new session"),
	)
	km.Chat.AddAttachment = key.NewBinding(
		key.WithKeys("ctrl+i"),
		key.WithHelp("ctrl+i", "add attachment"),
	)
	km.Chat.Cancel = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "cancel"),
	)
	km.Chat.BackgroundTool = key.NewBinding(
		key.WithKeys(chat.BackgroundToolKey),
		key.WithHelp(chat.BackgroundToolKey, "background command"),
	)
	km.Chat.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "change focus"),
	)
	km.Chat.Details = key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "toggle details"),
	)
	km.Chat.TogglePills = key.NewBinding(
		key.WithKeys("ctrl+t", "ctrl+space"),
		key.WithHelp("ctrl+t", "toggle tasks"),
	)
	km.Chat.PillLeft = key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←/→", "switch section"),
	)
	km.Chat.PillRight = key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("←/→", "switch section"),
	)

	km.Chat.Down = key.NewBinding(
		key.WithKeys("down", "ctrl+j", "j"),
		key.WithHelp("↓", "down"),
	)
	km.Chat.Up = key.NewBinding(
		key.WithKeys("up", "ctrl+k", "k"),
		key.WithHelp("↑", "up"),
	)
	km.Chat.FocusRightSidebar = key.NewBinding(
		key.WithKeys("l"),
		key.WithHelp("l", "focus sidebar"),
	)
	km.Chat.FocusChat = key.NewBinding(
		key.WithKeys("h"),
		key.WithHelp("h", "focus chat"),
	)
	km.Chat.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑↓", "scroll"),
	)
	km.Chat.UpOneItem = key.NewBinding(
		key.WithKeys("shift+up", "K"),
		key.WithHelp("shift+↑", "up one item"),
	)
	km.Chat.DownOneItem = key.NewBinding(
		key.WithKeys("shift+down", "J"),
		key.WithHelp("shift+↓", "down one item"),
	)
	km.Chat.UpDownOneItem = key.NewBinding(
		key.WithKeys("shift+up", "shift+down"),
		key.WithHelp("shift+↑↓", "scroll one item"),
	)
	km.Chat.PrevUserMessage = key.NewBinding(
		key.WithKeys("H"),
		key.WithHelp("H", "prev user message"),
	)
	km.Chat.NextUserMessage = key.NewBinding(
		key.WithKeys("L"),
		key.WithHelp("L", "next user message"),
	)
	km.Chat.PrevNextUserMessage = key.NewBinding(
		key.WithKeys("H", "L"),
		key.WithHelp("H/L", "prev/next user message"),
	)
	km.Chat.HalfPageDown = key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "half page down"),
	)
	km.Chat.PageDown = key.NewBinding(
		key.WithKeys("pgdown", " ", "f"),
		key.WithHelp("f/pgdn", "page down"),
	)
	km.Chat.PageUp = key.NewBinding(
		key.WithKeys("pgup", "b"),
		key.WithHelp("b/pgup", "page up"),
	)
	km.Chat.HalfPageUp = key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "half page up"),
	)
	km.Chat.Home = key.NewBinding(
		key.WithKeys("g", "home"),
		key.WithHelp("g", "home"),
	)
	km.Chat.End = key.NewBinding(
		key.WithKeys("G", "end"),
		key.WithHelp("G", "end"),
	)
	km.Chat.Copy = key.NewBinding(
		key.WithKeys("c", "y", "C", "Y"),
		key.WithHelp("c/y", "copy"),
	)
	km.Chat.ClearHighlight = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "clear selection"),
	)
	km.Chat.Expand = key.NewBinding(
		key.WithKeys("space"),
		key.WithHelp("space", "expand/collapse"),
	)
	km.Initialize.Yes = key.NewBinding(
		key.WithKeys("y", "Y"),
		key.WithHelp("y", "yes"),
	)
	km.Initialize.No = key.NewBinding(
		key.WithKeys("n", "N", "esc", "alt+esc"),
		key.WithHelp("n", "no"),
	)
	km.Initialize.Switch = key.NewBinding(
		key.WithKeys("left", "right", "tab"),
		key.WithHelp("tab", "switch"),
	)
	km.Initialize.Enter = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	)

	km.Sidebar.UpDown = key.NewBinding(
		key.WithKeys("up", "down", "k", "j"),
		key.WithHelp("↑↓", "navigate"),
	)
	km.Sidebar.Open = key.NewBinding(
		key.WithKeys("enter", "l"),
		key.WithHelp("enter", "open"),
	)
	km.Sidebar.Close = key.NewBinding(
		key.WithKeys("esc", "h"),
		key.WithHelp("esc", "close"),
	)
	km.Sidebar.Resize = key.NewBinding(
		key.WithKeys("[", "]", "-", "+", "=", "shift+left", "shift+right"),
		key.WithHelp("[/]", "resize"),
	)

	km.SessionSidebar.VisualSelect = key.NewBinding(
		key.WithKeys("v"),
		key.WithHelp("v", "visual select"),
	)
	km.SessionSidebar.ToggleSelect = key.NewBinding(
		key.WithKeys("space", " "),
		key.WithHelp("space", "toggle select"),
	)
	km.SessionSidebar.ArchiveSelect = key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "archive selected"),
	)
	// Mark selected sessions read. Like the other single-letter sidebar
	// bindings it is only handled while the sidebar is focused
	// (handleLeftSidebarKey), so it never collides with editor keys. "r"
	// is mnemonic for read.
	km.SessionSidebar.MarkRead = key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "mark read"),
	)
	// Toggle favorite on the session under the cursor. Favorites are
	// stickied to the top of the inbox view (just below sessions blocked
	// on a permission prompt). Only handled while the sidebar is focused,
	// so it never collides with editor keys. "f" is mnemonic for favorite.
	km.SessionSidebar.Favorite = key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "favorite"),
	)
	// Inbox toggle: ctrl+i is byte 0x09 == Tab in terminals without the
	// Kitty keyboard protocol (which Crush does not enable), and Tab is
	// already bound to change-focus — so ctrl+i would be an ambiguous
	// alias. Use a plain "i" instead; it is only handled while the sidebar
	// is focused (handleLeftSidebarKey), where single letters like v/a are
	// already owned, and is mnemonic for Inbox.
	km.SessionSidebar.Inbox = key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "inbox/sessions"),
	)
	// Text filter in the focused sidebar. Like the other single-letter
	// sidebar bindings it is only handled while the sidebar is focused, so
	// it never collides with the editor's "/" add-file binding.
	km.SessionSidebar.Search = key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	)
	// Jump between sections (workspaces in grouped mode, status sections in
	// inbox mode). Mirrors vim's paragraph motions.
	km.SessionSidebar.PrevSection = key.NewBinding(
		key.WithKeys("{"),
		key.WithHelp("{", "prev section"),
	)
	km.SessionSidebar.NextSection = key.NewBinding(
		key.WithKeys("}"),
		key.WithHelp("}", "next section"),
	)
	// Pin/unpin the navigator so it survives session switches. Like the
	// other single-letter sidebar bindings it is only handled while the
	// sidebar is focused; alt+s does the same from anywhere.
	km.SessionSidebar.Pin = key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "pin/unpin"),
	)

	return km
}
