package headless

import (
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// Emitter receives structured events as a run progresses.
type Emitter interface {
	Emit(v any) error
}

// NDJSONEmitter writes each event as one line of JSON.
type NDJSONEmitter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewNDJSONEmitter returns an emitter that writes to w.
func NewNDJSONEmitter(w io.Writer) *NDJSONEmitter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &NDJSONEmitter{enc: enc}
}

// Emit implements [Emitter].
func (e *NDJSONEmitter) Emit(v any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enc.Encode(v)
}

// Outcome is the terminal state observed by a [Tracker].
type Outcome struct {
	Status    Status
	MessageID string
	Text      string
	Error     string
}

// Tracker translates the workspace SSE stream into headless events for
// a single run and reports when that run ends.
//
// Correlation follows the same rules as the text-mode runner: when a
// RunID is set, only the RunComplete carrying that RunID ends the run.
// Message events carry no RunID, so when the caller's prompt may have
// been queued behind another turn on the same session (see
// [Tracker.AwaitUserPrompt]) streaming is held back until the caller's
// own user message appears.
type Tracker struct {
	sessionID string
	runID     string
	emit      Emitter

	awaitStart bool
	prompt     string

	text    map[string]string
	calls   map[string]bool
	results map[string]bool
}

// NewTracker returns a tracker for the run identified by sessionID and
// runID. emit may be nil when only the outcome is needed.
func NewTracker(sessionID, runID string, emit Emitter) *Tracker {
	return &Tracker{
		sessionID: sessionID,
		runID:     runID,
		emit:      emit,
		text:      make(map[string]string),
		calls:     make(map[string]bool),
		results:   make(map[string]bool),
	}
}

// AwaitUserPrompt holds back message streaming until a user message
// whose text equals prompt is observed. Use it when the session was
// already busy at send time, so output from the turn ahead of ours is
// not attributed to this run.
func (t *Tracker) AwaitUserPrompt(prompt string) {
	t.awaitStart = true
	t.prompt = strings.TrimSpace(prompt)
}

// Handle processes one SSE event. It returns a non-nil [Outcome] when
// the tracked run has ended. The error is non-nil only when emitting an
// event failed.
func (t *Tracker) Handle(ev any) (*Outcome, error) {
	switch e := ev.(type) {
	case pubsub.Event[proto.Message]:
		return nil, t.handleMessage(e.Payload)

	case pubsub.Event[proto.RunComplete]:
		rc := e.Payload
		if t.runID != "" {
			if rc.RunID != t.runID {
				return nil, nil
			}
		} else if rc.SessionID != t.sessionID {
			return nil, nil
		}
		if err := t.emitText(rc.MessageID, rc.Text); err != nil {
			return nil, err
		}
		text := rc.Text
		if text == "" {
			text = t.text[rc.MessageID]
		}
		out := &Outcome{MessageID: rc.MessageID, Text: text, Error: rc.Error}
		switch {
		case rc.Cancelled:
			out.Status = StatusCancelled
		case rc.Error != "":
			out.Status = StatusError
		default:
			out.Status = StatusCompleted
		}
		return out, nil

	case pubsub.Event[proto.AgentEvent]:
		if e.Payload.Error == nil {
			return nil, nil
		}
		if e.Payload.RunID != "" {
			if e.Payload.RunID != t.runID {
				return nil, nil
			}
		} else if e.Payload.SessionID == "" || e.Payload.SessionID != t.sessionID {
			return nil, nil
		}
		return &Outcome{Status: StatusError, Error: e.Payload.Error.Error()}, nil
	}
	return nil, nil
}

func (t *Tracker) handleMessage(msg proto.Message) error {
	if msg.SessionID != t.sessionID {
		return nil
	}
	if t.awaitStart {
		if msg.Role == proto.User && strings.TrimSpace(msg.Content().String()) == t.prompt {
			t.awaitStart = false
		}
		return nil
	}

	switch msg.Role {
	case proto.Assistant:
		if err := t.emitText(msg.ID, msg.Content().String()); err != nil {
			return err
		}
		finished := msg.IsFinished()
		for _, tc := range msg.ToolCalls() {
			if t.calls[tc.ID] || (!tc.Finished && !finished) {
				continue
			}
			t.calls[tc.ID] = true
			if err := t.send(ToolCallEvent{
				Type:       EventToolCall,
				SessionID:  t.sessionID,
				MessageID:  msg.ID,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Input:      rawInput(tc.Input),
			}); err != nil {
				return err
			}
		}

	case proto.Tool:
		for _, tr := range msg.ToolResults() {
			if t.results[tr.ToolCallID] {
				continue
			}
			t.results[tr.ToolCallID] = true
			content, truncated := truncate(tr.Content, MaxToolResultBytes)
			if err := t.send(ToolResultEvent{
				Type:       EventToolResult,
				SessionID:  t.sessionID,
				ToolCallID: tr.ToolCallID,
				Name:       tr.Name,
				Content:    content,
				Truncated:  truncated,
				IsError:    tr.IsError,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitText sends the not-yet-emitted suffix of full for message msgID.
// An update that does not extend the previously seen text (a reordered
// or rewritten update) only rebases the cursor without emitting, since
// the final result always carries the authoritative text.
func (t *Tracker) emitText(msgID, full string) error {
	if msgID == "" || full == "" {
		return nil
	}
	prev := t.text[msgID]
	t.text[msgID] = full
	if len(full) <= len(prev) || !strings.HasPrefix(full, prev) {
		return nil
	}
	return t.send(TextEvent{
		Type:      EventText,
		SessionID: t.sessionID,
		MessageID: msgID,
		Text:      full[len(prev):],
	})
}

func (t *Tracker) send(v any) error {
	if t.emit == nil {
		return nil
	}
	return t.emit.Emit(v)
}

func rawInput(input string) json.RawMessage {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	if json.Valid([]byte(input)) {
		return json.RawMessage(input)
	}
	quoted, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	return quoted
}

func truncate(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
