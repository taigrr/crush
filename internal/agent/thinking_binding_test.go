package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
	"github.com/taigrr/fantasy/providers/google"
	"github.com/taigrr/fantasy/providers/openai"
)

type bindingToolInput struct {
	Path string `json:"path"`
}

type bindingToolInputAlt struct {
	Pattern string `json:"pattern"`
}

func bindingTool(name string) fantasy.AgentTool {
	return fantasy.NewAgentTool(name, "desc "+name, func(context.Context, bindingToolInput, fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.ToolResponse{}, nil
	})
}

func bindingTools(names ...string) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, 0, len(names))
	for _, name := range names {
		out = append(out, bindingTool(name))
	}
	return out
}

func anthropicReasoning(text string) fantasy.ReasoningPart {
	return fantasy.ReasoningPart{Text: text, ProviderOptions: fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ReasoningOptionMetadata{Signature: "sig-" + text},
	}}
}

func bindingHistory() []fantasy.Message {
	return []fantasy.Message{
		fantasy.NewSystemMessage("system"),
		fantasy.NewUserMessage("hi"),
		{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: "answer"},
			anthropicReasoning("think"),
			fantasy.ToolCallPart{ToolCallID: "call-1", ToolName: "view", Input: "{}"},
		}},
		{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{ToolCallID: "call-1", Output: fantasy.ToolResultOutputContentText{Text: "ok"}},
		}},
		{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
			fantasy.ReasoningPart{Text: "google", ProviderOptions: fantasy.ProviderOptions{
				google.Name: &google.ReasoningMetadata{Signature: "gsig"},
			}},
			fantasy.ReasoningPart{Text: "openai", ProviderOptions: fantasy.ProviderOptions{
				openai.Name: &openai.ResponsesReasoningMetadata{ItemID: "rs_1"},
			}},
			fantasy.TextPart{Text: "done"},
		}},
	}
}

func countAnthropicReasoning(history []fantasy.Message) int {
	count := 0
	for _, msg := range history {
		for _, part := range msg.Content {
			if isAnthropicSignedReasoning(part) {
				count++
			}
		}
	}
	return count
}

func countParts[T fantasy.MessagePart](history []fantasy.Message) int {
	count := 0
	for _, msg := range history {
		for _, part := range msg.Content {
			if _, ok := part.(T); ok {
				count++
			}
		}
	}
	return count
}

func TestToolListHash(t *testing.T) {
	t.Parallel()
	base := toolListHash(bindingTools("view", "edit"))
	require.Equal(t, base, toolListHash(bindingTools("view", "edit")))
	require.NotEqual(t, base, toolListHash(bindingTools("view", "edit", "bash")), "added tool")
	require.NotEqual(t, base, toolListHash(bindingTools("view")), "removed tool")
	require.NotEqual(t, base, toolListHash(bindingTools("view", "write")), "renamed tool")

	alt := []fantasy.AgentTool{bindingTool("view"), fantasy.NewAgentTool("edit", "desc edit", func(context.Context, bindingToolInputAlt, fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.ToolResponse{}, nil
	})}
	require.NotEqual(t, base, toolListHash(alt), "schema changed")

	cached := bindingTools("view", "edit")
	cached[len(cached)-1].SetProviderOptions(fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{CacheControl: anthropic.CacheControl{Type: "ephemeral"}},
	})
	require.Equal(t, base, toolListHash(cached), "cache_control must not affect the hash")
}

func TestThinkingBindingToolsUnchanged(t *testing.T) {
	t.Parallel()
	var binding thinkingBinding
	hash := toolListHash(bindingTools("view", "edit"))
	history := bindingHistory()

	require.Equal(t, history, binding.apply("s", hash, history))
	got := binding.apply("s", hash, history)
	require.Equal(t, 1, countAnthropicReasoning(got))
}

func TestThinkingBindingToolsChanged(t *testing.T) {
	t.Parallel()
	for name, next := range map[string][]string{
		"added":    {"view", "edit", "bash"},
		"removed":  {"view"},
		"renamed":  {"view", "write"},
		"filtered": {"edit"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var binding thinkingBinding
			history := bindingHistory()
			binding.apply("s", toolListHash(bindingTools("view", "edit")), history)

			got := binding.apply("s", toolListHash(bindingTools(next...)), history)
			require.Zero(t, countAnthropicReasoning(got))
			require.Equal(t, 4, countParts[fantasy.TextPart](got))
			require.Equal(t, 1, countParts[fantasy.ToolCallPart](got))
			require.Equal(t, 1, countParts[fantasy.ToolResultPart](got))
			require.Equal(t, 2, countParts[fantasy.ReasoningPart](got), "google and openai reasoning stay")
			require.Equal(t, 1, countAnthropicReasoning(history), "input must not be mutated")

			// Thinking produced after the change is kept on later steps,
			// while the stale prefix stays stripped.
			appended := append(bindingHistory(), fantasy.Message{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{anthropicReasoning("new")},
			})
			later := binding.apply("s", toolListHash(bindingTools(next...)), appended)
			require.Equal(t, 1, countAnthropicReasoning(later))
		})
	}
}

func TestThinkingBindingPerSession(t *testing.T) {
	t.Parallel()
	var binding thinkingBinding
	history := bindingHistory()
	binding.apply("a", toolListHash(bindingTools("view")), history)
	got := binding.apply("b", toolListHash(bindingTools("edit")), history)
	require.Equal(t, 1, countAnthropicReasoning(got))
}
