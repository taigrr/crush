package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
)

// streamStallTimeout bounds how long a turn may go without any activity
// from the provider stream while no tool is executing. A provider that
// accepts the request but then never sends a byte (or stops mid-stream
// without closing the connection) would otherwise block the turn
// forever; for sub-agents such as the review tool's reviewers that also
// wedges the parent turn until the user cancels it by hand.
var streamStallTimeout = 2 * time.Minute

// keepaliveStreamStallTimeout applies to providers whose streams carry
// keepalive pings during long generations (Anthropic's own Messages API),
// so a long silence there is a real stall rather than slow output.
//
// Bedrock is deliberately excluded even though it serves the same Messages
// protocol: it does not forward Anthropic's ping events. Measured against a
// live Bedrock endpoint, a write tool call with ~30 KB of content arrived
// after a single silent gap of ~95 s with no events at all, because the
// tool input is buffered until the model has finished generating it.
var keepaliveStreamStallTimeout = 30 * time.Second

// toolInputStallTimeout applies while the model is producing a tool call's
// input. Providers may buffer that input (Bedrock does, even with
// fine-grained tool streaming requested), so the stream can be silent for
// as long as the model takes to generate it, which grows with the size of
// the argument (e.g. a large file passed to write).
var toolInputStallTimeout = 10 * time.Minute

// streamStallTimeoutFor returns the idle stall timeout for a fantasy
// provider name as reported by LanguageModel.Provider.
func streamStallTimeoutFor(provider string) time.Duration {
	switch provider {
	case anthropic.Name:
		return keepaliveStreamStallTimeout
	default:
		return streamStallTimeout
	}
}

// errStreamStalled is the cancellation cause recorded when the watchdog
// fires.
var errStreamStalled = errors.New("provider stream stalled")

// streamWatchdog cancels a stream context when neither stream events nor
// tool executions have been observed for the configured timeout. Time
// spent inside tools is excluded: long-running tools (bash, sub-agents)
// are not provider stalls.
type streamWatchdog struct {
	timeout          time.Duration
	toolInputTimeout time.Duration
	cancel           context.CancelCauseFunc

	mu          sync.Mutex
	timer       *time.Timer
	activeTools int
	// openToolInputs counts tool calls whose input has started but not
	// ended streaming; while non-zero the longer toolInputTimeout applies.
	openToolInputs int
	stopped        bool
}

func newStreamWatchdog(ctx context.Context, timeout, toolInputTimeout time.Duration) (context.Context, *streamWatchdog) {
	ctx, cancel := context.WithCancelCause(ctx)
	w := &streamWatchdog{timeout: timeout, toolInputTimeout: max(timeout, toolInputTimeout), cancel: cancel}
	if timeout > 0 {
		w.timer = time.AfterFunc(timeout, w.fire)
	}
	return ctx, w
}

// currentTimeout is the countdown for the current phase. Callers must
// hold w.mu.
func (w *streamWatchdog) currentTimeout() time.Duration {
	if w.openToolInputs > 0 {
		return w.toolInputTimeout
	}
	return w.timeout
}

func (w *streamWatchdog) fire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.activeTools > 0 {
		return
	}
	w.cancel(fmt.Errorf("%w: no response from the model for %s", errStreamStalled, w.currentTimeout()))
}

// touch records stream activity and restarts the countdown.
func (w *streamWatchdog) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.resetLocked()
}

// observe records a stream part, tracking whether a tool call's input is
// being generated, and restarts the countdown for the resulting phase.
func (w *streamWatchdog) observe(part fantasy.StreamPart) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch part.Type {
	case fantasy.StreamPartTypeToolInputStart:
		w.openToolInputs++
	case fantasy.StreamPartTypeToolInputEnd:
		w.openToolInputs = max(0, w.openToolInputs-1)
	}
	w.resetLocked()
}

// beginStep clears tool-input tracking at a step boundary: input left open
// by a step that ended mid-call must not extend the next step's timeout.
func (w *streamWatchdog) beginStep() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.openToolInputs = 0
	w.resetLocked()
}

func (w *streamWatchdog) resetLocked() {
	if w.timer == nil || w.stopped || w.activeTools > 0 {
		return
	}
	w.timer.Reset(w.currentTimeout())
}

// toolStarted pauses the countdown until the matching toolFinished.
func (w *streamWatchdog) toolStarted() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.activeTools++
	if w.timer != nil {
		w.timer.Stop()
	}
}

func (w *streamWatchdog) toolFinished() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.activeTools--
	if w.activeTools == 0 {
		w.resetLocked()
	}
}

// stop disarms the watchdog and releases its context.
func (w *streamWatchdog) stop() {
	w.mu.Lock()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	w.cancel(context.Canceled)
}

// stallErr returns the stall error if the watchdog canceled ctx, else nil.
func stallErr(ctx context.Context) error {
	if cause := context.Cause(ctx); errors.Is(cause, errStreamStalled) {
		return cause
	}
	return nil
}

func wrapToolsWithWatchdog(tools []fantasy.AgentTool, w *streamWatchdog) []fantasy.AgentTool {
	if w == nil || len(tools) == 0 {
		return tools
	}
	wrapped := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		wrapped[i] = &watchdogTool{AgentTool: tool, watchdog: w}
	}
	return wrapped
}

type watchdogTool struct {
	fantasy.AgentTool
	watchdog *streamWatchdog
}

func (t *watchdogTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	t.watchdog.toolStarted()
	defer t.watchdog.toolFinished()
	return t.AgentTool.Run(ctx, call)
}
