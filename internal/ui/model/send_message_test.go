package model

import (
	"context"
	"errors"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/attachments"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/version"
	"github.com/taigrr/crush/internal/workspace"
)

// sendWorkspace is a workspace stub that lets tests drive the agent
// readiness/init behavior used by sendMessage.
type sendWorkspace struct {
	workspace.Workspace

	readiness    func(ctx context.Context) (bool, error)
	initErr      error
	initCalls    int
	createCalled bool
	serverVer    proto.VersionInfo
	serverVerErr error
}

func (w *sendWorkspace) Config() *config.Config { return nil }

func (w *sendWorkspace) ConnectionState() workspace.ConnectionState {
	return workspace.ConnectionStateConnected
}

func (w *sendWorkspace) ServerVersion(ctx context.Context) (proto.VersionInfo, error) {
	return w.serverVer, w.serverVerErr
}

func (w *sendWorkspace) AgentReadiness(ctx context.Context) (bool, error) {
	return w.readiness(ctx)
}

func (w *sendWorkspace) AgentIsReady() bool {
	ready, err := w.readiness(context.Background())
	return err == nil && ready
}

func (w *sendWorkspace) InitCoderAgent(ctx context.Context) error {
	w.initCalls++
	return w.initErr
}

func newSendTestUI(t *testing.T, ws workspace.Workspace) *UI {
	t.Helper()

	com := common.DefaultCommon(ws)

	ta := textarea.New()
	ta.SetStyles(com.Styles.Editor.Textarea)
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.MinHeight = TextareaMinHeight
	ta.MaxHeight = TextareaMaxHeight

	att := attachments.New(
		attachments.NewRenderer(
			com.Styles.Attachments.Normal,
			com.Styles.Attachments.Deleting,
			com.Styles.Attachments.Image,
			com.Styles.Attachments.Text,
			com.Styles.Attachments.Skill,
		),
		attachments.Keymap{},
	)

	return &UI{
		com:         com,
		textarea:    ta,
		attachments: att,
		state:       uiChat,
		focus:       uiFocusEditor,
		width:       140,
		height:      45,
	}
}

func TestSendMessage_NetworkErrorPreservesPrompt(t *testing.T) {
	t.Parallel()

	ws := &sendWorkspace{
		readiness: func(context.Context) (bool, error) {
			return false, errors.New("dial tcp: connection refused")
		},
	}
	ui := newSendTestUI(t, ws)

	cmd := ui.sendMessage("my important prompt")
	require.NotNil(t, cmd, "expected an error command")

	require.Equal(t, "my important prompt", ui.textarea.Value(),
		"prompt must be restored on network error")
	require.Zero(t, ws.initCalls,
		"InitCoderAgent must not be called on a network error")
	require.False(t, ws.createCalled)
}

func TestSendMessage_VersionMismatchPreservesPrompt(t *testing.T) {
	t.Parallel()

	ws := &sendWorkspace{
		readiness: func(context.Context) (bool, error) { return false, nil },
	}
	ui := newSendTestUI(t, ws)
	ui.versionMismatch = true
	ui.serverVersionStr = "9.9.9"

	cmd := ui.sendMessage("keep me")
	require.NotNil(t, cmd)

	require.Equal(t, "keep me", ui.textarea.Value(),
		"prompt must be restored on version mismatch")
	require.Zero(t, ws.initCalls,
		"a version mismatch must not attempt re-initialization")
}

func TestSendMessage_NotReadyRetriesInitThenPreserves(t *testing.T) {
	t.Parallel()

	ws := &sendWorkspace{
		readiness: func(context.Context) (bool, error) { return false, nil },
		initErr:   errors.New("boom"),
	}
	ui := newSendTestUI(t, ws)

	cmd := ui.sendMessage("do not lose this")
	require.NotNil(t, cmd)

	require.Equal(t, 1, ws.initCalls,
		"reachable-but-not-ready must attempt InitCoderAgent once")
	require.Equal(t, "do not lose this", ui.textarea.Value(),
		"prompt must be restored when re-init fails")
}

func TestSendMessage_NotReadyReInitSucceedsButStillNotReady(t *testing.T) {
	t.Parallel()

	// Init returns nil but the server still reports not-ready: the prompt
	// must still be preserved rather than dropped.
	ws := &sendWorkspace{
		readiness: func(context.Context) (bool, error) { return false, nil },
	}
	ui := newSendTestUI(t, ws)

	cmd := ui.sendMessage("still here")
	require.NotNil(t, cmd)
	require.Equal(t, 1, ws.initCalls)
	require.Equal(t, "still here", ui.textarea.Value())
}

func TestRestoreUnsentPrompt_RestoresAttachments(t *testing.T) {
	t.Parallel()

	ws := &sendWorkspace{
		readiness: func(context.Context) (bool, error) { return false, nil },
	}
	ui := newSendTestUI(t, ws)

	att := message.Attachment{FileName: "a.md", MimeType: "text/markdown"}
	cmd := ui.restoreUnsentPrompt("text", []message.Attachment{att}, errors.New("x"))
	require.NotNil(t, cmd)

	require.Equal(t, "text", ui.textarea.Value())
	require.Len(t, ui.attachments.List(), 1)
	require.Equal(t, "a.md", ui.attachments.List()[0].FileName)
}

func TestCheckServerVersion_MatchNoMismatch(t *testing.T) {
	t.Parallel()

	ws := &sendWorkspace{
		readiness: func(context.Context) (bool, error) { return true, nil },
		serverVer: proto.VersionInfo{Version: version.Version, BuildID: version.BuildID},
	}
	ui := newSendTestUI(t, ws)

	msg := ui.checkServerVersion()()
	sv, ok := msg.(serverVersionMsg)
	require.True(t, ok)
	require.False(t, sv.mismatch, "identical version/build must not report a mismatch")
}

func TestCheckServerVersion_DifferentVersionReportsMismatch(t *testing.T) {
	t.Parallel()

	ws := &sendWorkspace{
		readiness: func(context.Context) (bool, error) { return true, nil },
		serverVer: proto.VersionInfo{Version: version.Version + "-old", BuildID: version.BuildID},
	}
	ui := newSendTestUI(t, ws)

	msg := ui.checkServerVersion()()
	sv, ok := msg.(serverVersionMsg)
	require.True(t, ok)
	require.True(t, sv.mismatch, "a differing version must report a mismatch")
}

func TestCheckServerVersion_ErrorIsIgnored(t *testing.T) {
	t.Parallel()

	// A transient error must not flash the mismatch banner: no message
	// is emitted so the existing state is preserved.
	ws := &sendWorkspace{
		readiness:    func(context.Context) (bool, error) { return true, nil },
		serverVerErr: errors.New("connection refused"),
	}
	ui := newSendTestUI(t, ws)

	require.Nil(t, ui.checkServerVersion()(),
		"a version-check error must not emit a mismatch message")
}
