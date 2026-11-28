package model

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/termstatus"
)

func rawOf(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	require.NotNil(t, cmd)
	msg := cmd()
	raw, ok := msg.(tea.RawMsg)
	require.True(t, ok)
	s, ok := raw.Msg.(string)
	require.True(t, ok)
	return s
}

func TestProgramStatusCmd(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}
	const openID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const otherID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	u.session = &session.Session{ID: openID, Title: "Open"}
	u.leftSidebar.SetOverviews([]proto.WorkspaceOverview{{
		Sessions: []proto.SessionOverview{
			{ID: openID, Title: "Open"},
			{ID: otherID, Title: "Other", IsBusy: true},
		},
	}})

	first := rawOf(t, u.programStatusCmd())
	require.Contains(t, first, "state=idle:id=")
	require.Contains(t, first, "state=working:id=")
	require.Contains(t, first, "title="+b64Title("Open"))
	require.Contains(t, first, "title="+b64Title("Other"))
	require.Nil(t, u.programStatusCmd())

	u.pendingPermissions[openID] = &permission.PermissionRequest{SessionID: openID, Description: "Allow bash?"}
	blocked := rawOf(t, u.programStatusCmd())
	require.Contains(t, blocked, "state=blocked:id=")
	require.Contains(t, blocked, "kind=permission")
	require.NotContains(t, blocked, "state=working") // the other session did not change

	delete(u.pendingPermissions, openID)
	u.pendingQuestions[otherID] = &question.Request{SessionID: otherID, Prompt: "Which?"}
	q := rawOf(t, u.programStatusCmd())
	require.Contains(t, q, "kind=question")
	require.Contains(t, q, "state=idle")

	u.leftSidebar.SetOverviews(nil)
	cleared := rawOf(t, u.programStatusCmd())
	require.Contains(t, cleared, "state=clear:id=")
	require.True(t, strings.HasPrefix(cleared, "\x1b]7501;"))
}

func TestNoteRunComplete(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.noteRunComplete(notify.RunComplete{SessionID: "s", Cancelled: true})
	require.Equal(t, termstatus.StateIdle, u.sessionOutcome["s"].state)
	u.noteRunComplete(notify.RunComplete{SessionID: "s", Error: "boom"})
	require.Equal(t, termstatus.StateError, u.sessionOutcome["s"].state)
	u.noteRunComplete(notify.RunComplete{SessionID: "s", Text: "first line\nrest"})
	require.Equal(t, termstatus.StateDone, u.sessionOutcome["s"].state)
	require.Equal(t, "first line", u.sessionOutcome["s"].msg)
}

func TestProgramStatusNoSessionEmitsNothing(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}
	require.Nil(t, u.programStatusCmd())
}

func TestBusySessionDropsOutcome(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	u.session = &session.Session{ID: id, Title: "Open"}
	u.noteRunComplete(notify.RunComplete{SessionID: id, Text: "done"})
	require.Contains(t, rawOf(t, u.programStatusCmd()), "state=done")

	u.leftSidebar.SetOverviews([]proto.WorkspaceOverview{{
		Sessions: []proto.SessionOverview{{ID: id, Title: "Open", IsBusy: true}},
	}})
	u.session = nil
	got := rawOf(t, u.programStatusCmd())
	require.Contains(t, got, "state=working")
	require.NotContains(t, got, "state=done")
	_, kept := u.sessionOutcome[id]
	require.False(t, kept)
}

func TestRecordIDStable(t *testing.T) {
	t.Parallel()
	a := recordID("11111111-1111-1111-1111-111111111111")
	b := recordID("22222222-2222-2222-2222-111111111111")
	require.NotEqual(t, a, b)
	require.LessOrEqual(t, len(a), 128)
	require.NotContains(t, a, "-")
	require.NotEmpty(t, termstatus.Report{State: termstatus.StateIdle, ID: a}.Sequence())
	require.Empty(t, recordID(""))
}

func TestProgramStatusClearedOnQuit(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}
	u.session = &session.Session{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Title: "Open"}
	require.NotNil(t, u.programStatusCmd())

	u.quitting = true
	require.Equal(t, "\x1b]7501;state=clear:id=s\x07", rawOf(t, u.programStatusClearCmd()))
	require.Nil(t, u.programStatusCmd(), "nothing is reported again after quitting")
	require.Nil(t, u.programStatusClearCmd(), "cleared once")
}

func TestProgramStatusTitleUpdatesWhenGenerated(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	u.session = &session.Session{ID: id, Title: "New Session"}

	first := rawOf(t, u.programStatusCmd())
	require.NotContains(t, first, "title=")

	u.session.Title = "Fix the login form"
	updated := rawOf(t, u.programStatusCmd())
	require.Contains(t, updated, "title="+b64Title("Fix the login form"))
	require.NotContains(t, updated, "title="+b64Title("New Session"))
	require.Nil(t, u.programStatusCmd())
}

func b64Title(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestProgramStatusClearNothingWritten(t *testing.T) {
	t.Parallel()
	require.Nil(t, newTestUI().programStatusClearCmd())
}
