package backend

import (
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
)

// AnswerQuestion resolves a pending question request. The returned bool
// reports whether this call resolved the pending request (true) or
// found it already resolved by a previous caller (false). A false
// return is not an error.
func (b *Backend) AnswerQuestion(workspaceID string, req proto.QuestionAnswer) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}

	return ws.Questions.Answer(question.Answer{
		ID:         req.ID,
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		Selected:   req.Selected,
		Cancelled:  req.Cancelled,
	}), nil
}
