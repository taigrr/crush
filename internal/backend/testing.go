package backend

import "context"

// InsertWorkspaceForTest registers ws with b under its current ID and
// path. It is intended for tests in other packages that need to drive
// HTTP handlers against a synthetic workspace without booting a real
// app.App. Production code should go through CreateWorkspace.
//
// If the workspace has no run context yet it is derived from the
// backend context (falling back to context.Background), mirroring the
// initialization CreateWorkspace performs, so dispatched agent runs
// have a non-nil ws.ctx.
func InsertWorkspaceForTest(b *Backend, ws *Workspace) {
	if ws.resolvedPath == "" {
		ws.resolvedPath = ws.Path
	}
	if ws.clients == nil {
		ws.clients = make(map[string]*clientState)
	}
	if ws.ctx == nil {
		parent := b.ctx
		if parent == nil {
			parent = context.Background()
		}
		ws.ctx, ws.cancel = context.WithCancel(parent)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.workspaces.Set(ws.ID, ws)
	if ws.resolvedPath != "" {
		b.pathIndex[ws.resolvedPath] = ws.ID
	}
}

// RegisterClientForTesting installs a creation hold for clientID on
// ws using the backend's normal registerClient path. Intended for
// tests in other packages that need to drive a hold-only client
// (streams == 0) without booting a real CreateWorkspace flow.
func RegisterClientForTesting(b *Backend, ws *Workspace, clientID string) error {
	if _, err := validateClientID(clientID); err != nil {
		return err
	}
	b.registerClient(ws, clientID, nil, "")
	return nil
}

// SetWorkspaceShutdownFnForTest overrides the workspace teardown
// callback. Useful for tests in other packages that drive synthetic
// workspaces (where the embedded [app.App] is incomplete) through
// detach paths that would otherwise crash inside App.Shutdown.
func SetWorkspaceShutdownFnForTest(ws *Workspace, fn func()) {
	ws.shutdownFn = fn
}

// WorkspaceLiveStreamCountForTest returns the number of clients on ws
// that have at least one live SSE stream. Used by integration tests
// in other packages to wait for SSE attaches before publishing events.
func WorkspaceLiveStreamCountForTest(ws *Workspace) int {
	ws.clientsMu.Lock()
	defer ws.clientsMu.Unlock()
	n := 0
	for _, cs := range ws.clients {
		if cs.streams > 0 {
			n++
		}
	}
	return n
}

// SetWorkspaceBusySessionsForTest overrides how ws reports its in-flight
// sessions, so tests in other packages can simulate active runs for a
// synthetic workspace without a real coordinator. The boolean busy
// predicate is derived from it.
func SetWorkspaceBusySessionsForTest(ws *Workspace, fn func() []string) {
	ws.busySessionsFn = fn
	ws.busyFn = func() bool { return len(fn()) > 0 }
}

// SignalDrainForTest wakes the drain waiter, standing in for the
// run-completion hook that runAgent fires in production.
func SignalDrainForTest(b *Backend) { b.signalDrain() }

// RehydrateQueueForTest replays the journaled queue for ws through the
// backend's normal rehydration path. Tests in other packages use it
// after swapping in a scripted coordinator, since CreateWorkspace only
// rehydrates when the workspace's config yields a real coordinator.
func RehydrateQueueForTest(b *Backend, ws *Workspace) { b.rehydrateQueue(ws) }
