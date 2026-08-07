package dialog

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/commands"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/oauth"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/ui/util"
)

// ActionClose is a message to close the current dialog.
type ActionClose struct{}

// ActionQuit is a message to quit the application.
type ActionQuit = tea.QuitMsg

// ActionOpenDialog is a message to open a dialog.
type ActionOpenDialog struct {
	DialogID string
}

// ActionSelectSession is a message indicating a session has been selected.
type ActionSelectSession struct {
	Session session.Session
}

// ActionPreviewSession is emitted as the session picker's cursor moves to a
// new session, so the model can live-preview it (debounced) in the chat view
// behind the modal without committing. SessionID is the newly highlighted
// session.
type ActionPreviewSession struct {
	SessionID string
}

// ActionSearchQueryChanged is emitted by the search palette when the
// query text, semantic toggle, or inline global flag changes. The UI owns
// the debounce: it bumps a generation counter and schedules a search
// command. AllWorkspaces requests cross-workspace fan-out. InputCmd
// carries the textinput's own cmd (cursor blink) so it isn't lost.
type ActionSearchQueryChanged struct {
	Query         string
	Semantic      *bool
	AllWorkspaces bool
	InputCmd      tea.Cmd
}

// ActionPreviewSearchResult is emitted as the search palette selection
// moves. The UI turns it into a live preview of the highlighted session
// (current-workspace hits only; foreign-workspace hits are not previewed).
type ActionPreviewSearchResult struct {
	Hit proto.SessionHit
}

// ActionSelectSearchResult is emitted when a search result is confirmed
// (enter). The UI loads the selected session.
type ActionSelectSearchResult struct {
	Hit proto.SessionHit
}

// ActionSelectModel is a message indicating a model has been selected.
type ActionSelectModel struct {
	Provider       catwalk.Provider
	Model          config.SelectedModel
	ModelType      config.SelectedModelType
	ReAuthenticate bool
}

// Messages for commands
type (
	ActionNewSession              struct{}
	ActionToggleHelp              struct{}
	ActionToggleCompactMode       struct{}
	ActionToggleThinking          struct{}
	ActionTogglePills             struct{}
	ActionExternalEditor          struct{}
	ActionToggleYoloMode          struct{}
	ActionToggleSysadminMode      struct{}
	ActionToggleNotifications     struct{}
	ActionSelectNotificationStyle struct {
		Style string
	}
	// ActionSelectEmbedding is emitted when an embedding model (or the
	// "disabled" entry) is confirmed in the embeddings picker.
	ActionSelectEmbedding struct {
		Choice EmbeddingChoice
	}
	// ActionStartBackfill requests the embedding-history backfill flow:
	// the UI fetches the pending count, then shows a confirm dialog.
	ActionStartBackfill struct{}
	// ActionConfirmBackfill is emitted when the user confirms the
	// backfill in the confirmation dialog.
	ActionConfirmBackfill             struct{}
	ActionToggleTransparentBackground struct{}
	ActionToggleLowBandwidth          struct{}
	ActionToggleSwarmMode             struct{}
	ActionInitializeProject           struct{}
	// ActionArchiveSession is emitted when the user confirms archiving the
	// current (active) session in the archive-confirmation dialog.
	ActionArchiveSession struct{}
	ActionSummarize      struct {
		SessionID string
	}
	// ActionSelectReasoningEffort is a message indicating a reasoning effort
	// has been selected.
	ActionSelectReasoningEffort struct {
		Effort string
	}
	// ActionPreviewTheme is emitted as the theme picker selection moves, so
	// the UI can apply the theme live as a preview without persisting it.
	ActionPreviewTheme struct {
		Styles styles.Styles
	}
	// ActionSelectTheme is emitted when a theme is confirmed in the picker.
	// The UI persists the name and keeps the (already-previewed) Styles.
	ActionSelectTheme struct {
		Name   string
		Styles styles.Styles
	}
	ActionPermissionResponse struct {
		Permission permission.PermissionRequest
		Action     PermissionAction
	}
	// ActionQuestionResponse is emitted when the user answers (or
	// cancels) a question dialog opened for a pending question.Request.
	ActionQuestionResponse struct {
		Request question.Request
		Answer  question.Answer
	}
	// ActionRunCustomCommand is a message to run a custom command.
	ActionRunCustomCommand struct {
		Content   string
		Arguments []commands.Argument
		Args      map[string]string // Actual argument values
		Skill     *skills.Skill     // Set when this is a skill command
	}
	// ActionAttachSkill is sent when a skill is selected from the commands
	// dialog to be attached to the conversation as a markdown attachment.
	ActionAttachSkill struct {
		ID   string
		Name string
	}
	// ActionRunMCPPrompt is a message to run a custom command.
	ActionRunMCPPrompt struct {
		Title       string
		Description string
		PromptID    string
		ClientID    string
		Arguments   []commands.Argument
		Args        map[string]string // Actual argument values
	}
	// ActionEnableDockerMCP is a message to enable Docker MCP.
	ActionEnableDockerMCP struct{}
	// ActionDisableDockerMCP is a message to disable Docker MCP.
	ActionDisableDockerMCP struct{}
	// ActionReloadConfig is a message to reload the configuration from disk.
	ActionReloadConfig struct{}

	// Snapshot/Worktree actions.

	// ActionOpenSnapshotsDialog opens the snapshots dialog.
	ActionOpenSnapshotsDialog struct {
		SessionID string
	}
	// ActionRestoreSnapshot restores the filesystem to a snapshot.
	ActionRestoreSnapshot struct {
		SnapshotID string
	}
	// ActionOpenWorktreesDialog opens the worktrees dialog.
	ActionOpenWorktreesDialog struct {
		SessionID string
	}
	// ActionCreateWorktree creates a new worktree.
	ActionCreateWorktree struct {
		SessionID      string
		Name           string
		FromSnapshotID string
	}
	// ActionSwitchWorktree switches to a different worktree.
	ActionSwitchWorktree struct {
		SessionID  string
		WorktreeID string
	}
	// ActionRunSnapshotGC runs garbage collection on snapshots.
	ActionRunSnapshotGC struct{}
	// ActionOpenMergeWorktreeDialog opens the merge worktree dialog.
	ActionOpenMergeWorktreeDialog struct {
		WorktreeID   string
		WorktreeName string
	}
	// ActionMergeWorktree merges or rebases a worktree onto a target branch.
	ActionMergeWorktree struct {
		WorktreeID   string
		TargetBranch string
		Rebase       bool
	}
	// ActionOpenForkDialog opens the fork dialog for a specific message.
	ActionOpenForkDialog struct {
		SessionID string
		MessageID string
	}
	// ActionForkConversation forks the conversation from a specific message.
	ActionForkConversation struct {
		SessionID       string
		MessageID       string
		NewSessionTitle string
		CreateWorktree  bool
	}
)

// Messages for API key input dialog.
type (
	ActionChangeAPIKeyState struct {
		State APIKeyInputState
	}
)

// Messages for OAuth2 device flow dialog.
type (
	// ActionInitiateOAuth is sent when the device auth is initiated
	// successfully.
	ActionInitiateOAuth struct {
		DeviceCode      string
		UserCode        string
		ExpiresIn       int
		VerificationURL string
		Interval        int
	}

	// ActionCompleteOAuth is sent when the device flow completes successfully.
	ActionCompleteOAuth struct {
		Token *oauth.Token
	}

	// ActionOAuthErrored is sent when the device flow encounters an error.
	ActionOAuthErrored struct {
		Error error
	}
)

// ActionCmd represents an action that carries a [tea.Cmd] to be passed to the
// Bubble Tea program loop.
type ActionCmd struct {
	Cmd tea.Cmd
}

// ActionScrollToTurn is a message to scroll the chat to a specific turn.
type ActionScrollToTurn struct {
	TurnNumber int
}

// ActionFilePickerSelected is a message indicating a file has been selected in
// the file picker dialog.
type ActionFilePickerSelected struct {
	Path string
}

// Cmd returns a command that reads the file at path and sends a
// [message.Attachement] to the program.
func (a ActionFilePickerSelected) Cmd() tea.Cmd {
	path := a.Path
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		// Images are accepted up to a larger ceiling and downscaled to
		// fit provider limits later (see fitImageAttachments); only
		// non-image files are held to the stricter attachment cap.
		sizeLimit := common.MaxAttachmentSize
		if common.IsImagePath(path) {
			sizeLimit = common.MaxImageAttachmentSize
		}
		isFileLarge, err := common.IsFileTooBig(path, sizeLimit)
		if err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("unable to read the image: %v", err),
			}
		}
		if isFileLarge {
			limitMB := sizeLimit / (1024 * 1024)
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("file too large, max %dMB", limitMB),
			}
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("unable to read the image: %v", err),
			}
		}

		mimeBufferSize := min(512, len(content))
		mimeType := http.DetectContentType(content[:mimeBufferSize])
		fileName := filepath.Base(path)

		return message.Attachment{
			FilePath: path,
			FileName: fileName,
			MimeType: mimeType,
			Content:  content,
		}
	}
}
