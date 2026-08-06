package agent

import (
	"context"

	"github.com/taigrr/crush/internal/message"
)

// swarmPartsContextKey carries [message.SwarmMessage] parts through
// [Coordinator.Run] / [Coordinator.RunAccepted] onto the eventual
// [SessionAgentCall.SwarmParts]. Threading them via context avoids
// changing the Run signature (which is a stable interface used by
// tests, backend dispatch, and the app-workspace client).
type swarmPartsContextKey struct{}

// WithSwarmParts tags ctx with the SwarmMessage parts to attach to
// the next user message the coordinator creates for the current
// dispatch. Only the first Run consumes it; subsequent goal-driven
// continuations still receive plain text prompts.
func WithSwarmParts(ctx context.Context, parts []message.SwarmMessage) context.Context {
	if len(parts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, swarmPartsContextKey{}, parts)
}

// SwarmPartsFromContext returns the parts set by [WithSwarmParts] or
// nil when none were set. Safe to call on any context.
func SwarmPartsFromContext(ctx context.Context) []message.SwarmMessage {
	if v, ok := ctx.Value(swarmPartsContextKey{}).([]message.SwarmMessage); ok {
		return v
	}
	return nil
}
