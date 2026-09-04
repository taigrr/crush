package agent

import "context"

// steerContextKey carries the "this is a mid-turn steer" flag from the
// workspace HTTP boundary (backend.runAgent) down into coordinator.run,
// where it is copied onto SessionAgentCall.Steer. Threading it via
// context mirrors WithRunID and WithSwarmParts so the Coordinator.Run
// signature stays stable.
type steerContextKey struct{}

// WithSteer tags ctx so the next Run dispatched on it is treated as a
// steer: if the session is busy the call is queued as usual and the
// session's soft interrupt is raised so opted-in tools wrap up the
// current step early (see tools.SoftInterrupt). Idle sessions run the
// prompt as a normal turn.
func WithSteer(ctx context.Context) context.Context {
	return context.WithValue(ctx, steerContextKey{}, true)
}

// SteerFromContext reports whether ctx was tagged by [WithSteer]. Safe
// to call on any context.
func SteerFromContext(ctx context.Context) bool {
	v, ok := ctx.Value(steerContextKey{}).(bool)
	return ok && v
}
