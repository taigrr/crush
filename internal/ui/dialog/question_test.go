package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
)

func newTestQuestion(t *testing.T, req question.Request) *Question {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	return NewQuestion(com, req)
}

func TestQuestion_SingleChoice_NavigateAndConfirm(t *testing.T) {
	t.Parallel()

	req := question.Request{
		ID:         "q-1",
		SessionID:  "s-1",
		ToolCallID: "call-1",
		Kind:       question.KindSingleChoice,
		Prompt:     "Which one?",
		Options:    []string{"a", "b", "c"},
	}
	q := newTestQuestion(t, req)
	require.Equal(t, 0, q.cursor)

	q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, q.cursor)

	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionQuestionResponse)
	require.True(t, ok)
	require.Equal(t, []string{"b"}, resp.Answer.Selected)
	require.False(t, resp.Answer.Cancelled)
	require.Equal(t, "q-1", resp.Answer.ID)
	require.Equal(t, "s-1", resp.Answer.SessionID)
	require.Equal(t, "call-1", resp.Answer.ToolCallID)
}

func TestQuestion_MultipleChoice_ToggleAndConfirm(t *testing.T) {
	t.Parallel()

	req := question.Request{
		ID:      "q-2",
		Kind:    question.KindMultipleChoice,
		Prompt:  "Which ones?",
		Options: []string{"a", "b", "c"},
	}
	q := newTestQuestion(t, req)

	q.HandleMsg(keyMsg(' ')) // toggle "a"
	q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	q.HandleMsg(keyMsg(' ')) // toggle "c"

	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionQuestionResponse)
	require.True(t, ok)
	require.Equal(t, []string{"a", "c"}, resp.Answer.Selected)
}

func TestQuestion_YesNo_Confirm(t *testing.T) {
	t.Parallel()

	req := question.Request{ID: "q-3", Kind: question.KindYesNo, Prompt: "Proceed?"}
	q := newTestQuestion(t, req)

	q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}) // move to "No"
	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionQuestionResponse)
	require.True(t, ok)
	require.Equal(t, []string{"no"}, resp.Answer.Selected)
}

func TestQuestion_FreeText_Confirm(t *testing.T) {
	t.Parallel()

	req := question.Request{ID: "q-4", Kind: question.KindFreeText, Prompt: "What next?"}
	q := newTestQuestion(t, req)

	for _, r := range "hello" {
		q.HandleMsg(keyMsg(r))
	}

	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionQuestionResponse)
	require.True(t, ok)
	require.Equal(t, []string{"hello"}, resp.Answer.Selected)
}

func TestQuestion_FreeText_EmptyConfirmBlocked(t *testing.T) {
	t.Parallel()

	req := question.Request{ID: "q-4b", Kind: question.KindFreeText, Prompt: "What next?"}
	q := newTestQuestion(t, req)

	// Confirming with no input must not resolve; it returns nil and
	// sets a hint. Only Escape (cancel) or a real answer resolves.
	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, action)
	require.NotEmpty(t, q.hint)
}

func TestQuestion_MultipleChoice_EmptyConfirmBlocked(t *testing.T) {
	t.Parallel()

	req := question.Request{
		ID:      "q-4c",
		Kind:    question.KindMultipleChoice,
		Prompt:  "Which ones?",
		Options: []string{"a", "b"},
	}
	q := newTestQuestion(t, req)

	// Confirming with nothing checked must not resolve.
	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, action)
	require.NotEmpty(t, q.hint)

	// After selecting one, confirm resolves.
	q.HandleMsg(keyMsg(' '))
	action = q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionQuestionResponse)
	require.True(t, ok)
	require.Equal(t, []string{"a"}, resp.Answer.Selected)
}

func TestQuestion_EscapeCancels(t *testing.T) {
	t.Parallel()

	req := question.Request{ID: "q-5", Kind: question.KindYesNo, Prompt: "Proceed?"}
	q := newTestQuestion(t, req)

	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	resp, ok := action.(ActionQuestionResponse)
	require.True(t, ok)
	require.True(t, resp.Answer.Cancelled)
}

func TestQuestion_IDMirrorsRequest(t *testing.T) {
	t.Parallel()

	req := question.Request{ID: "q-6", SessionID: "s-6", ToolCallID: "call-6", Kind: question.KindYesNo, Prompt: "?"}
	q := newTestQuestion(t, req)
	require.Equal(t, QuestionID, q.ID())
	require.Equal(t, "s-6", q.SessionID())
	require.Equal(t, "call-6", q.ToolCallID())
}
