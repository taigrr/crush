package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
	"github.com/taigrr/fantasy/providers/google"
	"github.com/taigrr/fantasy/providers/openai"
)

// steerTranscript persists the shape a mid-turn steer leaves in the DB —
// assistant tool_use, the folded user aside (created while the tool ran,
// so it sorts before the result), the tool result, a final assistant
// message — and returns the reloaded history as preparePrompt would hand
// it to the next turn.
func steerTranscript(t *testing.T) fantasy.Prompt {
	t.Helper()
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)
	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "steer")
	require.NoError(t, err)

	create := func(role message.MessageRole, parts ...message.ContentPart) {
		_, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{Role: role, Parts: parts})
		require.NoError(t, err)
	}
	create(message.User, message.TextContent{Text: "run the tests"})
	create(message.Assistant, message.ToolCall{ID: "call_a", Name: "bash", Input: `{"command":"go test ./..."}`, Finished: true})
	create(message.User, message.TextContent{Text: "[btw] only the agent package"})
	create(message.Tool, message.ToolResult{ToolCallID: "call_a", Name: "bash", Content: "moved to background"})
	create(message.Assistant, message.TextContent{Text: "Backgrounded; narrowing to ./internal/agent."})

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	history, _ := agent.preparePrompt(msgs, true)
	return history
}

// liveStepPrompt is the same conversation as it is fed to the model
// mid-turn: fantasy's step input (user, assistant tool_use, tool result)
// with the steer spliced in by insertFoldedAsides after the tool result.
func liveStepPrompt() fantasy.Prompt {
	base := []fantasy.Message{
		fantasy.NewUserMessage("run the tests"),
		{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
			fantasy.ToolCallPart{ToolCallID: "call_a", ToolName: "bash", Input: `{"command":"go test ./..."}`},
		}},
		{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{ToolCallID: "call_a", Output: fantasy.ToolResultOutputContentText{Text: "moved to background"}},
		}},
	}
	return insertFoldedAsides(base, []foldedAside{{
		at:       3,
		messages: wrapSteer([]fantasy.Message{fantasy.NewUserMessage("[btw] only the agent package")}),
	}})
}

// requireToolResultsAdjacent asserts the provider-neutral invariant every
// vendor enforces: a Tool message carrying results for every tool call of
// an assistant message must come immediately after that assistant message.
func requireToolResultsAdjacent(t *testing.T, p fantasy.Prompt) {
	t.Helper()
	for i, msg := range p {
		if msg.Role != fantasy.MessageRoleAssistant {
			continue
		}
		var callIDs []string
		for _, part := range msg.Content {
			if tc, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
				callIDs = append(callIDs, tc.ToolCallID)
			}
		}
		if len(callIDs) == 0 {
			continue
		}
		require.Less(t, i+1, len(p), "tool_use at %d has nothing after it", i)
		next := p[i+1]
		require.Equal(t, fantasy.MessageRoleTool, next.Role, "message after tool_use at %d must be its tool results, got %s", i, next.Role)
		got := map[string]bool{}
		for _, part := range next.Content {
			if tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				got[tr.ToolCallID] = true
			}
		}
		for _, id := range callIDs {
			require.True(t, got[id], "tool_use %s has no adjacent result", id)
		}
	}
}

// captureServer records the JSON body of the first request it receives
// and answers with an empty 200 so the SDK call returns (with a decode
// error we ignore) instead of retrying.
func captureServer(t *testing.T) (*httptest.Server, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		if body == nil {
			var parsed map[string]any
			if err := json.Unmarshal(raw, &parsed); err == nil {
				body = parsed
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		require.NotNil(t, body, "provider never sent a request")
		return body
	}
}

func asList(t *testing.T, v any) []map[string]any {
	t.Helper()
	raw, ok := v.([]any)
	require.True(t, ok, "expected a list, got %T", v)
	out := make([]map[string]any, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		require.True(t, ok, "expected an object, got %T", item)
		out[i] = m
	}
	return out
}

func TestSteerOrdering_ProviderWireFormats(t *testing.T) {
	t.Parallel()

	prompts := map[string]fantasy.Prompt{
		"reloaded": steerTranscript(t),
		"live":     liveStepPrompt(),
	}
	for name, p := range prompts {
		requireToolResultsAdjacent(t, p)
		steerSeen := false
		for _, txt := range userTexts(p) {
			if strings.HasSuffix(txt, "only the agent package") {
				steerSeen = true
			}
		}
		require.True(t, steerSeen, "%s prompt must still carry the steer", name)
	}

	for name, p := range prompts {
		t.Run("anthropic/"+name, func(t *testing.T) {
			t.Parallel()
			srv, body := captureServer(t)
			provider, err := anthropic.New(anthropic.WithBaseURL(srv.URL), anthropic.WithAPIKey("test"))
			require.NoError(t, err)
			model, err := provider.LanguageModel(t.Context(), "claude-test")
			require.NoError(t, err)
			_, _ = model.Generate(t.Context(), fantasy.Call{Prompt: p})

			msgs := asList(t, body()["messages"])
			require.NotEmpty(t, msgs)
			for i, m := range msgs {
				if i > 0 {
					require.NotEqual(t, msgs[i-1]["role"], m["role"], "anthropic roles must alternate (index %d)", i)
				}
				if m["role"] != "assistant" {
					continue
				}
				var useIDs []string
				for _, block := range asList(t, m["content"]) {
					if block["type"] == "tool_use" {
						useIDs = append(useIDs, block["id"].(string))
					}
				}
				if len(useIDs) == 0 {
					continue
				}
				require.Less(t, i+1, len(msgs))
				next := msgs[i+1]
				require.Equal(t, "user", next["role"])
				blocks := asList(t, next["content"])
				// tool_result blocks first, then the steer text.
				seenText := false
				results := map[string]bool{}
				for _, block := range blocks {
					switch block["type"] {
					case "tool_result":
						require.False(t, seenText, "tool_result must precede text in the user block")
						results[block["tool_use_id"].(string)] = true
					case "text":
						seenText = true
					}
				}
				for _, id := range useIDs {
					require.True(t, results[id], "tool_use %s lacks adjacent tool_result", id)
				}
				require.True(t, seenText, "the steer text must ride in the same user block as the tool results")
			}
		})

		t.Run("openai/"+name, func(t *testing.T) {
			t.Parallel()
			srv, body := captureServer(t)
			provider, err := openai.New(openai.WithBaseURL(srv.URL), openai.WithAPIKey("test"))
			require.NoError(t, err)
			model, err := provider.LanguageModel(t.Context(), "gpt-test")
			require.NoError(t, err)
			_, _ = model.Generate(t.Context(), fantasy.Call{Prompt: p})

			msgs := asList(t, body()["messages"])
			require.NotEmpty(t, msgs)
			for i, m := range msgs {
				if m["role"] != "assistant" || m["tool_calls"] == nil {
					continue
				}
				pending := map[string]bool{}
				for _, tc := range asList(t, m["tool_calls"]) {
					pending[tc["id"].(string)] = true
				}
				j := i + 1
				for ; j < len(msgs) && msgs[j]["role"] == "tool"; j++ {
					delete(pending, msgs[j]["tool_call_id"].(string))
				}
				require.Empty(t, pending, "every tool_call must be answered by a tool message before any other role")
				require.Less(t, j, len(msgs), "the steer must follow the tool messages")
				require.Equal(t, "user", msgs[j]["role"], "the steer follows the tool messages as a user turn")
			}
		})

		t.Run("google/"+name, func(t *testing.T) {
			t.Parallel()
			srv, body := captureServer(t)
			provider, err := google.New(google.WithBaseURL(srv.URL), google.WithGeminiAPIKey("test"))
			require.NoError(t, err)
			model, err := provider.LanguageModel(t.Context(), "gemini-test")
			require.NoError(t, err)
			_, _ = model.Generate(t.Context(), fantasy.Call{Prompt: p})

			contents := asList(t, body()["contents"])
			require.NotEmpty(t, contents)
			for i, c := range contents {
				if c["role"] != "model" {
					continue
				}
				calls := 0
				for _, part := range asList(t, c["parts"]) {
					if part["functionCall"] != nil {
						calls++
					}
				}
				if calls == 0 {
					continue
				}
				require.Less(t, i+1, len(contents))
				next := contents[i+1]
				require.Equal(t, "user", next["role"])
				responses := 0
				for _, part := range asList(t, next["parts"]) {
					require.NotNil(t, part["functionResponse"], "the turn after a functionCall must contain only functionResponse parts")
					responses++
				}
				require.Equal(t, calls, responses, "every functionCall needs an adjacent functionResponse")
				// Gemini accepts consecutive user turns; the steer must be a
				// later user turn, never spliced into the response turn or
				// placed before it.
				steerAfter := false
				for _, c := range contents[i+2:] {
					if c["role"] != "user" {
						continue
					}
					for _, part := range asList(t, c["parts"]) {
						if txt, ok := part["text"].(string); ok && strings.HasSuffix(txt, "only the agent package") {
							steerAfter = true
						}
					}
				}
				require.True(t, steerAfter, "the steer must follow the functionResponse turn")
			}
		})
	}
}
