package tools

import (
	"context"
	"sync"
)

// backgroundRequests is the process-wide registry of tool calls that can
// be moved to the background on request. A tool that supports it (bash
// while it waits on a foreground command, job_output while it waits for
// a job) registers its tool-call ID for the duration of the wait and
// selects on the returned channel alongside the step-wide soft interrupt.
// The UI's "background this" action resolves to RequestBackground with
// that ID, which fires only that one tool call — unlike a soft interrupt,
// which wakes every opted-in tool in the step.
//
// It is global (like shell.GetBackgroundShellManager) because tool-call
// IDs are unique per provider response and the request arrives over HTTP
// with no handle on the tool's context; the session ID is recorded so a
// request can be scoped to the session it came from.
var backgroundRequests = struct {
	mu    sync.Mutex
	calls map[string]*backgroundRequest
}{calls: make(map[string]*backgroundRequest)}

type backgroundRequest struct {
	sessionID string
	ch        chan struct{}
	fired     bool
}

// RegisterBackgroundable announces that the tool call identified by
// callID (running in the session carried by ctx) can be moved to the
// background. It returns the channel that RequestBackground closes and a
// release func the tool must call when its wait ends, whichever way it
// ended. A second registration for the same ID replaces the first.
func RegisterBackgroundable(ctx context.Context, callID string) (<-chan struct{}, func()) {
	if callID == "" {
		return nil, func() {}
	}
	req := &backgroundRequest{sessionID: GetSessionFromContext(ctx), ch: make(chan struct{})}
	backgroundRequests.mu.Lock()
	backgroundRequests.calls[callID] = req
	backgroundRequests.mu.Unlock()
	return req.ch, func() {
		backgroundRequests.mu.Lock()
		if cur, ok := backgroundRequests.calls[callID]; ok && cur == req {
			delete(backgroundRequests.calls, callID)
		}
		backgroundRequests.mu.Unlock()
	}
}

// RequestBackground asks the registered tool call callID to move its work
// to the background. sessionID, when non-empty, must match the session
// the call registered under. It reports whether a matching, not yet
// signalled registration was found; false means the call already
// finished, never registered, or belongs to another session.
func RequestBackground(sessionID, callID string) bool {
	backgroundRequests.mu.Lock()
	defer backgroundRequests.mu.Unlock()
	req, ok := backgroundRequests.calls[callID]
	if !ok || req.fired {
		return false
	}
	if sessionID != "" && req.sessionID != "" && req.sessionID != sessionID {
		return false
	}
	req.fired = true
	close(req.ch)
	return true
}

// BackgroundableToolCalls lists the tool-call IDs currently registered for
// sessionID (all sessions when empty). Intended for UIs that want to show
// a "background this" hint only on calls that can honor it.
func BackgroundableToolCalls(sessionID string) []string {
	backgroundRequests.mu.Lock()
	defer backgroundRequests.mu.Unlock()
	var ids []string
	for id, req := range backgroundRequests.calls {
		if req.fired {
			continue
		}
		if sessionID != "" && req.sessionID != sessionID {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}
