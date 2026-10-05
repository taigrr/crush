package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
)

func TestSplitToolCallsByFinished(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		parts          []message.ContentPart
		wantFinished   []string
		wantUnfinished []string
	}{
		{
			name: "no tool calls",
			parts: []message.ContentPart{
				message.TextContent{Text: "hello"},
			},
		},
		{
			name: "all finished",
			parts: []message.ContentPart{
				message.ToolCall{ID: "a", Name: "view", Input: `{}`, Finished: true},
				message.ToolCall{ID: "b", Name: "grep", Input: `{}`, Finished: true},
			},
			wantFinished: []string{"a", "b"},
		},
		{
			name: "mixed preserves order",
			parts: []message.ContentPart{
				message.TextContent{Text: "working"},
				message.ToolCall{ID: "a", Name: "view", Input: `{}`, Finished: true},
				message.ToolCall{ID: "b", Name: "grep", Finished: false},
				message.ToolCall{ID: "c", Name: "edit", Input: `{"x":1}`, Finished: true},
			},
			wantFinished:   []string{"a", "c"},
			wantUnfinished: []string{"b"},
		},
		{
			name: "all unfinished",
			parts: []message.ContentPart{
				message.ToolCall{ID: "a", Name: "view", Input: `{"pa`, Finished: false},
			},
			wantUnfinished: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := message.Message{Role: message.Assistant, Parts: tt.parts}
			finished, unfinished := splitToolCallsByFinished(msg)
			require.Equal(t, tt.wantFinished, toolCallIDs(finished))
			require.Equal(t, tt.wantUnfinished, toolCallIDs(unfinished))
		})
	}
}

func toolCallIDs(calls []message.ToolCall) []string {
	var ids []string
	for _, call := range calls {
		ids = append(ids, call.ID)
	}
	return ids
}

func TestDropToolCallsFromHistory(t *testing.T) {
	t.Parallel()

	ids := func(values ...string) map[string]struct{} {
		set := make(map[string]struct{}, len(values))
		for _, value := range values {
			set[value] = struct{}{}
		}
		return set
	}
	toolCall := func(id string) fantasy.ToolCallPart {
		return fantasy.ToolCallPart{ToolCallID: id, ToolName: "view", Input: `{}`}
	}
	toolResult := func(id string) fantasy.ToolResultPart {
		return fantasy.ToolResultPart{ToolCallID: id, Output: fantasy.ToolResultOutputContentText{Text: "ok"}}
	}

	tests := []struct {
		name    string
		history []fantasy.Message
		drop    map[string]struct{}
		want    []fantasy.Message
	}{
		{
			name: "nothing to drop returns input unchanged",
			history: []fantasy.Message{
				fantasy.NewUserMessage("hi"),
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{toolCall("a")}},
			},
			drop: nil,
			want: []fantasy.Message{
				fantasy.NewUserMessage("hi"),
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{toolCall("a")}},
			},
		},
		{
			name: "drops unfinished tool_use and keeps text and finished calls",
			history: []fantasy.Message{
				fantasy.NewUserMessage("hi"),
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
					fantasy.TextPart{Text: "let me look"},
					toolCall("good"),
					toolCall("bad"),
				}},
				{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
					toolResult("good"),
					toolResult("bad"),
				}},
			},
			drop: ids("bad"),
			want: []fantasy.Message{
				fantasy.NewUserMessage("hi"),
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
					fantasy.TextPart{Text: "let me look"},
					toolCall("good"),
				}},
				{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
					toolResult("good"),
				}},
			},
		},
		{
			name: "removes messages that become empty",
			history: []fantasy.Message{
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{toolCall("bad")}},
				{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{ToolCallID: "bad", Output: fantasy.ToolResultOutputContentError{Error: errors.New("boom")}},
				}},
				fantasy.NewUserMessage("next"),
			},
			drop: ids("bad"),
			want: []fantasy.Message{
				fantasy.NewUserMessage("next"),
			},
		},
		{
			name: "leaves non assistant and tool roles alone",
			history: []fantasy.Message{
				fantasy.NewSystemMessage("sys"),
				fantasy.NewUserMessage("hi"),
			},
			drop: ids("bad"),
			want: []fantasy.Message{
				fantasy.NewSystemMessage("sys"),
				fantasy.NewUserMessage("hi"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			original := cloneFantasyMessages(tt.history)
			got := dropToolCallsFromHistory(tt.history, tt.drop)
			require.Equal(t, tt.want, got)
			require.Equal(t, original, tt.history, "input must not be mutated")
		})
	}
}
