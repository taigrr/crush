package question

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuestionService_AskAnswer(t *testing.T) {
	t.Parallel()
	svc := NewQuestionService()

	requests := svc.Subscribe(t.Context())
	notifications := svc.SubscribeNotifications(t.Context())

	var (
		answer Answer
		err    error
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		answer, err = svc.Ask(t.Context(), CreateQuestionRequest{
			SessionID:  "s-1",
			ToolCallID: "call-1",
			Kind:       KindSingleChoice,
			Prompt:     "Which one?",
			Options:    []string{"a", "b"},
		})
	}()

	var req Request
	select {
	case ev := <-requests:
		req = ev.Payload
	case <-time.After(time.Second):
		t.Fatal("question request was not published")
	}

	require.Equal(t, "s-1", req.SessionID)
	require.Equal(t, "call-1", req.ToolCallID)
	require.Equal(t, KindSingleChoice, req.Kind)
	require.Equal(t, []string{"a", "b"}, req.Options)

	resolved := svc.Answer(Answer{
		ID:         req.ID,
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		Selected:   []string{"b"},
	})
	assert.True(t, resolved)

	wg.Wait()
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, answer.Selected)
	assert.False(t, answer.Cancelled)

	select {
	case ev := <-notifications:
		assert.True(t, ev.Payload.Answered)
		assert.False(t, ev.Payload.Cancelled)
		assert.Equal(t, "call-1", ev.Payload.ToolCallID)
	case <-time.After(time.Second):
		t.Fatal("answer did not publish a notification")
	}
}

func TestQuestionService_AnswerIdempotent(t *testing.T) {
	t.Parallel()
	svc := NewQuestionService()
	requests := svc.Subscribe(t.Context())

	go func() {
		_, _ = svc.Ask(t.Context(), CreateQuestionRequest{
			SessionID:  "s-2",
			ToolCallID: "call-2",
			Kind:       KindYesNo,
			Prompt:     "Proceed?",
		})
	}()

	var req Request
	select {
	case ev := <-requests:
		req = ev.Payload
	case <-time.After(time.Second):
		t.Fatal("question request was not published")
	}

	ans := Answer{ID: req.ID, SessionID: req.SessionID, ToolCallID: req.ToolCallID, Selected: []string{"yes"}}
	assert.True(t, svc.Answer(ans))
	// A second resolution attempt for the same ID must be a no-op, not
	// an error: it should report false without panicking or double
	// publishing a notification via a second channel send.
	assert.False(t, svc.Answer(ans))
}

func TestQuestionService_CancelPublishesCancellation(t *testing.T) {
	t.Parallel()
	svc := NewQuestionService()

	requests := svc.Subscribe(t.Context())
	notifications := svc.SubscribeNotifications(t.Context())

	ctx, cancel := context.WithCancel(t.Context())

	var (
		answer Answer
		err    error
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		answer, err = svc.Ask(ctx, CreateQuestionRequest{
			SessionID:  "s-cancel",
			ToolCallID: "call-cancel",
			Kind:       KindFreeText,
			Prompt:     "What should I do?",
		})
	}()

	<-requests
	cancel()
	wg.Wait()

	require.Error(t, err)
	assert.True(t, answer.Cancelled)

	select {
	case ev := <-notifications:
		assert.True(t, ev.Payload.Cancelled)
		assert.False(t, ev.Payload.Answered)
		assert.Equal(t, "call-cancel", ev.Payload.ToolCallID)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not publish a notification")
	}
}

func TestQuestionService_ConcurrentSessionsAreIndependent(t *testing.T) {
	t.Parallel()
	svc := NewQuestionService()
	requests := svc.Subscribe(t.Context())

	// Session A asks and blocks (never answered until the very end).
	aDone := make(chan Answer, 1)
	go func() {
		ans, _ := svc.Ask(t.Context(), CreateQuestionRequest{
			SessionID:  "s-A",
			ToolCallID: "call-A",
			Kind:       KindFreeText,
			Prompt:     "A?",
		})
		aDone <- ans
	}()

	// Wait for A's request to publish.
	var reqA Request
	select {
	case ev := <-requests:
		reqA = ev.Payload
	case <-time.After(2 * time.Second):
		t.Fatal("session A request never published")
	}
	require.Equal(t, "s-A", reqA.SessionID)

	// While A is still pending (unanswered), session B must be able to
	// ask, have its request PUBLISH, and be answered independently.
	// Before the fix this blocked on a service-wide mutex and B's
	// request never published.
	bDone := make(chan Answer, 1)
	go func() {
		ans, _ := svc.Ask(t.Context(), CreateQuestionRequest{
			SessionID:  "s-B",
			ToolCallID: "call-B",
			Kind:       KindFreeText,
			Prompt:     "B?",
		})
		bDone <- ans
	}()

	var reqB Request
	select {
	case ev := <-requests:
		reqB = ev.Payload
	case <-time.After(2 * time.Second):
		t.Fatal("session B request never published while A was pending (cross-session wedge)")
	}
	require.Equal(t, "s-B", reqB.SessionID)

	// Answer B first; it must resolve independently of A.
	require.True(t, svc.Answer(Answer{ID: reqB.ID, SessionID: "s-B", ToolCallID: "call-B", Selected: []string{"b-ans"}}))
	select {
	case ans := <-bDone:
		assert.Equal(t, []string{"b-ans"}, ans.Selected)
	case <-time.After(2 * time.Second):
		t.Fatal("session B did not resolve while A was still pending")
	}

	// A should still be pending; verify it hasn't resolved.
	select {
	case <-aDone:
		t.Fatal("session A resolved unexpectedly when only B was answered")
	default:
	}

	// Now answer A.
	require.True(t, svc.Answer(Answer{ID: reqA.ID, Selected: []string{"a-ans"}}))
	select {
	case ans := <-aDone:
		assert.Equal(t, []string{"a-ans"}, ans.Selected)
	case <-time.After(2 * time.Second):
		t.Fatal("session A did not resolve after answering")
	}
}

func TestQuestionService_AnswerRejectsWrongSessionRouting(t *testing.T) {
	t.Parallel()
	svc := NewQuestionService()
	requests := svc.Subscribe(t.Context())
	notifications := svc.SubscribeNotifications(t.Context())

	done := make(chan Answer, 1)
	go func() {
		ans, _ := svc.Ask(t.Context(), CreateQuestionRequest{
			SessionID:  "s-real",
			ToolCallID: "call-real",
			Kind:       KindYesNo,
			Prompt:     "Proceed?",
		})
		done <- ans
	}()

	var req Request
	select {
	case ev := <-requests:
		req = ev.Payload
	case <-time.After(2 * time.Second):
		t.Fatal("request never published")
	}

	// A valid ID but a mismatched SessionID must be rejected: a client
	// must not resolve (and dismiss the dialog / route the notification
	// for) a question belonging to another session.
	assert.False(t, svc.Answer(Answer{ID: req.ID, SessionID: "s-attacker", Selected: []string{"yes"}}))
	// Mismatched ToolCallID likewise.
	assert.False(t, svc.Answer(Answer{ID: req.ID, ToolCallID: "call-attacker", Selected: []string{"yes"}}))

	// Neither rejected attempt may have published a notification or
	// resolved the pending request.
	select {
	case <-done:
		t.Fatal("question resolved by a mismatched-routing answer")
	case <-notifications:
		t.Fatal("mismatched-routing answer published a notification")
	default:
	}

	// The correct answer still works, and the resulting notification
	// carries the STORED (trusted) routing fields.
	require.True(t, svc.Answer(Answer{ID: req.ID, SessionID: "s-real", ToolCallID: "call-real", Selected: []string{"yes"}}))
	select {
	case ans := <-done:
		assert.Equal(t, []string{"yes"}, ans.Selected)
		assert.Equal(t, "s-real", ans.SessionID)
		assert.Equal(t, "call-real", ans.ToolCallID)
	case <-time.After(2 * time.Second):
		t.Fatal("correct answer did not resolve the question")
	}

	select {
	case ev := <-notifications:
		assert.Equal(t, "s-real", ev.Payload.SessionID)
		assert.Equal(t, "call-real", ev.Payload.ToolCallID)
		assert.True(t, ev.Payload.Answered)
	case <-time.After(time.Second):
		t.Fatal("no notification published for the correct answer")
	}
}
