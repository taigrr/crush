package workspace

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ConsumeEventsForTest runs the event-handling loop on the given
// channel, invoking send for translated domain messages and refreshing
// the cached workspace snapshot on ConfigChanged. Exposed for
// cross-package integration tests that cannot rely on a real
// *tea.Program. It returns when evc is closed.
func (w *ClientWorkspace) ConsumeEventsForTest(evc <-chan any, send func(tea.Msg)) {
	w.consumeEvents(evc, send)
}

// SetSubscribeEventsFnForTest overrides the function used to open the
// event stream, letting tests simulate connection failures/drops
// without a real server.
func (w *ClientWorkspace) SetSubscribeEventsFnForTest(fn func(ctx context.Context, id string) (<-chan any, error)) {
	w.subscribeEventsFn = fn
}

// SubscribeLoopForTest runs the reconnect loop synchronously using a
// plain send func instead of a real *tea.Program. It returns once the
// workspace is shut down (via Shutdown, which closes w.stopped).
// Exposed so tests can exercise the retry/backoff state machine
// without standing up a *tea.Program.
func (w *ClientWorkspace) SubscribeLoopForTest(send func(tea.Msg)) {
	w.subscribeLoop(send)
}

// ConnectionStateForTest returns the raw ConnectionState without going
// through the exported (interface) accessor, for symmetry with other
// *ForTest helpers.
func (w *ClientWorkspace) ConnectionStateForTest() ConnectionState {
	return w.ConnectionState()
}

// SetReconnectDelayForTest overrides the initial reconnect backoff
// delay so tests don't have to sleep through the real multi-second
// default.
func (w *ClientWorkspace) SetReconnectDelayForTest(d time.Duration) {
	w.reconnectDelayOverride = d
}

// StoppedForTest exposes the shutdown signal channel so tests can
// assert the reconnect loop actually exits after Shutdown.
func (w *ClientWorkspace) StoppedForTest() <-chan struct{} {
	return w.stopped
}

// StopSubscribeLoopForTest stops the reconnect loop the same way
// Shutdown does (closing the stopped signal and cancelling the
// current subscription context) without also making a real
// DeleteWorkspace call against a (possibly nil, in tests) client.
func (w *ClientWorkspace) StopSubscribeLoopForTest() {
	w.stopSubscribeLoop()
}

// RequestSwitchForTest triggers the same reconnect signal
// SwitchWorkspace uses (mark switchRequested, cancel the live stream,
// pulse reconnectNow) without needing a real *client.Client to attach
// a new workspace. Lets tests exercise switch-during-connection and
// switch-aborts-backoff behavior.
func (w *ClientWorkspace) RequestSwitchForTest() {
	w.requestSwitch()
}

// SetBackoffObserverForTest installs a hook called with each delay
// waitBackoff is about to sleep on, letting tests assert the backoff
// progression (e.g. that a switch resets it to the base delay).
func (w *ClientWorkspace) SetBackoffObserverForTest(fn func(time.Duration)) {
	w.backoffObserver = fn
}
