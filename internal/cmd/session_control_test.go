package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/session"
)

type fakeLister struct {
	sessions []proto.Session
}

func (f fakeLister) GetSession(_ context.Context, _, id string) (*proto.Session, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return &s, nil
		}
	}
	return nil, errors.New("not found")
}

func (f fakeLister) ListSessions(context.Context, string) ([]proto.Session, error) {
	return f.sessions, nil
}

func TestResolveSessionRef(t *testing.T) {
	t.Parallel()

	a := proto.Session{ID: "11111111-1111-4111-8111-11111111aaaa", Color: "coral", Animal: "otter"}
	b := proto.Session{ID: "22222222-2222-4222-8222-22222222bbbb", Color: "coral", Animal: "otter"}
	c := proto.Session{ID: "33333333-3333-4333-8333-33333333cccc", Color: "teal", Animal: "polar bear"}
	l := fakeLister{sessions: []proto.Session{a, b, c}}

	got, err := resolveSessionRef(t.Context(), l, "ws", a.ID)
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID, "full IDs resolve directly")

	got, err = resolveSessionRef(t.Context(), l, "ws", session.HashID(c.ID)[:6])
	require.NoError(t, err)
	require.Equal(t, c.ID, got.ID, "hash prefixes from 'crush session list' resolve")

	got, err = resolveSessionRef(t.Context(), l, "ws", "3333")
	require.NoError(t, err)
	require.Equal(t, c.ID, got.ID, "UUID prefixes from 'crush run' output resolve")

	got, err = resolveSessionRef(t.Context(), l, "ws", session.ShortID(a.ID))
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID, "short IDs from run output resolve")

	got, err = resolveSessionRef(t.Context(), l, "ws", "coral-otter-bbbb")
	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID, "disambiguated swarm addresses resolve")

	got, err = resolveSessionRef(t.Context(), l, "ws", headless.SessionAddress(c))
	require.NoError(t, err)
	require.Equal(t, c.ID, got.ID, "the address printed in results resolves back")

	_, err = resolveSessionRef(t.Context(), l, "ws", "coral-otter")
	require.ErrorContains(t, err, "ambiguous (2 matches)")

	_, err = resolveSessionRef(t.Context(), l, "ws", "nope-nothing")
	require.ErrorContains(t, err, `session "nope-nothing" not found`)

	_, err = resolveSessionRef(t.Context(), l, "ws", "  ")
	require.ErrorContains(t, err, "no session given")
}

func statusForTest(t *testing.T, c *fakeRunClient, format headless.OutputFormat) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := sessionStatus(t.Context(), c, testRunWorkspace(), &proto.Session{
		ID: "sess-aaaa-bbbb", Title: "Fix tests", Color: "blue", Animal: "otter",
		PromptTokens: 10, CompletionTokens: 2, Cost: 0.1,
	}, format, &out)
	return out.String(), err
}

func TestSessionStatus(t *testing.T) {
	t.Parallel()

	t.Run("running", func(t *testing.T) {
		t.Parallel()
		c := newFakeRunClient(nil)
		c.busy = true
		out, err := statusForTest(t, c, headless.FormatJSON)
		require.NoError(t, err)
		lines := decodeLines(t, bytes.NewBufferString(out))
		require.Len(t, lines, 1)
		require.Equal(t, "status", lines[0]["type"])
		require.Equal(t, "running", lines[0]["state"])
		require.Equal(t, "blue-otter-bbbb", lines[0]["address"])
		require.Equal(t, "Fix tests", lines[0]["title"])
		require.Nil(t, lines[0]["last"])
		require.Equal(t, map[string]any{"prompt_tokens": 10.0, "completion_tokens": 2.0, "cost_usd": 0.1}, lines[0]["usage"])
	})

	t.Run("blocked", func(t *testing.T) {
		t.Parallel()
		c := newFakeRunClient(nil)
		c.busy = true
		c.pending = pendingBash()
		c.pending.Questions = []proto.QuestionRequest{{ID: "q1", Prompt: "Which DB?", Options: []string{"pg", "sqlite"}}}
		out, err := statusForTest(t, c, headless.FormatText)
		require.NoError(t, err)
		require.Contains(t, out, "blue-otter-bbbb  blocked")
		require.Contains(t, out, "permission perm-1: bash execute rm -rf build")
		require.Contains(t, out, "question q1: Which DB? [pg | sqlite]")
		require.Contains(t, out, "crush session answer sess-aaaa-bbbb")
	})

	t.Run("idle reports the last turn", func(t *testing.T) {
		t.Parallel()
		c := newFakeRunClient(nil)
		c.messages = []proto.Message{
			{ID: "u1", Role: proto.User, Parts: []proto.ContentPart{proto.TextContent{Text: "hi"}}},
			{ID: "a1", Role: proto.Assistant, Parts: []proto.ContentPart{
				proto.TextContent{Text: "All tests pass."},
				proto.Finish{Reason: proto.FinishReasonEndTurn},
			}},
		}
		out, err := statusForTest(t, c, headless.FormatJSON)
		require.NoError(t, err)
		lines := decodeLines(t, bytes.NewBufferString(out))
		require.Equal(t, "idle", lines[0]["state"])
		last := lines[0]["last"].(map[string]any)
		require.Equal(t, "completed", last["status"])
		require.Equal(t, "All tests pass.", last["text"])

		out, err = statusForTest(t, c, headless.FormatText)
		require.NoError(t, err)
		require.Contains(t, out, "last: completed")
		require.Contains(t, out, "All tests pass.")
	})
}

func waitForTest(t *testing.T, c *fakeRunClient, format headless.OutputFormat) ([]map[string]any, error) {
	t.Helper()
	var out bytes.Buffer
	err := sessionWait(t.Context(), c, testRunWorkspace(), &proto.Session{ID: "sess-aaaa-bbbb"}, 0, format, &out)
	return decodeLines(t, &out), err
}

func TestSessionWait_IdleSessionReturnsLastTurn(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.messages = []proto.Message{{ID: "a1", Role: proto.Assistant, Parts: []proto.ContentPart{
		proto.TextContent{Text: "boom"},
		proto.Finish{Reason: proto.FinishReasonError, Message: "provider overloaded"},
	}}}
	lines, err := waitForTest(t, c, headless.FormatJSON)
	require.Equal(t, headless.ExitAgentError, headless.ExitCode(err))
	require.Equal(t, "error", lines[0]["status"])
	require.Equal(t, "provider overloaded", lines[0]["error"])
	require.Equal(t, "a1", lines[0]["message_id"])
}

func TestSessionWait_RunningSessionCompletes(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.busy = true
	go func() {
		// Give Watch time to see the session as busy before it ends.
		time.Sleep(50 * time.Millisecond)
		c.events <- asstMsg("m1", "work")
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
		c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{SessionID: "sess-aaaa-bbbb", MessageID: "m1", Text: "work done"}}
	}()
	lines, err := waitForTest(t, c, headless.FormatStreamJSON)
	require.NoError(t, err)
	last := lines[len(lines)-1]
	require.Equal(t, "result", last["type"])
	require.Equal(t, "completed", last["status"])
	require.Equal(t, "work done", last["text"])
}

func TestSessionWait_BlockedSession(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.busy = true
	c.pending = pendingBash()
	lines, err := waitForTest(t, c, headless.FormatJSON)
	require.Equal(t, headless.ExitBlocked, headless.ExitCode(err))
	require.Equal(t, "blocked", lines[0]["status"])
	require.NotNil(t, lines[0]["pending"])
}

func TestSessionAnswer(t *testing.T) {
	t.Parallel()

	t.Run("permission", func(t *testing.T) {
		t.Parallel()
		c := newFakeRunClient(nil)
		c.pending = pendingBash()
		var out bytes.Buffer
		err := sessionAnswer(t.Context(), c, testRunWorkspace(), &proto.Session{ID: "sess-aaaa-bbbb"},
			headless.AnswerRequest{Permission: proto.PermissionAllowForSession}, false, 0, headless.FormatJSON, &out)
		require.NoError(t, err)
		require.Len(t, c.granted, 1)
		require.Equal(t, "perm-1", c.granted[0].Permission.ID)
		require.Equal(t, proto.PermissionAllowForSession, c.granted[0].Action)

		lines := decodeLines(t, &out)
		require.Equal(t, "answer", lines[0]["type"])
		require.Equal(t, "permission", lines[0]["kind"])
		require.Equal(t, true, lines[0]["resolved"])
	})

	t.Run("question then wait", func(t *testing.T) {
		t.Parallel()
		c := newFakeRunClient(nil)
		c.pending = proto.PendingPrompts{Questions: []proto.QuestionRequest{{ID: "q1", SessionID: "sess-aaaa-bbbb", ToolCallID: "tc9", Prompt: "Name?"}}}
		c.messages = []proto.Message{{ID: "a1", Role: proto.Assistant, Parts: []proto.ContentPart{proto.TextContent{Text: "Thanks, Ada"}}}}
		var out bytes.Buffer
		err := sessionAnswer(t.Context(), c, testRunWorkspace(), &proto.Session{ID: "sess-aaaa-bbbb"},
			headless.AnswerRequest{Answers: []string{"Ada"}}, true, 0, headless.FormatText, &out)
		require.NoError(t, err)
		require.Len(t, c.answered, 1)
		require.Equal(t, proto.QuestionAnswer{ID: "q1", SessionID: "sess-aaaa-bbbb", ToolCallID: "tc9", Selected: []string{"Ada"}}, c.answered[0])
		require.Equal(t, "Thanks, Ada\n", out.String(), "--wait prints the run's result")
	})

	t.Run("nothing pending", func(t *testing.T) {
		t.Parallel()
		c := newFakeRunClient(nil)
		err := sessionAnswer(t.Context(), c, testRunWorkspace(), &proto.Session{ID: "s"},
			headless.AnswerRequest{Permission: proto.PermissionDeny}, false, 0, headless.FormatText, &bytes.Buffer{})
		require.ErrorIs(t, err, headless.ErrNothingPending)
	})
}

func TestSessionCancel(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.busy = true
	var out bytes.Buffer
	require.NoError(t, sessionCancel(t.Context(), c, testRunWorkspace(), &proto.Session{ID: "s"}, headless.FormatJSON, &out))
	require.Equal(t, []string{"s"}, c.cancelled)
	lines := decodeLines(t, &out)
	require.Equal(t, map[string]any{"type": "cancel", "session_id": "s", "was_busy": true}, lines[0])

	c = newFakeRunClient(nil)
	out.Reset()
	require.NoError(t, sessionCancel(t.Context(), c, testRunWorkspace(), &proto.Session{ID: "s"}, headless.FormatText, &out))
	require.Equal(t, "s was not running\n", out.String())
}

func TestAnswerRequestFromFlags(t *testing.T) {
	t.Parallel()

	parse := func(args ...string) (headless.AnswerRequest, error) {
		cmd := &cobra.Command{Use: "answer"}
		addAnswerFlags(cmd)
		require.NoError(t, cmd.ParseFlags(args))
		return answerRequestFromFlags(cmd)
	}

	req, err := parse("--allow")
	require.NoError(t, err)
	require.Equal(t, proto.PermissionAllow, req.Permission)

	req, err = parse("--allow-session", "--request", "r1")
	require.NoError(t, err)
	require.Equal(t, proto.PermissionAllowForSession, req.Permission)
	require.Equal(t, "r1", req.RequestID)

	req, err = parse("--answer", "a", "--answer", "b")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, req.Answers)

	_, err = parse()
	require.ErrorContains(t, err, "specify one of")
}

func TestSessionControlCommandsRegistered(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"status", "wait", "answer", "cancel"} {
		found := false
		for _, c := range sessionCmd.Commands() {
			if c.Name() == name {
				found = true
				require.NotNil(t, c.Flags().Lookup("output-format"), name)
			}
		}
		require.True(t, found, name)
	}
}
