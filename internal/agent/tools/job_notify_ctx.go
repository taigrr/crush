package tools

import "context"

// JobNotifyFunc receives a notification the agent should fold into the
// session's conversation as soon as it is convenient: at the next step
// boundary when the session is busy, or at the start of the next turn when
// it is idle. It must never start a turn on its own.
type JobNotifyFunc func(sessionID, text string)

type jobNotifierContextKey struct{}

// WithJobNotifier attaches the callback tools use to report the completion
// of work they handed to the background (see bash's moved-to-background
// paths). Tools capture it from their call context before returning, so
// the notification reaches the right session even though the tool call
// itself is long over.
func WithJobNotifier(ctx context.Context, fn JobNotifyFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, jobNotifierContextKey{}, fn)
}

// JobNotifier returns the callback set by WithJobNotifier, or nil.
func JobNotifier(ctx context.Context) JobNotifyFunc {
	if fn, ok := ctx.Value(jobNotifierContextKey{}).(JobNotifyFunc); ok {
		return fn
	}
	return nil
}
