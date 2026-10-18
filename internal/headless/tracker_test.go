package headless

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// recorder is an [Emitter] that keeps every emitted event.
type recorder struct {
	events []any
	err    error
}

func (r *recorder) Emit(v any) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, v)
	return nil
}

func (r *recorder) texts() string {
	var b strings.Builder
	for _, ev := range r.events {
		if te, ok := ev.(TextEvent); ok {
			b.WriteString(te.Text)
		}
	}
	return b.String()
}

func assistant(id, text string, parts ...proto.ContentPart) pubsub.Event[proto.Message] {
	all := append([]proto.ContentPart{proto.TextContent{Text: text}}, parts...)
	return pubsub.Event[proto.Message]{Payload: proto.Message{
		ID: id, SessionID: "S", Role: proto.Assistant, Parts: all,
	}}
}

func runComplete(rc proto.RunComplete) pubsub.Event[proto.RunComplete] {
	return pubsub.Event[proto.RunComplete]{Payload: rc}
}

func TestTracker_StreamsTextDeltas(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tr := NewTracker("S", "R", rec)

	for _, text := range []string{"Hel", "Hello", "Hello, wor", "Hello, world"} {
		out, err := tr.Handle(assistant("m1", text))
		require.NoError(t, err)
		require.Nil(t, out)
	}
	require.Equal(t, "Hello, world", rec.texts())
	require.Len(t, rec.events, 4)
}

func TestTracker_DuplicateAndRegressedUpdatesEmitNothing(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tr := NewTracker("S", "R", rec)

	_, _ = tr.Handle(assistant("m1", "abc"))
	_, _ = tr.Handle(assistant("m1", "abc"))
	_, _ = tr.Handle(assistant("m1", "ab"))
	require.Equal(t, "abc", rec.texts())

	// After a regression the cursor rebases on the shorter text.
	_, _ = tr.Handle(assistant("m1", "abX"))
	require.Equal(t, "abcX", rec.texts())
}

func TestTracker_IgnoresOtherSessions(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tr := NewTracker("S", "R", rec)

	ev := assistant("m1", "nope")
	ev.Payload.SessionID = "OTHER"
	out, err := tr.Handle(ev)
	require.NoError(t, err)
	require.Nil(t, out)
	require.Empty(t, rec.events)

	out, err = tr.Handle(runComplete(proto.RunComplete{SessionID: "S", RunID: "OTHER-RUN"}))
	require.NoError(t, err)
	require.Nil(t, out, "a RunComplete for another run on the same session must not end ours")
}

func TestTracker_ToolCallsAndResults(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tr := NewTracker("S", "R", rec)

	// Input still streaming: not emitted yet.
	_, err := tr.Handle(assistant("m1", "", proto.ToolCall{ID: "tc1", Name: "bash", Input: `{"comm`}))
	require.NoError(t, err)
	require.Empty(t, rec.events)

	// Finished input: emitted once, even if the message is re-published.
	done := assistant("m1", "", proto.ToolCall{ID: "tc1", Name: "bash", Input: `{"command":"ls"}`, Finished: true})
	_, err = tr.Handle(done)
	require.NoError(t, err)
	_, err = tr.Handle(done)
	require.NoError(t, err)

	// Non-JSON input on an otherwise finished message is quoted.
	_, err = tr.Handle(assistant("m2", "", proto.ToolCall{ID: "tc2", Name: "raw", Input: "not json"}, proto.Finish{Reason: proto.FinishReasonToolUse}))
	require.NoError(t, err)

	long := strings.Repeat("é", MaxToolResultBytes)
	toolMsg := pubsub.Event[proto.Message]{Payload: proto.Message{
		ID: "t1", SessionID: "S", Role: proto.Tool,
		Parts: []proto.ContentPart{
			proto.ToolResult{ToolCallID: "tc1", Name: "bash", Content: "file.go"},
			proto.ToolResult{ToolCallID: "tc2", Name: "raw", Content: long, IsError: true},
		},
	}}
	_, err = tr.Handle(toolMsg)
	require.NoError(t, err)
	_, err = tr.Handle(toolMsg)
	require.NoError(t, err)

	require.Len(t, rec.events, 4)

	call1 := rec.events[0].(ToolCallEvent)
	require.Equal(t, EventToolCall, call1.Type)
	require.Equal(t, "tc1", call1.ToolCallID)
	require.JSONEq(t, `{"command":"ls"}`, string(call1.Input))

	call2 := rec.events[1].(ToolCallEvent)
	require.JSONEq(t, `"not json"`, string(call2.Input))

	res1 := rec.events[2].(ToolResultEvent)
	require.Equal(t, "file.go", res1.Content)
	require.False(t, res1.Truncated)

	res2 := rec.events[3].(ToolResultEvent)
	require.True(t, res2.IsError)
	require.True(t, res2.Truncated)
	require.LessOrEqual(t, len(res2.Content), MaxToolResultBytes)
	require.True(t, strings.HasPrefix(long, res2.Content))
	require.True(t, json.Valid(mustJSON(t, res2)), "truncation must not split a UTF-8 rune")
}

func TestTracker_RunCompleteOutcomes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rc   proto.RunComplete
		want Status
	}{
		{"completed", proto.RunComplete{RunID: "R", MessageID: "m1", Text: "done"}, StatusCompleted},
		{"error", proto.RunComplete{RunID: "R", Error: "boom"}, StatusError},
		{"cancelled", proto.RunComplete{RunID: "R", Cancelled: true, Error: "context canceled"}, StatusCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := NewTracker("S", "R", nil)
			out, err := tr.Handle(runComplete(tc.rc))
			require.NoError(t, err)
			require.NotNil(t, out)
			require.Equal(t, tc.want, out.Status)
			require.Equal(t, tc.rc.Error, out.Error)
		})
	}
}

func TestTracker_RunCompleteReconcilesFinalText(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tr := NewTracker("S", "R", rec)

	_, _ = tr.Handle(assistant("m1", "partial"))
	out, err := tr.Handle(runComplete(proto.RunComplete{SessionID: "S", RunID: "R", MessageID: "m1", Text: "partial and final"}))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "partial and final", out.Text)
	require.Equal(t, "m1", out.MessageID)
	require.Equal(t, "partial and final", rec.texts())
}

func TestTracker_RunCompleteFallsBackToStreamedText(t *testing.T) {
	t.Parallel()

	tr := NewTracker("S", "R", nil)
	_, _ = tr.Handle(assistant("m1", "streamed"))
	out, err := tr.Handle(runComplete(proto.RunComplete{RunID: "R", MessageID: "m1"}))
	require.NoError(t, err)
	require.Equal(t, "streamed", out.Text)
}

func TestTracker_SessionFallbackWithoutRunID(t *testing.T) {
	t.Parallel()

	tr := NewTracker("S", "", nil)
	out, err := tr.Handle(runComplete(proto.RunComplete{SessionID: "OTHER"}))
	require.NoError(t, err)
	require.Nil(t, out)

	out, err = tr.Handle(runComplete(proto.RunComplete{SessionID: "S"}))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, StatusCompleted, out.Status)
}

func TestTracker_AgentErrorAttribution(t *testing.T) {
	t.Parallel()

	tr := NewTracker("S", "R", nil)
	agentErr := func(ev proto.AgentEvent) pubsub.Event[proto.AgentEvent] {
		return pubsub.Event[proto.AgentEvent]{Payload: ev}
	}

	out, _ := tr.Handle(agentErr(proto.AgentEvent{Type: proto.AgentEventTypeResponse}))
	require.Nil(t, out, "non-error agent events are ignored")

	out, _ = tr.Handle(agentErr(proto.AgentEvent{Error: errors.New("x"), RunID: "OTHER"}))
	require.Nil(t, out, "errors from another run are ignored")

	out, _ = tr.Handle(agentErr(proto.AgentEvent{Error: errors.New("x"), SessionID: ""}))
	require.Nil(t, out, "unattributed errors are ignored")

	out, _ = tr.Handle(agentErr(proto.AgentEvent{Error: errors.New("x"), SessionID: "OTHER"}))
	require.Nil(t, out)

	out, _ = tr.Handle(agentErr(proto.AgentEvent{Error: errors.New("bad key"), SessionID: "S"}))
	require.NotNil(t, out)
	require.Equal(t, StatusError, out.Status)
	require.Equal(t, "bad key", out.Error)

	out, _ = tr.Handle(agentErr(proto.AgentEvent{Error: errors.New("ours"), RunID: "R"}))
	require.NotNil(t, out)
	require.Equal(t, "ours", out.Error)
}

func TestTracker_AwaitUserPromptHoldsBackForeignTurn(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tr := NewTracker("S", "R", rec)
	tr.AwaitUserPrompt("  my prompt\n")

	// Output of the turn ahead of ours.
	_, _ = tr.Handle(assistant("prev", "earlier turn output"))
	foreignUser := pubsub.Event[proto.Message]{Payload: proto.Message{
		ID: "u0", SessionID: "S", Role: proto.User,
		Parts: []proto.ContentPart{proto.TextContent{Text: "someone else"}},
	}}
	_, _ = tr.Handle(foreignUser)
	require.Empty(t, rec.events)

	ourUser := pubsub.Event[proto.Message]{Payload: proto.Message{
		ID: "u1", SessionID: "S", Role: proto.User,
		Parts: []proto.ContentPart{proto.TextContent{Text: "my prompt"}},
	}}
	_, _ = tr.Handle(ourUser)
	_, _ = tr.Handle(assistant("m1", "our output"))
	require.Equal(t, "our output", rec.texts())
}

func TestTracker_EmitErrorPropagates(t *testing.T) {
	t.Parallel()

	rec := &recorder{err: errors.New("broken pipe")}
	tr := NewTracker("S", "R", rec)
	_, err := tr.Handle(assistant("m1", "x"))
	require.EqualError(t, err, "broken pipe")

	_, err = tr.Handle(runComplete(proto.RunComplete{RunID: "R", MessageID: "m2", Text: "y"}))
	require.EqualError(t, err, "broken pipe")
}

func TestNDJSONEmitter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	e := NewNDJSONEmitter(&buf)
	require.NoError(t, e.Emit(TextEvent{Type: EventText, SessionID: "S", MessageID: "m", Text: "<a&b>"}))
	require.NoError(t, e.Emit(NewResult(StatusCompleted)))

	sc := bufio.NewScanner(&buf)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], `"text":"<a&b>"`, "HTML must not be escaped")

	var res map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &res))
	require.Equal(t, "result", res["type"])
	require.Equal(t, "completed", res["status"])
	require.Equal(t, []any{}, res["files_changed"])
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	s, cut := truncate("short", 10)
	require.Equal(t, "short", s)
	require.False(t, cut)

	s, cut = truncate("abcdef", 3)
	require.Equal(t, "abc", s)
	require.True(t, cut)

	// "é" is two bytes; cutting at 3 must back off to a rune boundary.
	s, cut = truncate("éé", 3)
	require.Equal(t, "é", s)
	require.True(t, cut)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
