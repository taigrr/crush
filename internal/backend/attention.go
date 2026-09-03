package backend

import (
	"context"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/sound"
)

// AttentionEvents returns a subscription to the backend's global,
// cross-workspace attention channel: permission/question blocked and
// resolved transitions plus agent busy/idle, from EVERY workspace, each
// tagged with the originating workspace. It powers the observe-only
// GET /v1/events stream so a client can surface a background session's
// state (busy dot, red pending indicator, red/green window border)
// without attaching to — and thereby pinning alive — that workspace.
func (b *Backend) AttentionEvents(ctx context.Context) <-chan pubsub.Event[proto.AttentionEvent] {
	return b.attention.Subscribe(ctx)
}

// publishAttention publishes one cross-workspace attention event tagged
// with the originating workspace's id and root. Safe to call with a nil
// broker or workspace (no-op) so tests without a full backend don't
// panic.
func (b *Backend) publishAttention(ws *Workspace, sessionID, toolCallID string, kind proto.AttentionKind) {
	if b == nil || b.attention == nil || ws == nil {
		return
	}
	b.attention.Publish(pubsub.CreatedEvent, proto.AttentionEvent{
		WorkspaceID:   ws.ID,
		WorkspaceRoot: ws.Path,
		SessionID:     sessionID,
		ToolCallID:    toolCallID,
		Kind:          kind,
	})
}

// startAttentionForwarder subscribes to the workspace's permission and
// question services and republishes their blocked/resolved transitions
// onto the backend's global attention channel, tagged with the
// workspace. (Agent busy/idle transitions are published directly from
// runAgent, which owns the run's lifetime.)
//
// The goroutine is bound to a dedicated context (attnCancel), NOT the
// workspace run context, so teardown can drain it deterministically:
// Workspace.Shutdown calls Permissions/Questions CancelAll (which
// publishes a resolution notification for each still-pending prompt),
// THEN cancels attnCancel. Subscription channels are buffered, so those
// just-published notifications are delivered and drained by the loop
// before its channels close — guaranteeing exactly one AttentionResolved
// per zombie prompt reaches observing clients even as the workspace goes
// away. attnWG lets Shutdown wait for that drain to finish.
func (b *Backend) startAttentionForwarder(ws *Workspace) {
	if ws == nil || ws.App == nil || b.attention == nil {
		return
	}
	if ws.Permissions == nil || ws.Questions == nil {
		return
	}

	attnCtx, cancel := context.WithCancel(context.Background())
	ws.attnCancel = cancel

	permReqs := ws.Permissions.Subscribe(attnCtx)
	permNotifs := ws.Permissions.SubscribeNotifications(attnCtx)
	qReqs := ws.Questions.Subscribe(attnCtx)
	qNotifs := ws.Questions.SubscribeNotifications(attnCtx)

	ws.attnWG.Go(func() {
		for {
			select {
			case ev, ok := <-permReqs:
				if !ok {
					permReqs = nil
					break
				}
				b.publishAttention(ws, ev.Payload.SessionID, ev.Payload.ToolCallID, proto.AttentionBlockedPermission)
				b.playSound(ws, sound.Blocked)
			case ev, ok := <-permNotifs:
				if !ok {
					permNotifs = nil
					break
				}
				// Only a terminal resolution clears the block; the
				// initial "opened" notification (neither granted nor
				// denied) is not a resolution.
				if ev.Payload.Granted || ev.Payload.Denied {
					b.publishAttention(ws, ev.Payload.SessionID, ev.Payload.ToolCallID, proto.AttentionResolved)
				}
			case ev, ok := <-qReqs:
				if !ok {
					qReqs = nil
					break
				}
				b.publishAttention(ws, ev.Payload.SessionID, ev.Payload.ToolCallID, proto.AttentionBlockedQuestion)
				b.playSound(ws, sound.Blocked)
			case ev, ok := <-qNotifs:
				if !ok {
					qNotifs = nil
					break
				}
				if ev.Payload.Answered || ev.Payload.Cancelled {
					b.publishAttention(ws, ev.Payload.SessionID, ev.Payload.ToolCallID, proto.AttentionResolved)
				}
			}
			if permReqs == nil && permNotifs == nil && qReqs == nil && qNotifs == nil {
				return
			}
		}
	})
}
