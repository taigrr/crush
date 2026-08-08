package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/dialog"
)

// TestPendingPermissions_MultipleSessionsNoClobber verifies concurrent
// background permission requests for different sessions are each cached
// under their own session ID and do not overwrite one another.
func TestPendingPermissions_MultipleSessionsNoClobber(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.session = &session.Session{ID: "session-A"}

	u.pendingPermissions["session-A"] = &permission.PermissionRequest{
		ID: "p-a", SessionID: "session-A", ToolCallID: "tc-a",
	}
	u.pendingPermissions["session-B"] = &permission.PermissionRequest{
		ID: "p-b", SessionID: "session-B", ToolCallID: "tc-b",
	}

	require.NotNil(t, u.pendingPermissions["session-A"])
	require.NotNil(t, u.pendingPermissions["session-B"])
	require.Equal(t, "tc-a", u.pendingPermissions["session-A"].ToolCallID)
	require.Equal(t, "tc-b", u.pendingPermissions["session-B"].ToolCallID)

	ids := u.pendingSessionIDs()
	require.True(t, ids["session-A"])
	require.True(t, ids["session-B"])
}

// TestSyncPermissionDialog_SurfacesRightSessionFromMap verifies that on
// a session switch, the sidebar's cached request for the now-active
// session is the one surfaced — not another session's.
func TestSyncPermissionDialog_SurfacesRightSessionFromMap(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.pendingPermissions["session-A"] = &permission.PermissionRequest{
		ID: "p-a", SessionID: "session-A", ToolCallID: "tc-a",
	}
	u.pendingPermissions["session-B"] = &permission.PermissionRequest{
		ID: "p-b", SessionID: "session-B", ToolCallID: "tc-b",
	}

	// Switch to B: only B's request must surface.
	u.session = &session.Session{ID: "session-B"}
	u.syncPermissionDialogForSession()

	d := u.dialog.Dialog(dialog.PermissionsID)
	require.NotNil(t, d, "a pending request for the active session must surface")
	perm, ok := d.(*dialog.Permissions)
	require.True(t, ok)
	require.Equal(t, "tc-b", perm.ToolCallID(), "the active session's request must surface, not another's")
}

// TestPendingPermissions_ResolutionClearsEntryNoZombie is the zombie
// modal case: a background request resolves (notification) while the
// user is elsewhere; switching to that session afterward must NOT
// resurface a stale prompt.
func TestPendingPermissions_ResolutionClearsEntryNoZombie(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.session = &session.Session{ID: "session-B"}

	// A background request for session-A is cached.
	u.pendingPermissions["session-A"] = &permission.PermissionRequest{
		ID: "p-a", SessionID: "session-A", ToolCallID: "tc-a",
	}

	// It resolves remotely while the user is viewing session-B.
	u.handlePermissionNotification(permission.PermissionNotification{
		SessionID:  "session-A",
		ToolCallID: "tc-a",
		Granted:    true,
	})
	require.Nil(t, u.pendingPermissions["session-A"],
		"resolution must clear the per-session cache")

	// Now switch to session-A: no stale modal must appear.
	u.session = &session.Session{ID: "session-A"}
	u.syncPermissionDialogForSession()
	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"a resolved request must not resurface as a zombie prompt on switch")
}

// TestPendingQuestions_MirrorsPermissionBehavior verifies the question
// path uses the same per-session map: no clobber, right-session surface,
// and resolution clears the entry (no zombie).
func TestPendingQuestions_MirrorsPermissionBehavior(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()

	u.pendingQuestions["session-A"] = &question.Request{
		ID: "q-a", SessionID: "session-A", ToolCallID: "tc-a", Kind: question.KindYesNo, Prompt: "A?",
	}
	u.pendingQuestions["session-B"] = &question.Request{
		ID: "q-b", SessionID: "session-B", ToolCallID: "tc-b", Kind: question.KindYesNo, Prompt: "B?",
	}
	require.Equal(t, "tc-a", u.pendingQuestions["session-A"].ToolCallID)
	require.Equal(t, "tc-b", u.pendingQuestions["session-B"].ToolCallID)

	// Switch to B: only B's question surfaces.
	u.session = &session.Session{ID: "session-B"}
	u.syncQuestionDialogForSession()
	d := u.dialog.Dialog(dialog.QuestionID)
	require.NotNil(t, d)
	q, ok := d.(*dialog.Question)
	require.True(t, ok)
	require.Equal(t, "tc-b", q.ToolCallID())

	// Resolve A remotely, then switch to A: no zombie question modal.
	u.dialog.CloseDialog(dialog.QuestionID)
	u.handleQuestionNotification(question.Notification{
		SessionID:  "session-A",
		ToolCallID: "tc-a",
		Answered:   true,
	})
	require.Nil(t, u.pendingQuestions["session-A"])
	u.session = &session.Session{ID: "session-A"}
	u.syncQuestionDialogForSession()
	require.False(t, u.dialog.ContainsDialog(dialog.QuestionID),
		"a resolved question must not resurface on switch")
}

// TestSetPendingSessions_RedIndicatorSeam verifies the sidebar accessor
// the row renderer consults for the pending indicator.
func TestSetPendingSessions_RedIndicatorSeam(t *testing.T) {
	t.Parallel()

	s := NewSessionsSidebar(nil)
	require.False(t, s.HasPending("session-A"))

	s.SetPendingSessions(map[string]bool{"session-A": true})
	require.True(t, s.HasPending("session-A"))
	require.False(t, s.HasPending("session-B"))

	s.SetPendingSessions(nil)
	require.False(t, s.HasPending("session-A"))
}

// TestBackgroundAttention_BorderState verifies the window attention
// signal: background pending → red (pending), background ready → green,
// both → red wins, only-current-session state → none, empty → none.
func TestBackgroundAttention_BorderState(t *testing.T) {
	t.Parallel()

	mkSidebar := func(sessions []proto.SessionOverview, pending map[string]bool) *SessionsSidebar {
		s := NewSessionsSidebar(nil)
		s.SetOverviews([]proto.WorkspaceOverview{{Root: "/w", Sessions: sessions}})
		s.SetPendingSessions(pending)
		return s
	}

	t.Run("background pending -> red", func(t *testing.T) {
		t.Parallel()
		s := mkSidebar([]proto.SessionOverview{
			{ID: "cur"},
			{ID: "bg"},
		}, map[string]bool{"bg": true})
		require.Equal(t, attentionPending, s.BackgroundAttention("cur"))
	})

	t.Run("no pending, background ready -> green", func(t *testing.T) {
		t.Parallel()
		s := mkSidebar([]proto.SessionOverview{
			{ID: "cur"},
			{ID: "bg", Unread: true},
		}, nil)
		require.Equal(t, attentionReady, s.BackgroundAttention("cur"))
	})

	t.Run("both pending and ready -> red wins", func(t *testing.T) {
		t.Parallel()
		s := mkSidebar([]proto.SessionOverview{
			{ID: "ready", Unread: true},
			{ID: "blocked"},
		}, map[string]bool{"blocked": true})
		require.Equal(t, attentionPending, s.BackgroundAttention("cur"))
	})

	t.Run("only current session has state -> none", func(t *testing.T) {
		t.Parallel()
		// Current session is unread AND has a pending prompt, but it is
		// the one in view, so it must not trigger the border.
		s := mkSidebar([]proto.SessionOverview{
			{ID: "cur", Unread: true},
			{ID: "bg"},
		}, map[string]bool{"cur": true})
		require.Equal(t, attentionNone, s.BackgroundAttention("cur"))
	})

	t.Run("busy background session is not ready", func(t *testing.T) {
		t.Parallel()
		// Unread but busy must not count as ready (matches sessionReady).
		s := mkSidebar([]proto.SessionOverview{
			{ID: "cur"},
			{ID: "bg", Unread: true, IsBusy: true},
		}, nil)
		require.Equal(t, attentionNone, s.BackgroundAttention("cur"))
	})

	t.Run("nothing -> none", func(t *testing.T) {
		t.Parallel()
		s := mkSidebar([]proto.SessionOverview{{ID: "cur"}}, nil)
		require.Equal(t, attentionNone, s.BackgroundAttention("cur"))
	})
}

// TestAttentionBorderColor_ThemedNotHardcoded verifies red uses the
// destructive token and green the ready/success token (same as the row
// indicators), and none yields no border.
func TestAttentionBorderColor_ThemedNotHardcoded(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	sty := u.com.Styles

	require.Equal(t, sty.Resource.ErrorIcon.GetForeground(), u.attentionBorderColor(attentionPending))
	require.Equal(t, sty.Resource.OnlineIcon.GetForeground(), u.attentionBorderColor(attentionReady))
	require.Nil(t, u.attentionBorderColor(attentionNone))
}

// TestHandleAttentionEvent_DrivesPendingAndBorder verifies a global
// attention "blocked" event lights the red row dot / window border for a
// background session in another workspace, and that "resolved" clears it.
func TestHandleAttentionEvent_DrivesPendingAndBorder(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.attentionPending = make(map[string]bool)
	u.session = &session.Session{ID: "focused"}
	// Two workspaces in the sidebar; "bg" lives in another workspace.
	u.leftSidebar.SetOverviews([]proto.WorkspaceOverview{
		{Root: "/w1", Sessions: []proto.SessionOverview{{ID: "focused"}}},
		{Root: "/w2", Sessions: []proto.SessionOverview{{ID: "bg"}}},
	})

	u.handleAttentionEvent(proto.AttentionEvent{
		WorkspaceID:   "w2",
		WorkspaceRoot: "/w2",
		SessionID:     "bg",
		ToolCallID:    "tc",
		Kind:          proto.AttentionBlockedPermission,
	})
	require.True(t, u.attentionPending["bg"])
	require.True(t, u.leftSidebar.HasPending("bg"))
	// A background pending prompt => red window border.
	require.Equal(t, attentionPending, u.leftSidebar.BackgroundAttention("focused"))

	u.handleAttentionEvent(proto.AttentionEvent{
		WorkspaceID: "w2",
		SessionID:   "bg",
		ToolCallID:  "tc",
		Kind:        proto.AttentionResolved,
	})
	require.False(t, u.attentionPending["bg"])
	require.False(t, u.leftSidebar.HasPending("bg"))
	require.Equal(t, attentionNone, u.leftSidebar.BackgroundAttention("focused"))
}
