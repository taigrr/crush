package tools

import (
	"context"

	"github.com/taigrr/fantasy"
)

// ContextAvailability is implemented by tools that may exclude
// themselves from a given turn based on the request context. The agent
// calls AvailableInContext when assembling the tool set for each turn;
// tools that do not implement this interface are always advertised.
type ContextAvailability interface {
	AvailableInContext(ctx context.Context) bool
}

// contextGated wraps an AgentTool with a per-turn availability
// predicate. It embeds the wrapped tool so all AgentTool methods
// (Info, SetProviderOptions, Run, ...) forward unchanged.
type contextGated struct {
	fantasy.AgentTool
	available func(context.Context) bool
}

func (c contextGated) AvailableInContext(ctx context.Context) bool {
	return c.available(ctx)
}

// WithContextGate returns tool wrapped so it is only advertised to the
// model for turns where available(ctx) reports true. Use it for tools
// whose backing capability is per-turn rather than fixed for the
// coordinator's lifetime (e.g. editor tools that depend on the
// initiating client having an attached editor).
func WithContextGate(tool fantasy.AgentTool, available func(context.Context) bool) fantasy.AgentTool {
	return contextGated{AgentTool: tool, available: available}
}

// FilterAvailableTools returns ts with any ContextAvailability tool that
// opts out of ctx removed. Tools that do not implement
// ContextAvailability are always retained. The input slice is not
// modified.
func FilterAvailableTools(ctx context.Context, ts []fantasy.AgentTool) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, 0, len(ts))
	for _, t := range ts {
		if g, ok := t.(ContextAvailability); ok && !g.AvailableInContext(ctx) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// EditorAttached reports whether the turn's initiating client has a live
// editor bridge. It is the gate predicate for editor-only tools.
func EditorAttached(ctx context.Context) bool {
	return EditorBridgeFromContext(ctx).Available()
}
