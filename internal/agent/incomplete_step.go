package agent

import (
	"fmt"
	"strings"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/message"
)

// incompleteStep records a model step whose streamed content arrived
// truncated: at least one tool_use block was announced (tool-input-start)
// but never completed (no tool-call), so fantasy's in-memory assistant
// message for that step is missing a block the model actually produced.
// Sending that message back is fatal on providers that validate the
// latest assistant turn against a signature (Anthropic extended thinking
// rejects it with "thinking blocks ... cannot be modified"), and silently
// lossy elsewhere. See patchIncompleteSteps for the recovery.
type incompleteStep struct {
	// completed are the tool calls that did arrive in full.
	completed []message.ToolCall
	// unfinished are the tool calls that never completed.
	unfinished []message.ToolCall
	// text is the assistant prose from the step, if any.
	text string
}

// unfinishedToolCalls splits an assistant message's tool calls into those
// that finished streaming and those that did not.
func unfinishedToolCalls(m *message.Message) (completed, unfinished []message.ToolCall) {
	for _, tc := range m.ToolCalls() {
		if tc.Finished {
			completed = append(completed, tc)
		} else {
			unfinished = append(unfinished, tc)
		}
	}
	return completed, unfinished
}

// patchIncompleteSteps rewrites the request history so a truncated step is
// not replayed as an assistant turn. The broken assistant message and the
// tool message that follows it are replaced by one user message that
// carries the same information as plain text: what the model said, which
// calls completed (with their results), and which calls were lost in
// transit. The model can then re-issue the lost call and carry on, and the
// provider never sees the corrupted turn. Messages that do not match a
// recorded step are returned unchanged.
func patchIncompleteSteps(msgs []fantasy.Message, steps []incompleteStep) []fantasy.Message {
	if len(steps) == 0 {
		return msgs
	}
	out := make([]fantasy.Message, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		step, ok := matchIncompleteStep(m, steps)
		if !ok {
			out = append(out, m)
			continue
		}
		results := map[string]string{}
		if i+1 < len(msgs) && msgs[i+1].Role == fantasy.MessageRoleTool {
			for _, part := range msgs[i+1].Content {
				tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
				if !ok {
					continue
				}
				results[tr.ToolCallID] = toolResultText(tr)
			}
			i++
		}
		out = append(out, fantasy.Message{
			Role:    fantasy.MessageRoleUser,
			Content: []fantasy.MessagePart{fantasy.TextPart{Text: incompleteStepNote(step, results)}},
		})
	}
	return out
}

// matchIncompleteStep finds the recorded step, if any, that m is the
// assistant message of. A step with completed calls is matched by tool
// call id; a step whose every call was lost has no tool calls in m, so it
// is matched by the assistant text instead.
func matchIncompleteStep(m fantasy.Message, steps []incompleteStep) (incompleteStep, bool) {
	if m.Role != fantasy.MessageRoleAssistant {
		return incompleteStep{}, false
	}
	ids := map[string]bool{}
	var text strings.Builder
	for _, part := range m.Content {
		if tc, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
			ids[tc.ToolCallID] = true
		}
		if tp, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
			text.WriteString(tp.Text)
		}
	}
	for _, step := range steps {
		if len(step.completed) > 0 {
			if ids[step.completed[0].ID] {
				return step, true
			}
			continue
		}
		if len(ids) == 0 && strings.TrimSpace(text.String()) == strings.TrimSpace(step.text) {
			return step, true
		}
	}
	return incompleteStep{}, false
}

func toolResultText(tr fantasy.ToolResultPart) string {
	switch out := tr.Output.(type) {
	case fantasy.ToolResultOutputContentText:
		return out.Text
	case fantasy.ToolResultOutputContentError:
		if out.Error != nil {
			return "error: " + out.Error.Error()
		}
		return "error"
	case fantasy.ToolResultOutputContentMedia:
		return strings.TrimSpace(out.Text + " [media omitted]")
	}
	return ""
}

// incompleteStepNote renders the replacement user message.
func incompleteStepNote(step incompleteStep, results map[string]string) string {
	var b strings.Builder
	b.WriteString("[system] Your previous response was cut off in transit: ")
	names := make([]string, 0, len(step.unfinished))
	for _, tc := range step.unfinished {
		names = append(names, tc.Name)
	}
	fmt.Fprintf(&b, "%d tool call(s) never arrived (%s). ", len(step.unfinished), strings.Join(names, ", "))
	b.WriteString("Nothing from that response was executed except the calls listed below; re-issue the lost call(s) if you still need them and continue.\n")
	if text := strings.TrimSpace(step.text); text != "" {
		b.WriteString("\nYou had said:\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	for _, tc := range step.completed {
		fmt.Fprintf(&b, "\nCompleted call %s %s\nResult:\n%s\n", tc.Name, tc.Input, truncateForTranscript(results[tc.ID]))
	}
	return b.String()
}
