package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/message"
)

func TestUnfinishedToolCalls(t *testing.T) {
	t.Parallel()
	m := &message.Message{Role: message.Assistant}
	m.AddToolCall(message.ToolCall{ID: "a", Name: "bash", Finished: false})
	m.AddToolCall(message.ToolCall{ID: "b", Name: "bash", Input: `{"command":"ls"}`, Finished: true})
	completed, unfinished := unfinishedToolCalls(m)
	require.Len(t, completed, 1)
	require.Equal(t, "b", completed[0].ID)
	require.Len(t, unfinished, 1)
	require.Equal(t, "a", unfinished[0].ID)
}

func assistantWith(text string, calls ...fantasy.ToolCallPart) fantasy.Message {
	var parts []fantasy.MessagePart
	parts = append(parts, fantasy.ReasoningPart{Text: "", ProviderOptions: fantasy.ProviderOptions{}})
	if text != "" {
		parts = append(parts, fantasy.TextPart{Text: text})
	}
	for _, c := range calls {
		parts = append(parts, c)
	}
	return fantasy.Message{Role: fantasy.MessageRoleAssistant, Content: parts}
}

func toolMsg(results ...fantasy.ToolResultPart) fantasy.Message {
	var parts []fantasy.MessagePart
	for _, r := range results {
		parts = append(parts, r)
	}
	return fantasy.Message{Role: fantasy.MessageRoleTool, Content: parts}
}

// The broken assistant turn and its tool results collapse into one user
// message that preserves the completed results and names the lost call;
// everything around it is untouched.
func TestPatchIncompleteSteps_RewritesBrokenStep(t *testing.T) {
	t.Parallel()
	user := fantasy.Message{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "do it"}}}
	good := assistantWith("first", fantasy.ToolCallPart{ToolCallID: "ok1", ToolName: "bash", Input: `{"command":"echo 1"}`})
	goodRes := toolMsg(fantasy.ToolResultPart{ToolCallID: "ok1", Output: fantasy.ToolResultOutputContentText{Text: "1"}})
	broken := assistantWith("Now #5.", fantasy.ToolCallPart{ToolCallID: "done", ToolName: "bash", Input: `{"command":"grep x"}`})
	brokenRes := toolMsg(fantasy.ToolResultPart{ToolCallID: "done", Output: fantasy.ToolResultOutputContentText{Text: "match:1"}})

	steps := []incompleteStep{{
		completed:  []message.ToolCall{{ID: "done", Name: "bash", Input: `{"command":"grep x"}`, Finished: true}},
		unfinished: []message.ToolCall{{ID: "lost", Name: "bash"}},
		text:       "Now #5.",
	}}
	out := patchIncompleteSteps([]fantasy.Message{user, good, goodRes, broken, brokenRes}, steps)

	require.Len(t, out, 4)
	require.Equal(t, user, out[0])
	require.Equal(t, good, out[1])
	require.Equal(t, goodRes, out[2])
	require.Equal(t, fantasy.MessageRoleUser, out[3].Role)
	tp, ok := fantasy.AsMessagePart[fantasy.TextPart](out[3].Content[0])
	require.True(t, ok)
	require.Contains(t, tp.Text, "cut off in transit")
	require.Contains(t, tp.Text, "1 tool call(s) never arrived (bash)")
	require.Contains(t, tp.Text, "Now #5.")
	require.Contains(t, tp.Text, `grep x`)
	require.Contains(t, tp.Text, "match:1")
}

// A step where every tool call was lost has no ToolCallPart to match on, so
// it is matched by its text; an error result is rendered as such.
func TestPatchIncompleteSteps_AllCallsLostAndErrorResult(t *testing.T) {
	t.Parallel()
	broken := assistantWith("Let me check.")
	steps := []incompleteStep{{unfinished: []message.ToolCall{{ID: "lost", Name: "view"}}, text: "Let me check."}}
	out := patchIncompleteSteps([]fantasy.Message{broken}, steps)
	require.Len(t, out, 1)
	require.Equal(t, fantasy.MessageRoleUser, out[0].Role)

	withErr := assistantWith("", fantasy.ToolCallPart{ToolCallID: "c", ToolName: "bash", Input: "{}"})
	res := toolMsg(fantasy.ToolResultPart{ToolCallID: "c", Output: fantasy.ToolResultOutputContentError{Error: errors.New("boom")}})
	steps = []incompleteStep{{completed: []message.ToolCall{{ID: "c", Name: "bash", Input: "{}", Finished: true}}, unfinished: []message.ToolCall{{ID: "l", Name: "edit"}}}}
	out = patchIncompleteSteps([]fantasy.Message{withErr, res}, steps)
	require.Len(t, out, 1)
	tp, _ := fantasy.AsMessagePart[fantasy.TextPart](out[0].Content[0])
	require.Contains(t, tp.Text, "error: boom")
}

func TestPatchIncompleteSteps_NoStepsIsIdentity(t *testing.T) {
	t.Parallel()
	msgs := []fantasy.Message{assistantWith("x", fantasy.ToolCallPart{ToolCallID: "a", ToolName: "bash", Input: "{}"})}
	require.Equal(t, msgs, patchIncompleteSteps(msgs, nil))
	// A non-matching step leaves the history alone too.
	require.Equal(t, msgs, patchIncompleteSteps(msgs, []incompleteStep{{completed: []message.ToolCall{{ID: "zzz"}}, unfinished: []message.ToolCall{{ID: "q"}}}}))
}
