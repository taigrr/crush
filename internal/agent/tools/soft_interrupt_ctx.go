package tools

import "context"

// softInterruptContextKey carries the per-step soft-interrupt channel from
// the agent loop to the tools it runs. A soft interrupt is a request, not
// a cancellation: it tells a long-running tool that a user message is
// waiting (a mid-turn steer) or that the user asked to background the
// running work, and that it should wrap up early if it can do so without
// losing its result. Tools that do not opt in are unaffected — the
// channel is only observed by tools that select on it — and a tool that
// does opt in must still return a complete, valid result (for example
// "moved to background, job id X") rather than an error.
type softInterruptContextKey struct{}

// WithSoftInterrupt returns ctx tagged with a channel that is closed when
// the agent wants in-flight tools for the current step to wrap up early.
// A nil channel is ignored so SoftInterrupt keeps returning a channel that
// never fires.
func WithSoftInterrupt(ctx context.Context, ch <-chan struct{}) context.Context {
	if ch == nil {
		return ctx
	}
	return context.WithValue(ctx, softInterruptContextKey{}, ch)
}

// SoftInterrupt returns the soft-interrupt channel for the current step,
// or nil when none was attached. A nil channel blocks forever in a
// select, so callers can always write `case <-tools.SoftInterrupt(ctx):`
// without a nil check: on contexts without a soft interrupt that case is
// simply never taken.
func SoftInterrupt(ctx context.Context) <-chan struct{} {
	if v, ok := ctx.Value(softInterruptContextKey{}).(<-chan struct{}); ok {
		return v
	}
	return nil
}

// SoftInterrupted reports whether a soft interrupt has already been
// requested on ctx. It never blocks.
func SoftInterrupted(ctx context.Context) bool {
	select {
	case <-SoftInterrupt(ctx):
		return true
	default:
		return false
	}
}
