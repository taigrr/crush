package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/question"
)

func runQuestionTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params QuestionParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  QuestionToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

func TestQuestionTool_HardFailsWhenNonInteractive(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", true /* skip */, nil)
	questions := question.NewQuestionService()
	tool := NewQuestionTool(perms, questions)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp := runQuestionTool(t, tool, ctx, QuestionParams{
		Kind:   "yes_no",
		Prompt: "Should I proceed?",
	})

	require.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "no interactive client")
}

func TestQuestionTool_HardFailsForUnattendedSessionPolicy(t *testing.T) {
	t.Parallel()

	for _, policy := range []permission.SessionPolicy{permission.SessionPolicyAllow, permission.SessionPolicyDeny} {
		perms := permission.NewPermissionService("/tmp", false, nil)
		perms.SetSessionPolicy("headless", policy)
		tool := NewQuestionTool(perms, question.NewQuestionService())

		ctx := context.WithValue(t.Context(), SessionIDContextKey, "headless")
		resp := runQuestionTool(t, tool, ctx, QuestionParams{Kind: "yes_no", Prompt: "Proceed?"})
		require.True(t, resp.IsError, policy)
		assert.Contains(t, resp.Content, "no interactive client", policy)
	}
}

func TestQuestionTool_RunPolicy(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", true /* skip */, nil)
	tool := NewQuestionTool(perms, question.NewQuestionService())

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "headless")
	resp := runQuestionTool(t, tool, permission.WithPolicy(ctx, permission.SessionPolicyDeny), QuestionParams{Kind: "yes_no", Prompt: "Proceed?"})
	require.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "no interactive client")

	// A delegated run's question reaches its caller even under workspace
	// yolo: it blocks in Ask (here until the context is cancelled)
	// instead of failing fast.
	delegated, cancel := context.WithCancel(permission.WithPolicy(ctx, permission.SessionPolicyPrompt))
	cancel()
	resp = runQuestionTool(t, tool, delegated, QuestionParams{Kind: "yes_no", Prompt: "Proceed?"})
	require.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "question was not answered")
}

func TestQuestionCapable_FollowsRunPolicy(t *testing.T) {
	t.Parallel()

	skipping := permission.NewPermissionService("/tmp", true, nil)
	interactive := permission.NewPermissionService("/tmp", false, nil)
	interactive.AutoApproveSession("fetch-child")
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "s")

	assert.True(t, QuestionCapable(skipping)(permission.WithPolicy(ctx, permission.SessionPolicyPrompt)),
		"a delegated run can ask even when the workspace skips prompts")
	assert.False(t, QuestionCapable(skipping)(ctx))
	assert.True(t, QuestionCapable(interactive)(ctx))
	assert.False(t, QuestionCapable(interactive)(permission.WithPolicy(ctx, permission.SessionPolicyDeny)))
	assert.False(t, QuestionCapable(interactive)(permission.WithPolicy(ctx, permission.SessionPolicyAllow)))

	child := context.WithValue(permission.WithPolicy(t.Context(), permission.SessionPolicyPrompt), SessionIDContextKey, "fetch-child")
	assert.False(t, QuestionCapable(interactive)(child), "an auto-approving sub-agent is unattended")
}

func TestQuestionTool_NotAvailableInNonInteractiveContext(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", true /* skip */, nil)
	available := QuestionCapable(perms)
	assert.False(t, available(t.Context()))

	perms2 := permission.NewPermissionService("/tmp", false, nil)
	available2 := QuestionCapable(perms2)
	assert.True(t, available2(t.Context()))
}

func TestQuestionTool_RejectsInvalidKind(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", false, nil)
	questions := question.NewQuestionService()
	tool := NewQuestionTool(perms, questions)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp := runQuestionTool(t, tool, ctx, QuestionParams{
		Kind:   "not_a_kind",
		Prompt: "Anything?",
	})

	require.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "invalid kind")
}

func TestQuestionTool_RejectsMissingPrompt(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", false, nil)
	questions := question.NewQuestionService()
	tool := NewQuestionTool(perms, questions)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp := runQuestionTool(t, tool, ctx, QuestionParams{Kind: "yes_no"})

	require.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "prompt is required")
}

func TestQuestionTool_RequiresTwoOptionsForChoiceKinds(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", false, nil)
	questions := question.NewQuestionService()
	tool := NewQuestionTool(perms, questions)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp := runQuestionTool(t, tool, ctx, QuestionParams{
		Kind:    "single_choice",
		Prompt:  "Pick one",
		Options: []string{"only one"},
	})

	require.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "at least two entries")
}

func TestQuestionTool_AskAnswerRoundTrip(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", false, nil)
	questions := question.NewQuestionService()
	tool := NewQuestionTool(perms, questions)

	requests := questions.Subscribe(t.Context())

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	respCh := make(chan fantasy.ToolResponse, 1)
	go func() {
		respCh <- runQuestionTool(t, tool, ctx, QuestionParams{
			Kind:    "single_choice",
			Prompt:  "Which environment?",
			Options: []string{"staging", "production"},
		})
	}()

	var req question.Request
	select {
	case ev := <-requests:
		req = ev.Payload
	case <-time.After(time.Second):
		t.Fatal("question request was not published")
	}

	assert.Equal(t, "session-1", req.SessionID)
	assert.Equal(t, "test-call", req.ToolCallID)
	assert.Equal(t, question.KindSingleChoice, req.Kind)

	require.True(t, questions.Answer(question.Answer{
		ID:         req.ID,
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		Selected:   []string{"production"},
	}))

	select {
	case resp := <-respCh:
		assert.False(t, resp.IsError)
		assert.Contains(t, resp.Content, "production")
	case <-time.After(time.Second):
		t.Fatal("tool did not return after answer")
	}
}

func TestQuestionTool_CancelledAnswerIsNotAnError(t *testing.T) {
	t.Parallel()

	perms := permission.NewPermissionService("/tmp", false, nil)
	questions := question.NewQuestionService()
	tool := NewQuestionTool(perms, questions)

	requests := questions.Subscribe(t.Context())
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	respCh := make(chan fantasy.ToolResponse, 1)
	go func() {
		respCh <- runQuestionTool(t, tool, ctx, QuestionParams{
			Kind:   "free_text",
			Prompt: "Anything else?",
		})
	}()

	var req question.Request
	select {
	case ev := <-requests:
		req = ev.Payload
	case <-time.After(time.Second):
		t.Fatal("question request was not published")
	}

	require.True(t, questions.Answer(question.Answer{
		ID:         req.ID,
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		Cancelled:  true,
	}))

	select {
	case resp := <-respCh:
		assert.False(t, resp.IsError)
		assert.Contains(t, resp.Content, "best judgment")
	case <-time.After(time.Second):
		t.Fatal("tool did not return after cancellation")
	}
}
