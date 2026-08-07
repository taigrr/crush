package proto

// QuestionKind mirrors question.Kind on the wire.
type QuestionKind string

const (
	QuestionKindSingleChoice   QuestionKind = "single_choice"
	QuestionKindMultipleChoice QuestionKind = "multiple_choice"
	QuestionKindFreeText       QuestionKind = "free_text"
	QuestionKindYesNo          QuestionKind = "yes_no"
)

// QuestionRequest represents a pending question awaiting an answer.
type QuestionRequest struct {
	ID         string       `json:"id"`
	SessionID  string       `json:"session_id"`
	ToolCallID string       `json:"tool_call_id"`
	Kind       QuestionKind `json:"kind"`
	Prompt     string       `json:"prompt"`
	Options    []string     `json:"options,omitempty"`
}

// QuestionNotification represents a notification about a question
// being resolved (answered or cancelled).
type QuestionNotification struct {
	SessionID  string `json:"session_id"`
	ToolCallID string `json:"tool_call_id"`
	Answered   bool   `json:"answered"`
	Cancelled  bool   `json:"cancelled"`
}

// QuestionAnswer represents a client's answer to a pending question.
type QuestionAnswer struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"session_id"`
	ToolCallID string   `json:"tool_call_id"`
	Selected   []string `json:"selected,omitempty"`
	Cancelled  bool     `json:"cancelled,omitempty"`
}

// QuestionAnswerResponse is the server's response to a question answer
// call. Resolved is true when this call resolved the pending request,
// false when it had already been resolved by a previous caller. A
// false value is not an error.
type QuestionAnswerResponse struct {
	Resolved bool `json:"resolved"`
}
