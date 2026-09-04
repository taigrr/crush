package model

import (
	"context"
	"image"
	"log/slog"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/commands"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/sessionimport"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/ui/attachments"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/completions"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/notification"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/version"
	"github.com/taigrr/crush/internal/workspace"
	"github.com/taigrr/crush/internal/worktree"
)

// MouseScrollThreshold defines how many lines to scroll the chat when a mouse
// wheel event occurs.
const MouseScrollThreshold = 5

// Compact mode breakpoints.
const (
	compactModeWidthBreakpoint  = 120
	compactModeHeightBreakpoint = 30
)

// If pasted text has more than 10 newlines, treat it as a file attachment.
const pasteLinesThreshold = 10

// If pasted text has more than 1000 columns, treat it as a file attachment.
const pasteColsThreshold = 1000

// Session details panel max height.
const sessionDetailsMaxHeight = 20

// TextareaMaxHeight is the maximum height of the prompt textarea.
const TextareaMaxHeight = 15

// editorHeightMargin is the vertical margin added to the textarea height to
// account for the attachments row (top) and bottom margin.
const editorHeightMargin = 2

// TextareaMinHeight is the minimum height of the prompt textarea.
const TextareaMinHeight = 3

// uiFocusState represents the current focus state of the UI.
type uiFocusState uint8

// Possible uiFocusState values.
const (
	uiFocusNone uiFocusState = iota
	uiFocusEditor
	uiFocusMain
	uiFocusLeftSidebar
	uiFocusRightSidebar
)

type uiState uint8

// Possible uiState values.
const (
	uiOnboarding uiState = iota
	uiInitialize
	uiLanding
	uiChat
)

type openEditorMsg struct {
	Text string
}

// backfillCountMsg carries the pending-embedding count for the backfill
// confirm flow.
type backfillCountMsg struct {
	count int
	err   error
}

// backfillDoneMsg carries the result of an embedding backfill.
type backfillDoneMsg struct {
	count int
	err   error
}

// embeddingStatusMsg carries a polled embedding index status used to
// drive the sidebar progress bar while a backfill runs.
type embeddingStatusMsg struct {
	status proto.EmbeddingStatus
	err    error
}

type (
	// cancelTimerExpiredMsg is sent when the cancel timer expires. gen is
	// the generation the timer was armed with; a mismatch means a newer
	// arm superseded it and the message must be ignored.
	cancelTimerExpiredMsg struct{ gen int }
	// userCommandsLoadedMsg is sent when user commands are loaded.
	userCommandsLoadedMsg struct {
		Commands []commands.CustomCommand
	}
	// mcpPromptsLoadedMsg is sent when mcp prompts are loaded.
	mcpPromptsLoadedMsg struct {
		Prompts []commands.MCPPrompt
	}
	// lspStateChangedMsg is sent when there is a change in LSP client states.
	lspStateChangedMsg struct {
		states map[string]workspace.LSPClientInfo
	}
	// mcpStateChangedMsg is sent when there is a change in MCP client states.
	mcpStateChangedMsg struct {
		states map[string]mcp.ClientInfo
	}
	// sendMessageMsg is sent to send a message.
	// currently only used for mcp prompts.
	sendMessageMsg struct {
		Content     string
		Attachments []message.Attachment
	}

	// closeDialogMsg is sent to close the current dialog.
	closeDialogMsg       struct{}
	openSessionImportMsg struct {
		sources []sessionimport.SourceInfo
	}

	// hyperRefreshDoneMsg is sent after a silent Hyper OAuth refresh
	// finishes. It carries the original model-selection action so the
	// selection can be resumed.
	hyperRefreshDoneMsg struct {
		action dialog.ActionSelectModel
	}

	// copyChatHighlightMsg is sent to copy the current chat highlight to clipboard.
	copyChatHighlightMsg struct{}

	// sessionFilesUpdatesMsg is sent when the files for this session have been updated
	sessionFilesUpdatesMsg struct {
		sessionID    string
		sessionFiles []SessionFile
	}
	// creditsUpdatedMsg is sent when the remaining Hyper credits have been
	// fetched from the API.
	creditsUpdatedMsg struct {
		credits int
	}
	// forkCompletedMsg is sent when a conversation fork completes.
	forkCompletedMsg struct {
		newSession  session.Session
		worktree    *worktree.Worktree
		prefillText string
	}

	// forkFailedMsg is sent when a conversation fork fails, so the progress
	// dialog can be closed and the error surfaced.
	forkFailedMsg struct {
		err error
	}
)

// UI represents the main user interface model.
type UI struct {
	com     *common.Common
	session *session.Session
	// pasteCount is the highest paste_N index handed out in this UI so
	// far; see pasteIdx.
	pasteCount   int
	sessionFiles []SessionFile

	// pendingPermissions caches unresolved permission requests keyed by
	// session ID. Requests are broadcast to every client in the
	// workspace (including background sessions the user is not viewing),
	// so multiple sessions can each have a request in flight at once; a
	// single pointer would let one session's prompt clobber another's.
	// The entry for the active session is auto-surfaced; others are
	// cached (and OS-notified) and re-surfaced on session switch.
	// Cleared when the request resolves.
	pendingPermissions map[string]*permission.PermissionRequest

	// pendingQuestions mirrors pendingPermissions for the question tool:
	// unresolved question requests keyed by session ID.
	pendingQuestions map[string]*question.Request

	// attentionPending is the set of session IDs (across ALL workspaces)
	// reported as blocked on a permission/question prompt by the global
	// attention channel. Unlike pendingPermissions/pendingQuestions it
	// holds no full request (the request body arrives on the originating
	// workspace's own stream when the user switches to it); it only
	// drives the red row dot and the red window border for background
	// sessions in workspaces this client is not currently focused on.
	attentionPending map[string]bool

	// keeps track of read files while we don't have a session id
	sessionFileReads []string

	// initialSessionID is set when loading a specific session on startup.
	initialSessionID string
	// continueLastSession is set to continue the most recent session on startup.
	continueLastSession bool

	lastUserMessageTime int64

	// The width and height of the terminal in cells.
	width  int
	height int
	layout uiLayout

	isTransparent     bool
	hasDarkBackground bool
	activeThemeName   string

	focus uiFocusState
	state uiState

	keyMap KeyMap
	keyenh tea.KeyboardEnhancementsMsg

	dialog *dialog.Overlay
	status *Status

	// isCanceling tracks whether the user has pressed escape once to cancel.
	isCanceling bool
	// cancelGen is bumped every time the cancel timer is (re)armed. Each
	// timer carries the generation it was started with so a stale timer
	// from an earlier arm cannot disarm a newer cycle.
	cancelGen int

	header *header

	// sendProgressBar instructs the TUI to send progress bar updates to the
	// terminal.
	sendProgressBar    bool
	progressBarEnabled bool

	// caps hold different terminal capabilities that we query for.
	caps common.Capabilities

	// Editor components
	textarea textarea.Model

	// Attachment list
	attachments *attachments.Attachments

	readyPlaceholder   string
	workingPlaceholder string

	// Completions state
	completions              *completions.Completions
	completionsOpen          bool
	completionsStartIndex    int
	completionsQuery         string
	completionsTrigger       byte        // '@' for files/resources, '/' for commands
	completionsPositionStart image.Point // x,y where user typed the trigger

	// Chat components
	chat *Chat

	// leftSidebar is the cross-workspace session navigator. It is visible
	// when leftSidebarVisible is true and focused when focus is
	// uiFocusLeftSidebar.
	leftSidebar        *SessionsSidebar
	leftSidebarVisible bool
	// stash holds a parked prompt draft (see toggleStash); nil when empty.
	stash *promptStash
	// leftSidebarPinned keeps the navigator open across session switches:
	// activating a session, esc, and h return focus to the editor instead
	// of collapsing it, and ctrl+s toggles focus rather than visibility.
	// Seeded from config and persisted on toggle.
	leftSidebarPinned bool
	// leftSidebarWidth is the navigator's width in columns. Seeded from
	// config so a resize persists across restarts.
	leftSidebarWidth int

	// Session live-preview state (see session_preview.go). While
	// previewSessionID is non-empty the chat view shows an ephemeral,
	// read-only render of a highlighted (but not committed) session; the
	// committed session (m.session) is untouched and remains the routing
	// key for live message events. previewGen supersedes stale debounced
	// loads; pendingPreviewID is the id a scheduled load is waiting on.
	// pendingPreviewRoot is that session's workspace root, empty when it
	// is the currently-attached workspace — it decides whether the
	// eventual load goes through ListMessages (current workspace, live
	// service) or PeekMessages (any other known workspace, attached or
	// not, read without switching this client's own workspace).
	previewSessionID   string
	pendingPreviewID   string
	pendingPreviewRoot string
	previewGen         int

	// previewSess and previewFiles hold the highlighted session's
	// metadata and modified-file stats fetched alongside its preview
	// messages, so the right info-sidebar reflects the previewed session
	// (title, swarm identity, working dir, cost/tokens, modified files)
	// rather than the committed one. They are only consulted while
	// previewing() is true and previewSess is non-nil; both are cleared
	// when a preview is cancelled or committed.
	previewSess  *session.Session
	previewFiles []SessionFile

	// Leading-edge burst tracking (see session_preview.go). The first two
	// preview loads inside a rolling burst window fire immediately; the
	// third and later within the window fall back to the trailing debounce.
	// previewBurstCount counts loads in the current window; previewLastNav
	// timestamps the last load so an idle gap longer than the window resets
	// the counter. previewNow is an injectable clock for tests (nil == real
	// time).
	previewBurstCount int
	previewLastNav    time.Time
	previewNow        func() time.Time

	// Right info-sidebar virtual scroll state. rightSidebarScrollable and
	// rightSidebarMaxOffsetVal are recomputed each frame in drawSidebar.
	rightSidebarOffset       int
	rightSidebarScrollable   bool
	rightSidebarMaxOffsetVal int

	// swarmAddrRow is the absolute screen row of the sidebar's swarm
	// address line (color-animal-shorthash) on the last frame, or -1 when
	// it is not visible. swarmAddr is the address text rendered there.
	// Both are recomputed each frame in drawSidebar so a click on that
	// row can copy the address to the clipboard.
	swarmAddrRow int
	swarmAddr    string

	// shellCancel cancels the in-flight bang-mode (!) shell command. It is
	// non-nil only while a command is running; the cancellation propagates
	// through the client HTTP request to the server's shell.Run context.
	shellCancel context.CancelFunc

	// chatFullscreen hides both the left navigator and the right info
	// sidebar so the chat uses the full width (toggled with ctrl+f).
	chatFullscreen bool

	// onboarding state
	onboarding struct {
		yesInitializeSelected bool
	}

	// lsp
	lspStates map[string]workspace.LSPClientInfo

	// mcp
	mcpStates map[string]mcp.ClientInfo

	// skills
	skillStates []*skills.SkillState

	// embedding backfill progress (shown in the sidebar only while a
	// backfill is running).
	backfillActive bool
	backfillStatus proto.EmbeddingStatus

	// sidebarLogo keeps a cached version of the sidebar sidebarLogo.
	sidebarLogo string

	// Notification state
	notifyBackend       notification.Backend
	notifyWindowFocused bool
	// custom commands & mcp commands
	customCommands []commands.CustomCommand
	mcpPrompts     []commands.MCPPrompt

	// forceCompactMode tracks whether compact mode is forced by user toggle
	forceCompactMode bool

	// yoloMode caches the currently-viewed workspace's permission
	// skip-requests flag so the editor placeholder/prompt icon don't have
	// to make a synchronous round trip to the server on every Update call.
	// It is refreshed explicitly: at startup, on ToggleYolo, and whenever
	// the client re-targets a different workspace (SwitchWorkspace) —
	// each workspace has its own independent skip-requests flag, so a
	// stale cached value here is exactly what made the yolo indicator show
	// the PREVIOUS workspace's state right after switching.
	yoloMode bool

	// isCompact tracks whether we're currently in compact layout mode (either
	// by user toggle or auto-switch based on window size)
	isCompact bool

	// detailsOpen tracks whether the details panel is open (in compact mode)
	detailsOpen bool

	// pills state
	pillsExpanded      bool
	pillsAutoExpanded  bool
	focusedPillSection pillSection
	promptQueue        int
	pillsView          string

	// Todo spinner
	todoSpinner    spinner.Model
	todoIsSpinning bool

	// titleAnim drives the session-title reveal animation.
	titleAnim titleAnimState

	// mouse highlighting related state
	lastClickTime time.Time

	// hyperCredits is the remaining Hyper credits, updated after each prompt.
	hyperCredits *int

	// versionMismatch is set when the connected server's version does not
	// match this client's version (e.g. another client restarted the
	// shared server with a different binary). When set, the UI renders a
	// full-screen banner instructing the user to restart crush.
	versionMismatch  bool
	serverVersionStr string

	// themePreviewOriginal holds the styles captured when the theme picker
	// opened, so canceling (esc) can restore them after live previews.
	themePreviewOriginal *styles.Styles

	// Prompt history for up/down navigation through previous messages.
	promptHistory struct {
		messages []string
		index    int
		draft    string
	}

	// searchGen is the search palette's debounce generation counter. Each
	// keystroke bumps it; the debounce tick and the eventual RPC carry the
	// generation they were scheduled under and are dropped if stale.
	searchGen int
}

// New creates a new instance of the [UI] model.
func New(com *common.Common, initialSessionID string, continueLast bool) *UI {
	// Editor components
	ta := textarea.New()
	ta.SetStyles(com.Styles.Editor.Textarea)
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = TextareaMinHeight
	ta.MaxHeight = TextareaMaxHeight
	ta.Focus()

	ch := NewChat(com)

	keyMap := DefaultKeyMap()

	// Completions component
	comp := completions.New(
		com.Styles.Completions.Normal,
		com.Styles.Completions.Focused,
		com.Styles.Completions.Match,
	)

	todoSpinner := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(com.Styles.Pills.TodoSpinner),
	)

	// Attachments component
	attachments := attachments.New(
		attachments.NewRenderer(
			com.Styles.Attachments.Normal,
			com.Styles.Attachments.Deleting,
			com.Styles.Attachments.Image,
			com.Styles.Attachments.Text,
			com.Styles.Attachments.Skill,
		),
		attachments.Keymap{
			DeleteMode: keyMap.Editor.AttachmentDeleteMode,
			DeleteAll:  keyMap.Editor.DeleteAllAttachments,
			Escape:     keyMap.Editor.Escape,
		},
	)

	header := newHeader(com)

	ui := &UI{
		com:                 com,
		dialog:              dialog.NewOverlay(),
		keyMap:              keyMap,
		textarea:            ta,
		chat:                ch,
		leftSidebar:         NewSessionsSidebar(com),
		header:              header,
		completions:         comp,
		attachments:         attachments,
		todoSpinner:         todoSpinner,
		lspStates:           make(map[string]workspace.LSPClientInfo),
		mcpStates:           make(map[string]mcp.ClientInfo),
		notifyBackend:       notification.NoopBackend{},
		notifyWindowFocused: true,
		hasDarkBackground:   true,
		initialSessionID:    initialSessionID,
		continueLastSession: continueLast,
		pendingPermissions:  make(map[string]*permission.PermissionRequest),
		pendingQuestions:    make(map[string]*question.Request),
		attentionPending:    make(map[string]bool),
	}

	status := NewStatus(com, ui)

	ui.setEditorPrompt(com.Workspace.PermissionSkipRequests())
	ui.randomizePlaceholders()
	ui.textarea.Placeholder = ui.readyPlaceholder
	ui.status = status

	// Initialize compact mode from config
	ui.forceCompactMode = com.Config().Options.TUI.CompactMode

	// Seed the navigator width from config so a previous resize persists.
	ui.leftSidebarWidth = clampLeftSidebarWidth(com.Config().Options.TUI.SessionsSidebarWidth)
	// A pinned navigator starts open (the layout only carves it out in the
	// chat and landing states); focus stays with the editor.
	ui.leftSidebarPinned = com.Config().Options.TUI.SessionsSidebarPinned
	ui.leftSidebarVisible = ui.leftSidebarPinned

	// set onboarding state defaults
	ui.onboarding.yesInitializeSelected = true

	desiredState := uiLanding
	desiredFocus := uiFocusEditor
	if !com.Config().IsConfigured() {
		desiredState = uiOnboarding
	} else if n, _ := com.Workspace.ProjectNeedsInitialization(); n {
		desiredState = uiInitialize
	}

	// set initial state
	ui.setState(desiredState, desiredFocus)

	opts := com.Config().Options

	// disable indeterminate progress bar
	ui.progressBarEnabled = opts.Progress == nil || *opts.Progress
	// enable transparent mode
	ui.isTransparent = opts.TUI.Transparent != nil && *opts.TUI.Transparent

	return ui
}

// Init initializes the UI model.
func (m *UI) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	if m.state == uiOnboarding {
		if cmd := m.openModelsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// load the user commands async
	cmds = append(cmds, m.loadCustomCommands())
	// load prompt history async
	cmds = append(cmds, m.loadPromptHistory())
	// load initial LSP, MCP, and skill states
	cmds = append(cmds, m.loadInitialLSPStates(), m.loadInitialMCPStates(), m.loadInitialSkillStates())
	// load cross-workspace session overviews for the landing screen and
	// the session navigator.
	cmds = append(cmds, m.loadWorkspaceOverviews())
	// load initial session if specified
	if cmd := m.loadInitialSession(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if m.com.IsHyper() {
		cmds = append(cmds, m.fetchHyperCredits())
	}
	// Detect client/server version mismatch and keep re-checking.
	cmds = append(cmds, m.checkServerVersion(), m.scheduleVersionCheck())
	// Keep the between-messages assistant-info "since" label current.
	cmds = append(cmds, m.scheduleAssistantInfoTick())
	return tea.Batch(cmds...)
}

// loadInitialSession loads the initial session if one was specified on startup.
func (m *UI) loadInitialSession() tea.Cmd {
	switch {
	case m.state != uiLanding:
		// Only load if we're in landing state (i.e., fully configured)
		return nil
	case m.initialSessionID != "":
		return m.loadSession(m.initialSessionID)
	case m.continueLastSession:
		return func() tea.Msg {
			sessions, err := m.com.Workspace.ListSessions(context.Background())
			if err != nil || len(sessions) == 0 {
				return nil
			}
			return m.loadSession(sessions[0].ID)()
		}
	default:
		return nil
	}
}

// sendNotification returns a command that sends a notification if allowed by policy.
func (m *UI) setState(state uiState, focus uiFocusState) {
	if state == uiLanding {
		// Always turn off compact mode when going to landing
		m.isCompact = false
	}
	m.state = state
	m.focus = focus
	// Changing the state may change layout, so update it.
	m.updateLayoutAndSize()
}

// loadCustomCommands loads the custom commands asynchronously.
func (m *UI) loadCustomCommands() tea.Cmd {
	return func() tea.Msg {
		customCommands, err := commands.LoadCustomCommands(m.com.Config())
		if err != nil {
			slog.Error("Failed to load custom commands", "error", err)
		}
		// Append user-invocable skills as commands.
		skillEntries, err := m.com.Workspace.ListSkills(context.Background())
		if err != nil {
			slog.Error("Failed to load skill commands", "error", err)
		}
		customCommands = append(customCommands, commands.FromSkillCatalog(skillEntries)...)
		return userCommandsLoadedMsg{Commands: customCommands}
	}
}

// loadMCPrompts loads the MCP prompts asynchronously.
func (m *UI) loadMCPrompts() tea.Msg {
	prompts, err := commands.LoadMCPPrompts()
	if err != nil {
		slog.Error("Failed to load MCP prompts", "error", err)
	}
	if prompts == nil {
		// flag them as loaded even if there is none or an error
		prompts = []commands.MCPPrompt{}
	}
	return mcpPromptsLoadedMsg{Prompts: prompts}
}

// serverVersionMsg carries the result of a server version check.
type serverVersionMsg struct {
	mismatch bool
	version  string
}

// versionCheckInterval is how often the UI re-checks the server version
// to detect a mismatch introduced after startup (e.g. another client
// restarted the shared server with a newer binary).
const versionCheckInterval = 30 * time.Second

// checkServerVersion queries the server version and reports whether it
// differs from this client's build. The check is best-effort: transient
// errors are ignored so a momentarily unreachable server does not flash
// the mismatch banner.
func (m *UI) checkServerVersion() tea.Cmd {
	return func() tea.Msg {
		vi, err := m.com.Workspace.ServerVersion(context.Background())
		if err != nil {
			return nil
		}
		mismatch := vi.Version != version.Version || vi.BuildID != version.BuildID
		return serverVersionMsg{mismatch: mismatch, version: vi.Version}
	}
}

// scheduleVersionCheck re-runs the server version check after
// versionCheckInterval.
func (m *UI) scheduleVersionCheck() tea.Cmd {
	return tea.Tick(versionCheckInterval, func(time.Time) tea.Msg {
		return versionCheckTickMsg{}
	})
}

// versionCheckTickMsg triggers a periodic server version check.
type versionCheckTickMsg struct{}

// assistantInfoTickInterval is how often the between-messages
// assistant-info footer refreshes its humanized "since" label. The
// label only changes at minute/hour granularity, so a coarse cadence
// keeps it current without wasting CPU.
const assistantInfoTickInterval = 30 * time.Second

// assistantInfoTickMsg triggers a refresh of the humanized "since"
// timestamps on assistant-info footers.
type assistantInfoTickMsg struct{}

// scheduleAssistantInfoTick re-runs the assistant-info time refresh
// after assistantInfoTickInterval.
func (m *UI) scheduleAssistantInfoTick() tea.Cmd {
	return tea.Tick(assistantInfoTickInterval, func(time.Time) tea.Msg {
		return assistantInfoTickMsg{}
	})
}

// uiLayout defines the positioning of UI elements.
type uiLayout struct {
	// area is the overall available area.
	area uv.Rectangle

	// header is the header shown in special cases
	// e.x when the sidebar is collapsed
	// or when in the landing page
	// or in init/config
	header uv.Rectangle

	// main is the area for the main pane. (e.x chat, configure, landing)
	main uv.Rectangle

	// pills is the area for the pills panel.
	pills uv.Rectangle

	// editor is the area for the editor pane.
	editor uv.Rectangle

	// sidebar is the area for the sidebar.
	sidebar uv.Rectangle

	// leftSidebar is the area for the cross-workspace session navigator.
	leftSidebar uv.Rectangle

	// status is the area for the status view.
	status uv.Rectangle

	// session details is the area for the session details overlay in compact mode.
	sessionDetails uv.Rectangle
}

// shellCommandFinishedMsg is emitted when a bang-mode shell command
// completes (or is cancelled), so the model can clear its cancel func and
// surface any error.
type shellCommandFinishedMsg struct {
	err error
}

const cancelTimerDuration = 2 * time.Second

// logoEdition is the fork's edition label shown in the logo's diagonal
// banner.
const logoEdition = "taigrr edition"
