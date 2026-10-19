package headless

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

func TestParsePermissionMode(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]PermissionMode{
		"":         ModeYOLO,
		"yolo":     ModeYOLO,
		"DENY":     ModeDeny,
		"delegate": ModeDelegate,
	} {
		got, err := ParsePermissionMode(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	_, err := ParsePermissionMode("ask")
	require.ErrorContains(t, err, `invalid permission mode "ask" (want one of yolo, deny, delegate)`)
}

func TestPermissionModePolicy(t *testing.T) {
	t.Parallel()

	require.Equal(t, proto.PermissionPolicyAllow, ModeYOLO.Policy())
	require.Equal(t, proto.PermissionPolicyDeny, ModeDeny.Policy())
	require.Equal(t, proto.PermissionPolicyPrompt, ModeDelegate.Policy())
	require.True(t, ModeDelegate.Delegates())
	require.False(t, ModeYOLO.Delegates())
	require.False(t, ModeDeny.Delegates())
}

func TestAnswer(t *testing.T) {
	t.Parallel()

	perm := proto.PermissionRequest{ID: "p1", SessionID: "child", ToolName: "bash"}
	q := proto.QuestionRequest{ID: "q1", SessionID: "S", ToolCallID: "tc", Prompt: "Name?"}

	t.Run("permission", func(t *testing.T) {
		t.Parallel()
		c := newFakeWatchClient()
		c.pending = proto.PendingPrompts{Permissions: []proto.PermissionRequest{perm}}
		res, err := Answer(t.Context(), c, "ws", "S", AnswerRequest{Permission: proto.PermissionDeny})
		require.NoError(t, err)
		require.Equal(t, AnswerResult{Type: EventAnswer, Kind: AnswerKindPermission, RequestID: "p1", SessionID: "child", Resolved: true}, res)
		require.Equal(t, []proto.PermissionGrant{{Permission: perm, Action: proto.PermissionDeny}}, c.grants)
	})

	t.Run("question answer and cancel", func(t *testing.T) {
		t.Parallel()
		c := newFakeWatchClient()
		c.pending = proto.PendingPrompts{Questions: []proto.QuestionRequest{q}}
		c.resolved = false
		res, err := Answer(t.Context(), c, "ws", "S", AnswerRequest{Answers: []string{"Ada"}})
		require.NoError(t, err)
		require.Equal(t, AnswerKindQuestion, res.Kind)
		require.False(t, res.Resolved, "losing the race to another client is reported, not an error")

		_, err = Answer(t.Context(), c, "ws", "S", AnswerRequest{CancelQuestion: true, RequestID: "q1"})
		require.NoError(t, err)
		require.Equal(t, []proto.QuestionAnswer{
			{ID: "q1", SessionID: "S", ToolCallID: "tc", Selected: []string{"Ada"}},
			{ID: "q1", SessionID: "S", ToolCallID: "tc", Cancelled: true},
		}, c.answers)
	})

	t.Run("validation", func(t *testing.T) {
		t.Parallel()
		c := newFakeWatchClient()
		c.pending = proto.PendingPrompts{
			Permissions: []proto.PermissionRequest{perm, {ID: "p2"}},
		}

		_, err := Answer(t.Context(), c, "ws", "S", AnswerRequest{})
		require.ErrorContains(t, err, "exactly one")
		_, err = Answer(t.Context(), c, "ws", "S", AnswerRequest{Permission: proto.PermissionAllow, Answers: []string{"x"}})
		require.ErrorContains(t, err, "exactly one")
		_, err = Answer(t.Context(), c, "ws", "S", AnswerRequest{Permission: "maybe"})
		require.ErrorContains(t, err, `invalid permission decision "maybe"`)

		_, err = Answer(t.Context(), c, "ws", "S", AnswerRequest{Permission: proto.PermissionAllow})
		require.ErrorContains(t, err, "2 permission requests are pending; pass the request ID")

		res, err := Answer(t.Context(), c, "ws", "S", AnswerRequest{Permission: proto.PermissionAllow, RequestID: "p2"})
		require.NoError(t, err)
		require.Equal(t, "p2", res.RequestID)

		_, err = Answer(t.Context(), c, "ws", "S", AnswerRequest{Permission: proto.PermissionAllow, RequestID: "zzz"})
		require.ErrorIs(t, err, ErrNothingPending)

		_, err = Answer(t.Context(), c, "ws", "S", AnswerRequest{Answers: []string{"x"}})
		require.ErrorIs(t, err, ErrNothingPending)
	})
}

func TestBuildStatus(t *testing.T) {
	t.Parallel()

	sess := proto.Session{ID: "sess-1234", Title: "T", Color: "red", Animal: "fox", PromptTokens: 5, Cost: 0.2}

	c := newFakeWatchClient()
	c.queued = 2
	c.setBusy(true)
	st, err := BuildStatus(t.Context(), c, "ws", "/cwd", sess)
	require.NoError(t, err)
	require.Equal(t, StateRunning, st.State)
	require.Equal(t, EventStatus, st.Type)
	require.Equal(t, "red-fox-1234", st.Address)
	require.Equal(t, 2, st.QueuedPrompts)
	require.Equal(t, Usage{PromptTokens: 5, CostUSD: 0.2}, st.Usage)
	require.Equal(t, "crush --cwd /cwd --session sess-1234", st.AttachCommand)
	require.Nil(t, st.Pending)
	require.Nil(t, st.Last)

	c.pending = proto.PendingPrompts{Permissions: []proto.PermissionRequest{{ID: "p"}}}
	st, err = BuildStatus(t.Context(), c, "ws", "/cwd", sess)
	require.NoError(t, err)
	require.Equal(t, StateBlocked, st.State)
	require.NotNil(t, st.Pending)

	c = newFakeWatchClient()
	c.messages = []proto.Message{asst("a", "bye", proto.Finish{Reason: proto.FinishReasonEndTurn})}
	st, err = BuildStatus(t.Context(), c, "ws", "/cwd", sess)
	require.NoError(t, err)
	require.Equal(t, StateIdle, st.State)
	require.Equal(t, &LastTurn{Status: StatusCompleted, MessageID: "a", Text: "bye"}, st.Last)
}
