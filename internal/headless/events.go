package headless

import (
	"encoding/json"
	"errors"
)

// Status is the terminal state of a headless run.
type Status string

const (
	// StatusCompleted means the agent finished its turn.
	StatusCompleted Status = "completed"
	// StatusError means the run failed.
	StatusError Status = "error"
	// StatusCancelled means the run was cancelled before finishing.
	StatusCancelled Status = "cancelled"
)

// ExitCode returns the process exit code associated with s.
func (s Status) ExitCode() int {
	switch s {
	case StatusCompleted:
		return ExitOK
	case StatusCancelled:
		return ExitCancelled
	case StatusError:
		return ExitAgentError
	default:
		return ExitFailure
	}
}

// EventType discriminates stream-json lines.
type EventType string

const (
	EventInit       EventType = "init"
	EventText       EventType = "text"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventResult     EventType = "result"
)

// InitEvent is the first stream-json line. It identifies the session so
// a caller can resume, attach to, or cancel the run even if it never
// reads the final result.
type InitEvent struct {
	Type          EventType `json:"type"`
	SessionID     string    `json:"session_id"`
	Address       string    `json:"address,omitempty"`
	WorkspaceID   string    `json:"workspace_id"`
	RunID         string    `json:"run_id"`
	Cwd           string    `json:"cwd"`
	Provider      string    `json:"provider,omitempty"`
	Model         string    `json:"model,omitempty"`
	AttachCommand string    `json:"attach_command,omitempty"`
}

// TextEvent carries a newly streamed chunk of assistant text.
type TextEvent struct {
	Type      EventType `json:"type"`
	SessionID string    `json:"session_id"`
	MessageID string    `json:"message_id"`
	Text      string    `json:"text"`
}

// ToolCallEvent is emitted once per tool call, after its input has been
// fully streamed.
type ToolCallEvent struct {
	Type       EventType       `json:"type"`
	SessionID  string          `json:"session_id"`
	MessageID  string          `json:"message_id"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input,omitempty"`
}

// ToolResultEvent is emitted once per tool result. Content is truncated
// to [MaxToolResultBytes] to keep the stream readable.
type ToolResultEvent struct {
	Type       EventType `json:"type"`
	SessionID  string    `json:"session_id"`
	ToolCallID string    `json:"tool_call_id"`
	Name       string    `json:"name"`
	Content    string    `json:"content"`
	Truncated  bool      `json:"truncated,omitempty"`
	IsError    bool      `json:"is_error,omitempty"`
}

// MaxToolResultBytes caps tool result content in stream-json output.
const MaxToolResultBytes = 4096

// Usage is the token and cost consumption attributed to a run.
type Usage struct {
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// Diagnostic is a single LSP diagnostic in a file touched by the run.
// Line and Column are 1-based.
type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
}

// DiagnosticSummary aggregates the LSP errors and warnings that remain
// in files the run changed.
type DiagnosticSummary struct {
	Errors   int          `json:"errors"`
	Warnings int          `json:"warnings"`
	Items    []Diagnostic `json:"items,omitempty"`
}

// MaxDiagnosticItems caps the number of diagnostics listed in a result.
const MaxDiagnosticItems = 50

// Result is the final object of every structured run: the only line in
// json mode and the last line in stream-json mode.
type Result struct {
	Type          EventType          `json:"type"`
	Status        Status             `json:"status"`
	ExitCode      int                `json:"exit_code"`
	SessionID     string             `json:"session_id,omitempty"`
	Address       string             `json:"address,omitempty"`
	WorkspaceID   string             `json:"workspace_id,omitempty"`
	RunID         string             `json:"run_id,omitempty"`
	MessageID     string             `json:"message_id,omitempty"`
	Cwd           string             `json:"cwd,omitempty"`
	Text          string             `json:"text"`
	Error         string             `json:"error,omitempty"`
	DurationMS    int64              `json:"duration_ms"`
	Usage         *Usage             `json:"usage,omitempty"`
	FilesChanged  []string           `json:"files_changed"`
	Diagnostics   *DiagnosticSummary `json:"diagnostics,omitempty"`
	AttachCommand string             `json:"attach_command,omitempty"`
}

// NewResult returns a result with the type and exit code derived from
// status.
func NewResult(status Status) Result {
	return Result{
		Type:         EventResult,
		Status:       status,
		ExitCode:     status.ExitCode(),
		FilesChanged: []string{},
	}
}

// Err returns nil for a completed result and an [ExitError] carrying the
// result's exit code otherwise. The error is marked silent because the
// result object already reports it.
func (r Result) Err() error {
	if r.ExitCode == ExitOK {
		return nil
	}
	msg := r.Error
	if msg == "" {
		msg = string(r.Status)
	}
	return &ExitError{Code: r.ExitCode, Err: errors.New(msg), Silent: true}
}
