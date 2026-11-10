package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
)

// toolListHash returns a stable hash of the advertised tool list: names,
// descriptions and input schemas in order. Provider options (such as
// cache_control on the last tool) are ignored because Anthropic does not
// include them in the conversation prefix a thinking signature binds to.
func toolListHash(agentTools []fantasy.AgentTool) string {
	hasher := sha256.New()
	for _, tool := range agentTools {
		info := tool.Info()
		data, err := json.Marshal(struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
			Required    []string       `json:"required"`
		}{info.Name, info.Description, info.Parameters, info.Required})
		if err != nil {
			data = []byte(info.Name)
		}
		hasher.Write(data)
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// isAnthropicSignedReasoning reports whether the part is an Anthropic (or
// Bedrock, which shares the anthropic metadata) thinking block whose
// signature is bound to the conversation prefix.
func isAnthropicSignedReasoning(part fantasy.MessagePart) bool {
	reasoning, ok := part.(fantasy.ReasoningPart)
	if !ok {
		return false
	}
	_, ok = reasoning.ProviderOptions[anthropic.Name]
	return ok
}

// stripAnthropicThinking removes Anthropic signed thinking blocks from the
// first limit messages of history. Text, tool calls, and other providers'
// reasoning (Google thought signatures, OpenAI Responses data) are kept.
// The input slice and its messages are not mutated.
func stripAnthropicThinking(history []fantasy.Message, limit int) []fantasy.Message {
	limit = min(limit, len(history))
	rewritten := make([]fantasy.Message, 0, len(history))
	for i, msg := range history {
		if i >= limit || msg.Role != fantasy.MessageRoleAssistant {
			rewritten = append(rewritten, msg)
			continue
		}
		parts := make([]fantasy.MessagePart, 0, len(msg.Content))
		for _, part := range msg.Content {
			if isAnthropicSignedReasoning(part) {
				continue
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

// thinkingBinding tracks, per session, the tool list hash that thinking
// blocks were produced under. When the hash changes, every message replayed
// up to that point is treated as bound to a stale prefix and has its
// Anthropic thinking stripped from outbound requests. Messages appended
// after the change were produced under the new prefix (which already
// excludes the stripped blocks), so they keep their thinking.
type thinkingBinding struct {
	mu       sync.Mutex
	sessions map[string]thinkingBindingState
}

type thinkingBindingState struct {
	toolHash string
	// staleCount is the number of leading history messages whose
	// Anthropic thinking must be stripped.
	staleCount int
}

// apply records toolHash for the session and returns history with stale
// thinking stripped. It does not mutate history.
func (b *thinkingBinding) apply(sessionID, toolHash string, history []fantasy.Message) []fantasy.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessions == nil {
		b.sessions = make(map[string]thinkingBindingState)
	}
	state, seen := b.sessions[sessionID]
	if state.staleCount > len(history) {
		// History was replaced (e.g. summarized); the stale messages are
		// gone.
		state.staleCount = 0
	}
	if seen && state.toolHash != toolHash {
		state.staleCount = len(history)
	}
	state.toolHash = toolHash
	b.sessions[sessionID] = state
	if state.staleCount == 0 {
		return history
	}
	return stripAnthropicThinking(history, state.staleCount)
}
