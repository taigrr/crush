package agent

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/fantasy"

	_ "github.com/joho/godotenv/autoload"
)

func TestMain(m *testing.M) {
	slog.SetLogLoggerLevel(slog.LevelError)
	m.Run()
}

func makeTestTodos(n int) []session.Todo {
	todos := make([]session.Todo, n)
	for i := range n {
		todos[i] = session.Todo{
			Status:  session.TodoStatusPending,
			Content: fmt.Sprintf("Task %d: Implement feature with some description that makes it realistic", i),
		}
	}
	return todos
}

func BenchmarkBuildSummaryPrompt(b *testing.B) {
	cases := []struct {
		name     string
		numTodos int
	}{
		{"0todos", 0},
		{"5todos", 5},
		{"10todos", 10},
		{"50todos", 50},
	}

	for _, tc := range cases {
		todos := makeTestTodos(tc.numTodos)

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = buildSummaryPrompt(todos, nil)
			}
		})
	}
}

func TestPreparePrompt_FiltersImageAttachments(t *testing.T) {
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt")
	agent := sa.(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	// User message with text, a text attachment, and an image attachment.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello world"},
			message.BinaryContent{Path: "notes.txt", MIMEType: "text/plain", Data: []byte("important notes")},
			message.BinaryContent{Path: "image.png", MIMEType: "image/png", Data: []byte("fake-image-data")},
		},
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	// When supportsImages is false, image attachments should be stripped.
	history, _ := agent.preparePrompt(msgs, false)
	// First message is the system reminder, second is the user message.
	require.Len(t, history, 2)
	require.Len(t, history[1].Content, 1)
	text, ok := fantasy.AsMessagePart[fantasy.TextPart](history[1].Content[0])
	require.True(t, ok)
	require.Contains(t, text.Text, "hello world")
	require.Contains(t, text.Text, "important notes")

	// When supportsImages is true, image attachments should remain.
	history, _ = agent.preparePrompt(msgs, true)
	require.Len(t, history, 2)
	require.Len(t, history[1].Content, 2)
	text, ok = fantasy.AsMessagePart[fantasy.TextPart](history[1].Content[0])
	require.True(t, ok)
	require.Contains(t, text.Text, "hello world")
	file, ok := fantasy.AsMessagePart[fantasy.FilePart](history[1].Content[1])
	require.True(t, ok)
	require.Equal(t, "image.png", file.Filename)
}

func TestPreparePrompt_OrphanedToolUse(t *testing.T) {
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt")
	agent := sa.(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	// Create a user message.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
		},
	})
	require.NoError(t, err)

	// Create an assistant message with a tool call but no tool result —
	// this simulates a cancelled/interrupted agent tool call.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "let me check"},
			message.ToolCall{
				ID:       "call_orphaned_1",
				Name:     "agent",
				Input:    `{"prompt":"do something"}`,
				Finished: true,
			},
		},
	})
	require.NoError(t, err)

	// Create the next user message (the one that interrupted the tool call).
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Fix #2"},
		},
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	history, _ := agent.preparePrompt(msgs, true)

	// The history must contain a synthetic tool result for the orphaned call.
	found := false
	for _, msg := range history {
		if msg.Role != fantasy.MessageRoleTool {
			continue
		}
		for _, part := range msg.Content {
			if tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				if tr.ToolCallID == "call_orphaned_1" {
					found = true
					_, isError := tr.Output.(fantasy.ToolResultOutputContentError)
					require.True(t, isError, "orphaned tool result should be an error")
				}
			}
		}
	}
	require.True(t, found, "expected synthetic tool result for orphaned tool call")
}

func TestPreparePrompt_OrphanedToolUseMixed(t *testing.T) {
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt")
	agent := sa.(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
		},
	})
	require.NoError(t, err)

	// Assistant with 2 tool calls: one has a result, one is orphaned.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{
				ID:       "call_ok",
				Name:     "view",
				Input:    `{"path":"/foo"}`,
				Finished: true,
			},
			message.ToolCall{
				ID:       "call_orphaned",
				Name:     "agent",
				Input:    `{"prompt":"search"}`,
				Finished: true,
			},
		},
	})
	require.NoError(t, err)

	// Only one tool result — for call_ok.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{
				ToolCallID: "call_ok",
				Name:       "view",
				Content:    "file contents",
			},
		},
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	history, _ := agent.preparePrompt(msgs, true)

	// Should have a synthetic result only for the orphaned call.
	var syntheticCount int
	for _, msg := range history {
		if msg.Role != fantasy.MessageRoleTool {
			continue
		}
		for _, part := range msg.Content {
			if tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				if tr.ToolCallID == "call_orphaned" {
					syntheticCount++
				}
			}
		}
	}
	require.Equal(t, 1, syntheticCount, "expected exactly one synthetic result for the orphaned call")
}

func TestPreparePrompt_ToolResultReorderedAfterInterposedUser(t *testing.T) {
	// Reproduces the lightgreen-crocodile failure: a folded /btw user
	// message is persisted between a tool_use and its tool_result, and the
	// real tool_result gets a later created_at than that user turn. On
	// reload the tool_use would be followed by the user message instead of
	// its result, which providers reject. preparePrompt must re-attach the
	// result directly after the calling assistant message.
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt")
	agent := sa.(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	// Assistant emits a tool call.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_x", Name: "view", Input: `{"path":"/f"}`, Finished: true},
		},
	})
	require.NoError(t, err)

	// A [btw] user aside is folded in before the tool finishes.
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "[btw] rename that param"}},
	})
	require.NoError(t, err)

	// The real tool result is persisted last (later created_at).
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "call_x", Name: "view", Content: "file contents"},
		},
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	history, _ := agent.preparePrompt(msgs, true)

	// Find the assistant message carrying the tool call and assert the
	// message immediately after it is the tool result (not the user turn).
	asstIdx := -1
	for i, m := range history {
		if m.Role != fantasy.MessageRoleAssistant {
			continue
		}
		for _, part := range m.Content {
			if tc, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok && tc.ToolCallID == "call_x" {
				asstIdx = i
			}
		}
	}
	require.NotEqual(t, -1, asstIdx, "assistant tool call message must be present")
	require.Less(t, asstIdx+1, len(history), "a message must follow the tool call")

	next := history[asstIdx+1]
	require.Equal(t, fantasy.MessageRoleTool, next.Role, "tool result must immediately follow its tool_use")
	tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](next.Content[0])
	require.True(t, ok)
	require.Equal(t, "call_x", tr.ToolCallID)
	txt, ok := tr.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "the real result (not a synthetic error) must be used")
	require.Contains(t, txt.Text, "file contents")
}

func TestPreparePrompt_ToolResultReorderedAfterShellMessage(t *testing.T) {
	// Bang-mode (!) shell commands persist a message.Shell turn. If one is
	// run while a tool call is still in flight, the shell message lands
	// between the tool_use and its result — same reorder hazard as the
	// folded /btw aside. The tool result must still be re-attached to its
	// calling assistant.
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt")
	agent := sa.(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call_s", Name: "view", Input: `{"path":"/f"}`, Finished: true},
		},
	})
	require.NoError(t, err)

	// A bang-mode shell turn is interposed (command + output parts).
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Shell,
		Parts: []message.ContentPart{
			message.TextContent{Text: "ls -la"},
			message.TextContent{Text: "total 0"},
		},
	})
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "call_s", Name: "view", Content: "file contents"},
		},
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	history, _ := agent.preparePrompt(msgs, true)

	asstIdx := -1
	for i, m := range history {
		if m.Role != fantasy.MessageRoleAssistant {
			continue
		}
		for _, part := range m.Content {
			if tc, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok && tc.ToolCallID == "call_s" {
				asstIdx = i
			}
		}
	}
	require.NotEqual(t, -1, asstIdx, "assistant tool call message must be present")
	require.Less(t, asstIdx+1, len(history), "a message must follow the tool call")

	next := history[asstIdx+1]
	require.Equal(t, fantasy.MessageRoleTool, next.Role, "tool result must immediately follow its tool_use")
	tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](next.Content[0])
	require.True(t, ok)
	require.Equal(t, "call_s", tr.ToolCallID)
	txt, ok := tr.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "the real result (not a synthetic error) must be used")
	require.Contains(t, txt.Text, "file contents")
}

func TestProviderRetryLogFields(t *testing.T) {
	t.Run("nil provider error", func(t *testing.T) {
		fields := providerRetryLogFields(nil, 2*time.Second)
		require.Equal(t, []any{"retry_delay", "2s"}, fields)
	})

	t.Run("provider error with title and message", func(t *testing.T) {
		fields := providerRetryLogFields(&fantasy.ProviderError{
			StatusCode: 429,
			Title:      "rate limit",
			Message:    "too many requests",
		}, 1500*time.Millisecond)
		require.Equal(t, []any{
			"retry_delay", "1.5s",
			"status_code", 429,
			"title", "rate limit",
			"message", "too many requests",
		}, fields)
	})

	t.Run("provider error without optional strings", func(t *testing.T) {
		fields := providerRetryLogFields(&fantasy.ProviderError{
			StatusCode: 503,
		}, time.Second)
		require.Equal(t, []any{
			"retry_delay", "1s",
			"status_code", 503,
		}, fields)
	})
}
