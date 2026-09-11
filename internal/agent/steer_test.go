package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/fantasy"
)

// toolThenFinishModel emits a call to the "wait" tool on each of its first
// toolSteps Streams (default 1) and a plain text finish on every later one.
// It records the prompt of each Stream call so tests can assert on the
// messages the model saw at each step boundary.
type toolThenFinishModel struct {
	toolSteps int
	mu        sync.Mutex
	prompts   []fantasy.Prompt
}

func (m *toolThenFinishModel) Provider() string { return "fake" }
func (m *toolThenFinishModel) Model() string    { return "fake-model" }

func (m *toolThenFinishModel) record(call fantasy.Call) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompts = append(m.prompts, call.Prompt)
	return len(m.prompts)
}

func (m *toolThenFinishModel) Prompts() []fantasy.Prompt {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]fantasy.Prompt(nil), m.prompts...)
}

func (m *toolThenFinishModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *toolThenFinishModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	n := m.record(call)
	toolSteps := max(m.toolSteps, 1)
	return func(yield func(fantasy.StreamPart) bool) {
		if n <= toolSteps {
			id := fmt.Sprintf("tc-%d", n)
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: id, ToolCallName: "wait"}) {
				return
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: id}) {
				return
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: id, ToolCallName: "wait", ToolCallInput: `{}`}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t", Delta: "done"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *toolThenFinishModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *toolThenFinishModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

type waitToolParams struct{}

// newWaitTool returns a tool that blocks until it is soft-interrupted (or
// a generous safety timeout elapses) and reports which one happened. It
// signals started once it is inside the wait so the test can act while
// the step is genuinely in flight.
func newWaitTool(started chan<- struct{}) fantasy.AgentTool {
	var calls atomic.Int32
	return fantasy.NewAgentTool("wait", "blocks until soft-interrupted",
		func(ctx context.Context, _ waitToolParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if calls.Add(1) > 1 {
				return fantasy.NewTextResponse("fast"), nil
			}
			started <- struct{}{}
			select {
			case <-tools.SoftInterrupt(ctx):
				return fantasy.NewTextResponse("interrupted"), nil
			case <-time.After(5 * time.Second):
				return fantasy.NewTextResponse("timeout"), nil
			case <-ctx.Done():
				return fantasy.ToolResponse{}, ctx.Err()
			}
		})
}

// firstToolResultText digs the text of the first tool result part out of a
// prompt.
func firstToolResultText(t *testing.T, p fantasy.Prompt) string {
	t.Helper()
	for _, msg := range p {
		if msg.Role != fantasy.MessageRoleTool {
			continue
		}
		for _, part := range msg.Content {
			tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				continue
			}
			if txt, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](tr.Output); ok {
				return txt.Text
			}
		}
	}
	t.Fatal("no tool result in prompt")
	return ""
}

func userTexts(p fantasy.Prompt) []string {
	var out []string
	for _, msg := range p {
		if msg.Role != fantasy.MessageRoleUser {
			continue
		}
		for _, part := range msg.Content {
			if tp, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				out = append(out, tp.Text)
			}
		}
	}
	return out
}

func TestRun_SteerSoftInterruptsToolAndFoldsAtNextStep(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	started := make(chan struct{}, 1)
	model := &toolThenFinishModel{}
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system", newWaitTool(started)).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}

	// The session is busy inside a tool: a steer must queue (no turn of
	// its own) and wake the tool early.
	res, err := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "steer", Steer: true})
	require.NoError(t, err)
	require.Nil(t, res, "a steer on a busy session must be queued, not run as its own turn")

	select {
	case err := <-mainDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("main turn did not finish")
	}

	prompts := model.Prompts()
	require.Len(t, prompts, 2, "one step for the tool call, one after the fold")
	require.Equal(t, "interrupted", firstToolResultText(t, prompts[1]),
		"the steer must wake the tool via the soft interrupt, not let it time out")
	require.True(t, containsSteer(prompts[1]),
		"the steer must be folded into the step right after the interrupted tool")

	// Ordering: the steer must come after the tool result, never between
	// the tool_use and its tool_result.
	step2 := prompts[1]
	toolIdx, steerIdx := -1, -1
	for i, msg := range step2 {
		switch msg.Role {
		case fantasy.MessageRoleTool:
			toolIdx = i
		case fantasy.MessageRoleUser:
			for _, part := range msg.Content {
				if tp, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok && strings.HasSuffix(tp.Text, "steer") {
					steerIdx = i
					require.True(t, strings.HasPrefix(tp.Text, steerPreamble), "steer text must be framed for the model")
				}
			}
		}
	}
	require.Greater(t, steerIdx, toolIdx, "steer must follow the tool result")
	require.Equal(t, fantasy.MessageRoleAssistant, step2[toolIdx-1].Role, "tool result must directly follow its tool_use")

	require.Equal(t, 0, sa.QueuedPrompts(sess.ID), "the steer must have been consumed by the fold")
	_, live := sa.softInterrupts.Get(sess.ID)
	require.False(t, live, "soft-interrupt channel must be dropped once the session goes idle")
}

func containsSteer(p fantasy.Prompt) bool {
	for _, txt := range userTexts(p) {
		if strings.HasSuffix(txt, "steer") {
			return true
		}
	}
	return false
}

// steerIndex returns the index of the folded steer message in p, or -1.
func steerIndex(p fantasy.Prompt) int {
	for i, msg := range p {
		if msg.Role != fantasy.MessageRoleUser {
			continue
		}
		for _, part := range msg.Content {
			if tp, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok && strings.HasSuffix(tp.Text, "steer") {
				return i
			}
		}
	}
	return -1
}

// TestRun_FoldedSteerPersistsAcrossLaterSteps guards against the model
// seeing a steer exactly once: fantasy rebuilds every step's input from
// the initial prompt plus its own assistant/tool output, so a message
// appended in one PrepareStep must be re-inserted, at the same offset, in
// every later step of the turn.
func TestRun_FoldedSteerPersistsAcrossLaterSteps(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	started := make(chan struct{}, 1)
	model := &toolThenFinishModel{toolSteps: 2}
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system", newWaitTool(started)).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "steer", Steer: true})
	require.NoError(t, err)
	select {
	case err := <-mainDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("main turn did not finish")
	}

	prompts := model.Prompts()
	require.Len(t, prompts, 3, "tool step, tool step with the fold, final step")
	require.Equal(t, -1, steerIndex(prompts[0]))
	idx1 := steerIndex(prompts[1])
	require.NotEqual(t, -1, idx1, "steer folded into step 2")
	idx2 := steerIndex(prompts[2])
	require.Equal(t, idx1, idx2, "steer must stay at the same offset in step 3")
	require.Len(t, prompts[2], len(prompts[1])+2, "step 3 adds exactly the step-2 assistant and tool messages after the steer")
	require.Equal(t, fantasy.MessageRoleAssistant, prompts[2][idx2+1].Role)
	require.Equal(t, fantasy.MessageRoleTool, prompts[2][idx2+2].Role)
	require.Equal(t, 1, countSteers(prompts[2]), "the steer must not be duplicated")
}

func countSteers(p fantasy.Prompt) int {
	n := 0
	for _, txt := range userTexts(p) {
		if strings.HasSuffix(txt, "steer") {
			n++
		}
	}
	return n
}

func TestRun_SoftInterruptAloneWakesToolWithoutMessage(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	started := make(chan struct{}, 1)
	model := &toolThenFinishModel{}
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system", newWaitTool(started)).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}

	sa.SoftInterrupt(sess.ID)
	// Idempotent within a step: a second call must not double-close.
	sa.SoftInterrupt(sess.ID)

	select {
	case err := <-mainDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("main turn did not finish")
	}

	prompts := model.Prompts()
	require.Len(t, prompts, 2)
	require.Equal(t, "interrupted", firstToolResultText(t, prompts[1]))
	require.False(t, containsSteer(prompts[1]))
	require.Equal(t, len(userTexts(prompts[0])), len(userTexts(prompts[1])),
		"a bare soft interrupt must not inject a user message")
}

func TestSoftInterrupt_IdleSessionIsNoop(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sa := testSessionAgent(env, &finishStreamModel{text: "x"}, &finishStreamModel{text: "title"}, "system").(*sessionAgent)
	require.NotPanics(t, func() { sa.SoftInterrupt("nobody-home") })
	_, ok := sa.softInterrupts.Get("nobody-home")
	require.False(t, ok)
}

func TestRun_SteerOnIdleSessionRunsNormally(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sa := testSessionAgent(env, &finishStreamModel{text: "ok"}, &finishStreamModel{text: "title"}, "system")
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	res, err := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello", Steer: true})
	require.NoError(t, err)
	require.NotNil(t, res, "a steer on an idle session is just a normal turn")
}

// TestRun_JobNoticeFoldsAtNextStepAndNeverStartsATurn covers the aside
// path used for background-job completions: a notice raised while the
// session is busy is folded into the next step; one raised while idle
// waits for the next user turn instead of starting one, and is not
// counted as a queued prompt.
func TestRun_JobNoticeFoldsAtNextStepAndNeverStartsATurn(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	started := make(chan struct{}, 1)
	model := &toolThenFinishModel{}
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system", newWaitTool(started)).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}

	// Busy: the notice is parked, does not count as a prompt, and does
	// not wake the tool.
	sa.notifyJobDone(sess.ID, "[background job finished] busy-notice")
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID), "a system notice is not a user prompt")
	sa.SoftInterrupt(sess.ID)
	select {
	case err := <-mainDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("main turn did not finish")
	}
	prompts := model.Prompts()
	require.Len(t, prompts, 2)
	require.Contains(t, userTexts(prompts[1]), "[background job finished] busy-notice")
	_, pending := sa.pendingAsides.Get(sess.ID)
	require.False(t, pending)

	// Idle: the notice waits; no turn starts.
	sa.notifyJobDone(sess.ID, "[background job finished] idle-notice")
	time.Sleep(200 * time.Millisecond)
	require.False(t, sa.IsSessionBusy(sess.ID), "a notice on an idle session must not start a turn")
	require.Len(t, model.Prompts(), 2)
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID))

	// The next user turn picks it up in its first step.
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-2", Prompt: "next"})
	require.NoError(t, err)
	prompts = model.Prompts()
	require.Len(t, prompts, 3)
	require.Contains(t, userTexts(prompts[2]), "[background job finished] idle-notice")
	_, pending = sa.pendingAsides.Get(sess.ID)
	require.False(t, pending)
}

// TestDeferCall_SteerSoftInterruptsActiveTurn: a swarm btw message that is
// deferred (drain-time, so it carries a RunID and waits for its own turn)
// must still wake the active turn's tools, exactly like a live steer, so
// the turn it is waiting on ends sooner.
func TestDeferCall_SteerSoftInterruptsActiveTurn(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	started := make(chan struct{}, 1)
	model := &toolThenFinishModel{}
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system", newWaitTool(started)).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}

	sa.deferCall(SessionAgentCall{SessionID: sess.ID, RunID: "run-deferred", Prompt: "later", Steer: true}, false)
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID), "a deferred steer waits for its own turn")

	select {
	case err := <-mainDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("main turn did not finish")
	}
	prompts := model.Prompts()
	require.GreaterOrEqual(t, len(prompts), 2)
	require.Equal(t, "interrupted", firstToolResultText(t, prompts[1]), "the deferred steer must wake the running tool")
}

// TestRun_SteerFoldReportsDispatch: a folded steer is reported through the
// queue-dispatch hook like any drained prompt, so reply obligations it
// carries flip from undelivered to delivered.
func TestRun_SteerFoldReportsDispatch(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	started := make(chan struct{}, 1)
	model := &toolThenFinishModel{}
	var dispatched []string
	var dispatchedMu sync.Mutex
	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel:   Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel:   Model{Model: &finishStreamModel{text: "title"}, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SystemPrompt: "system",
		IsYolo:       true,
		Sessions:     env.sessions,
		Messages:     env.messages,
		Tools:        []fantasy.AgentTool{newWaitTool(started)},
		OnQueueDispatch: func(c SessionAgentCall) {
			dispatchedMu.Lock()
			dispatched = append(dispatched, c.Prompt)
			dispatchedMu.Unlock()
		},
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "steer", Steer: true})
	require.NoError(t, err)
	select {
	case err := <-mainDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("main turn did not finish")
	}
	dispatchedMu.Lock()
	defer dispatchedMu.Unlock()
	require.Equal(t, []string{"steer"}, dispatched)
}
