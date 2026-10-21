package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
	"github.com/taigrr/fantasy/providers/bedrock"
)

// streamStallTimeout bounds how long a turn may go without any activity
// from the provider stream while no tool is executing. A provider that
// accepts the request but then never sends a byte (or stops mid-stream
// without closing the connection) would otherwise block the turn
// forever; for sub-agents such as the review tool's reviewers that also
// wedges the parent turn until the user cancels it by hand.
var streamStallTimeout = 2 * time.Minute

// keepaliveStreamStallTimeout applies to providers whose streams carry
// keepalive pings (the Anthropic Messages protocol, including Bedrock),
// so a long silence there is a real stall rather than slow reasoning.
var keepaliveStreamStallTimeout = 30 * time.Second

// streamStallTimeoutFor returns the stall timeout for a fantasy provider
// name as reported by LanguageModel.Provider.
func streamStallTimeoutFor(provider string) time.Duration {
	switch provider {
	case anthropic.Name, bedrock.Name:
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
	timeout time.Duration
	cancel  context.CancelCauseFunc

	mu          sync.Mutex
	timer       *time.Timer
	activeTools int
	stopped     bool
}

func newStreamWatchdog(ctx context.Context, timeout time.Duration) (context.Context, *streamWatchdog) {
	ctx, cancel := context.WithCancelCause(ctx)
	w := &streamWatchdog{timeout: timeout, cancel: cancel}
	if timeout > 0 {
		w.timer = time.AfterFunc(timeout, w.fire)
	}
	return ctx, w
}

func (w *streamWatchdog) fire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.activeTools > 0 {
		return
	}
	w.cancel(fmt.Errorf("%w: no response from the model for %s", errStreamStalled, w.timeout))
}

// touch records stream activity and restarts the countdown.
func (w *streamWatchdog) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer == nil || w.stopped || w.activeTools > 0 {
		return
	}
	w.timer.Reset(w.timeout)
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
	if w.timer != nil && !w.stopped && w.activeTools == 0 {
		w.timer.Reset(w.timeout)
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
