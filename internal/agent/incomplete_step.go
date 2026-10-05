package agent

import (
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
)

// splitToolCallsByFinished partitions the assistant message's tool calls
// into those whose full input arrived (finished) and those that only ever
// saw a tool-input-start before the stream ended (unfinished). An
// unfinished call has partial or empty input and is not valid to replay to
// any provider.
func splitToolCallsByFinished(msg message.Message) (finished []message.ToolCall, unfinished []message.ToolCall) {
	for _, toolCall := range msg.ToolCalls() {
		if toolCall.Finished {
			finished = append(finished, toolCall)
			continue
		}
		unfinished = append(unfinished, toolCall)
	}
	return finished, unfinished
}

// dropToolCallsFromHistory returns a copy of history with every tool_use
// part whose ID is in dropIDs removed, along with any tool_result part
// that pairs with it, so no dangling pairing survives. Messages left with
// no content are dropped entirely. The input slice and its parts are not
// mutated. When dropIDs is empty the input is returned as-is.
func dropToolCallsFromHistory(history []fantasy.Message, dropIDs map[string]struct{}) []fantasy.Message {
	if len(dropIDs) == 0 {
		return history
	}
	rewritten := make([]fantasy.Message, 0, len(history))
	for _, msg := range history {
		switch msg.Role {
		case fantasy.MessageRoleAssistant, fantasy.MessageRoleTool:
		default:
			rewritten = append(rewritten, msg)
			continue
		}
		parts := make([]fantasy.MessagePart, 0, len(msg.Content))
		for _, part := range msg.Content {
			switch typed := part.(type) {
			case fantasy.ToolCallPart:
				if _, drop := dropIDs[typed.ToolCallID]; drop {
					continue
				}
			case fantasy.ToolResultPart:
				if _, drop := dropIDs[typed.ToolCallID]; drop {
					continue
				}
			}
			parts = append(parts, part)
		}
		if len(parts) == 0 {
			continue
		}
		msg.Content = parts
		rewritten = append(rewritten, msg)
	}
	return rewritten
}
