package tools

import (
	"context"

	"github.com/taigrr/crush/internal/editor"
)

// editorBridgeContextKey carries the per-turn editor bridge from the
// workspace HTTP boundary (backend.SendMessage) down into tool
// callbacks. The bridge belongs to the specific client that initiated
// the turn, not to the workspace: a single workspace (and therefore a
// single shared coordinator) may be opened by several clients at once,
// each running its own editor (or none). Routing the bridge through the
// request context — mirroring SessionIDContextKey and agent.WithRunID —
// lets a shared coordinator's tools target the right client's editor
// without per-session shared state.
type editorBridgeContextKey struct{}

// WithEditorBridge returns ctx tagged with the editor bridge for the
// client that initiated the current turn. A nil bridge is normalized to
// editor.Noop so EditorBridgeFromContext never returns nil.
func WithEditorBridge(ctx context.Context, bridge editor.Bridge) context.Context {
	if bridge == nil {
		bridge = editor.Noop{}
	}
	return context.WithValue(ctx, editorBridgeContextKey{}, bridge)
}

// EditorBridgeFromContext returns the bridge set by WithEditorBridge, or
// editor.Noop{} when none was set. Safe to call on any context; the
// result is always non-nil.
func EditorBridgeFromContext(ctx context.Context) editor.Bridge {
	if v, ok := ctx.Value(editorBridgeContextKey{}).(editor.Bridge); ok && v != nil {
		return v
	}
	return editor.Noop{}
}
