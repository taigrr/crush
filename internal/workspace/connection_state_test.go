package workspace

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// connEventRecorder collects the ConnectionEvent states pushed through
// subscribeLoop's send func, in order.
type connEventRecorder struct {
	mu     sync.Mutex
	states []ConnectionState
}

func (r *connEventRecorder) send(msg tea.Msg) {
	ev, ok := msg.(pubsub.Event[ConnectionEvent])
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, ev.Payload.State)
}

func (r *connEventRecorder) snapshot() []ConnectionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ConnectionState(nil), r.states...)
}

func newTestClientWorkspace() *ClientWorkspace {
	return NewClientWorkspace(nil, proto.Workspace{ID: "ws1"})
}

// scriptedSubscriber returns canned (chan, err) pairs in sequence for
// successive calls, so tests can simulate a failing-then-succeeding
// (or dropping) event stream without a real server.
type scriptedSubscriber struct {
	mu    sync.Mutex
	calls int
	steps []func() (<-chan any, error)
}

func (s *scriptedSubscriber) fn(ctx context.Context, _ string) (<-chan any, error) {
	s.mu.Lock()
	i := s.calls
	if i < len(s.steps) {
		s.calls++
	}
	s.mu.Unlock()
	if i >= len(s.steps) {
		// Ran out of scripted steps: keep the connection "up" until the
		// caller cancels ctx (mirrors the real SubscribeEvents, whose
		// stream closes when its context is cancelled).
		ch := make(chan any)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}
	return s.steps[i]()
}

func closedChan() (<-chan any, error) {
	ch := make(chan any)
	close(ch)
	return ch, nil
}

// TestConnectionState_NeverConnectedYetStaysConnecting verifies that
// repeated failures before any successful connection are reported as
// ConnectionStateConnecting (not Reconnecting), and that the state is
// only pushed once (no duplicate events) since it never actually
// changes.
func TestConnectionState_NeverConnectedYetStaysConnecting(t *testing.T) {
	t.Parallel()

	w := newTestClientWorkspace()
	w.SetReconnectDelayForTest(5 * time.Millisecond)

	var mu sync.Mutex
	calls := 0
	w.SetSubscribeEventsFnForTest(func(context.Context, string) (<-chan any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		// Always fails: this client never successfully connects, so
		// the state must stay Connecting no matter how many retries
		// happen.
		return nil, errors.New("boom")
	})

	rec := &connEventRecorder{}
	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(rec.send)
		close(done)
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls >= 3
	}, time.Second, time.Millisecond)

	// The state never actually changed (it started at the zero value,
	// ConnectionStateConnecting), so no ConnectionEvent should have
	// been pushed at all.
	require.Empty(t, rec.snapshot())
	require.Equal(t, ConnectionStateConnecting, w.ConnectionStateForTest())

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}
}

// TestConnectionState_DropAfterConnectReportsReconnecting verifies
// that once a connection has been established, a subsequent drop
// (stream closing without a deliberate workspace switch) is reported
// as Reconnecting rather than Connecting, and that a later successful
// reconnect reports Connected again.
func TestConnectionState_DropAfterConnectReportsReconnecting(t *testing.T) {
	t.Parallel()

	w := newTestClientWorkspace()
	w.SetReconnectDelayForTest(5 * time.Millisecond)

	reconnected := make(chan struct{})
	sub := &scriptedSubscriber{steps: []func() (<-chan any, error){
		closedChan, // first "connection": succeeds, then immediately ends.
		func() (<-chan any, error) {
			defer close(reconnected)
			return closedChan()
		},
	}}
	w.SetSubscribeEventsFnForTest(sub.fn)

	rec := &connEventRecorder{}
	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(rec.send)
		close(done)
	}()

	select {
	case <-reconnected:
	case <-time.After(time.Second):
		t.Fatal("did not observe reconnect attempt")
	}

	require.Eventually(t, func() bool {
		states := rec.snapshot()
		return len(states) >= 3
	}, time.Second, time.Millisecond)

	states := rec.snapshot()
	require.Equal(t, []ConnectionState{
		ConnectionStateConnected,
		ConnectionStateReconnecting,
		ConnectionStateConnected,
	}, states[:3])

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}
}

// TestConnectionState_WorkspaceSwitchDoesNotReportReconnecting
// verifies that a deliberate SwitchWorkspace-triggered reconnect
// (switchRequested) does not spuriously report a Reconnecting state,
// since it is not a connection drop.
func TestConnectionState_WorkspaceSwitchDoesNotReportReconnecting(t *testing.T) {
	t.Parallel()

	w := newTestClientWorkspace()
	w.SetReconnectDelayForTest(5 * time.Millisecond)

	var callCount int
	var mu sync.Mutex
	w.SetSubscribeEventsFnForTest(func(ctx context.Context, _ string) (<-chan any, error) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()
		if n == 1 {
			// First connection: mark the close as a deliberate switch
			// (as SwitchWorkspace would) and close immediately.
			w.subMu.Lock()
			w.switchRequested = true
			w.subMu.Unlock()
			ch := make(chan any)
			close(ch)
			return ch, nil
		}
		// Second connection: park until the loop is stopped (which
		// cancels ctx), mirroring the real SubscribeEvents.
		ch := make(chan any)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	})

	rec := &connEventRecorder{}
	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(rec.send)
		close(done)
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return callCount >= 2
	}, time.Second, time.Millisecond)

	states := rec.snapshot()
	require.Equal(t, []ConnectionState{ConnectionStateConnected}, states)

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}
}

// TestConnectionState_SwitchResetsBackoff verifies that a workspace
// switch which aborts an in-progress backoff sleep resets the backoff
// to the base delay, rather than carrying over the grown delay from
// the preceding drop/failed-reconnect sequence. Without the reset, a
// post-switch connect that fails immediately would make the user wait
// out the stale (grown) delay.
func TestConnectionState_SwitchResetsBackoff(t *testing.T) {
	t.Parallel()

	const base = 40 * time.Millisecond
	w := newTestClientWorkspace()
	w.SetReconnectDelayForTest(base)

	// First connect succeeds then drops; every subsequent connect
	// fails, so the backoff grows on each retry until the switch.
	var subMu sync.Mutex
	subCalls := 0
	w.SetSubscribeEventsFnForTest(func(context.Context, string) (<-chan any, error) {
		subMu.Lock()
		subCalls++
		first := subCalls == 1
		subMu.Unlock()
		if first {
			return closedChan()
		}
		return nil, errors.New("still down")
	})

	var obsMu sync.Mutex
	var observed []time.Duration
	var switchOnce sync.Once
	switched := make(chan struct{})
	w.SetBackoffObserverForTest(func(d time.Duration) {
		obsMu.Lock()
		observed = append(observed, d)
		obsMu.Unlock()
		// Once the backoff has clearly grown past the base (>= 4x),
		// trigger a switch to abort this sleep. The switch must reset
		// the backoff so the NEXT observed delay is the base again.
		if d >= 4*base {
			switchOnce.Do(func() {
				w.RequestSwitchForTest()
				close(switched)
			})
		}
	})

	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(func(tea.Msg) {})
		close(done)
	}()

	select {
	case <-switched:
	case <-time.After(5 * time.Second):
		t.Fatal("backoff never grew enough to trigger the switch")
	}

	// After the switch aborts the grown sleep, the loop reconnects
	// (fails again) and must back off starting from the base delay.
	require.Eventually(t, func() bool {
		obsMu.Lock()
		defer obsMu.Unlock()
		// Find a grown value, then a base value strictly after it.
		grownAt := -1
		for i, d := range observed {
			if d >= 4*base {
				grownAt = i
				break
			}
		}
		if grownAt < 0 {
			return false
		}
		return slices.Contains(observed[grownAt+1:], base)
	}, 5*time.Second, 5*time.Millisecond, "backoff was not reset to base after a switch")

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}
}

// TestConnectionState_SwitchDuringConnectResetsBackoff covers the
// sibling of SwitchResetsBackoff: a switch that lands DURING a
// connect/subscribe attempt (not during the backoff sleep). It must
// also reset the backoff to base, so the fresh workspace's first
// failed connect doesn't inherit the grown delay from prior drops.
func TestConnectionState_SwitchDuringConnectResetsBackoff(t *testing.T) {
	t.Parallel()

	const base = 40 * time.Millisecond
	w := newTestClientWorkspace()
	w.SetReconnectDelayForTest(base)

	// Track the largest backoff delay observed so far, so the
	// subscriber can trigger an in-connect switch only once the backoff
	// has clearly grown.
	var obsMu sync.Mutex
	var observed []time.Duration
	grown := make(chan struct{})
	var grownOnce sync.Once
	w.SetBackoffObserverForTest(func(d time.Duration) {
		obsMu.Lock()
		observed = append(observed, d)
		obsMu.Unlock()
		if d >= 4*base {
			grownOnce.Do(func() { close(grown) })
		}
	})

	// Every connect fails, so the backoff grows. Once it has grown,
	// the next connect attempt triggers a switch *from inside the
	// connect* (before returning its error), exercising the
	// prepReconnect reconnectSwitch (connect-window) path rather than
	// the waitBackoff sleep-window path.
	var switchedOnce sync.Once
	switchDone := make(chan struct{})
	w.SetSubscribeEventsFnForTest(func(context.Context, string) (<-chan any, error) {
		select {
		case <-grown:
			switchedOnce.Do(func() {
				w.RequestSwitchForTest()
				close(switchDone)
			})
		default:
		}
		return nil, errors.New("still down")
	})

	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(func(tea.Msg) {})
		close(done)
	}()

	select {
	case <-switchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("backoff never grew enough to trigger the in-connect switch")
	}

	// Record how many delays had been observed at the moment of the
	// switch; the switch must reset backoff so a delay recorded strictly
	// afterwards is the base again.
	obsMu.Lock()
	switchIdx := len(observed)
	obsMu.Unlock()

	require.Eventually(t, func() bool {
		obsMu.Lock()
		defer obsMu.Unlock()
		return slices.Contains(observed[switchIdx:], base)
	}, 5*time.Second, 5*time.Millisecond, "backoff was not reset to base after an in-connect switch")

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}
}

func TestConnectionState_String(t *testing.T) {
	t.Parallel()
	require.Equal(t, "connecting", ConnectionStateConnecting.String())
	require.Equal(t, "connected", ConnectionStateConnected.String())
	require.Equal(t, "reconnecting", ConnectionStateReconnecting.String())
}

// liveThenDoneSub returns, for each call, a channel that stays open
// until the per-call context is cancelled (mirroring the real
// SubscribeEvents, whose stream closes when its context is cancelled).
// It signals connectedOnce after the first successful subscribe so
// tests can synchronize on "we are connected now".
type liveThenDoneSub struct {
	mu        sync.Mutex
	calls     int
	connected chan struct{}
}

func (s *liveThenDoneSub) fn(ctx context.Context, _ string) (<-chan any, error) {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	ch := make(chan any)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	if first && s.connected != nil {
		close(s.connected)
	}
	return ch, nil
}

// TestConnectionState_SwitchAbortsBackoff verifies that a workspace
// switch during an in-progress reconnect backoff aborts the sleep
// immediately, so the loop reconnects to the new workspace without
// waiting out the (here deliberately long) backoff timer.
func TestConnectionState_SwitchAbortsBackoff(t *testing.T) {
	t.Parallel()

	w := newTestClientWorkspace()
	// A long backoff: if the switch did NOT abort it, the reconnect
	// (and thus the Connected assertion below) would take this long
	// and the test's short Eventually window would fail.
	w.SetReconnectDelayForTest(30 * time.Second)

	var mu sync.Mutex
	calls := 0
	dropped := make(chan struct{})
	w.SetSubscribeEventsFnForTest(func(ctx context.Context, _ string) (<-chan any, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			// First connect succeeds then immediately drops, sending
			// the loop into its (long) reconnect backoff.
			defer close(dropped)
			return closedChan()
		}
		// After the switch aborts the backoff, this reconnect parks
		// (stays connected until ctx is cancelled).
		ch := make(chan any)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	})

	rec := &connEventRecorder{}
	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(rec.send)
		close(done)
	}()

	<-dropped
	// Wait until the loop has actually entered the backoff (reported
	// Reconnecting) so the switch races the sleep, not the connect.
	require.Eventually(t, func() bool {
		return w.ConnectionStateForTest() == ConnectionStateReconnecting
	}, 2*time.Second, time.Millisecond)

	w.RequestSwitchForTest()

	// The switch must abort the 30s backoff and let the loop reconnect
	// promptly.
	require.Eventually(t, func() bool {
		return w.ConnectionStateForTest() == ConnectionStateConnected
	}, 2*time.Second, time.Millisecond, "switch did not abort the backoff")

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}
}

// TestConnectionState_DropDuringShutdownDoesNotFlashReconnecting
// verifies that when a live stream ends because of a deliberate
// Shutdown (stopped closed + ctx cancelled), the loop exits without
// emitting a spurious Reconnecting state/event.
func TestConnectionState_DropDuringShutdownDoesNotFlashReconnecting(t *testing.T) {
	t.Parallel()

	w := newTestClientWorkspace()
	w.SetReconnectDelayForTest(5 * time.Millisecond)
	sub := &liveThenDoneSub{connected: make(chan struct{})}
	w.SetSubscribeEventsFnForTest(sub.fn)

	rec := &connEventRecorder{}
	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(rec.send)
		close(done)
	}()

	// Wait until connected, then stop: the stop closes the stream,
	// consumeEvents returns, and prepReconnect must observe stopped and
	// exit rather than flashing Reconnecting.
	<-sub.connected
	require.Eventually(t, func() bool {
		return w.ConnectionStateForTest() == ConnectionStateConnected
	}, time.Second, time.Millisecond)

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}

	require.Equal(t, []ConnectionState{ConnectionStateConnected}, rec.snapshot(),
		"a deliberate shutdown must not flash Reconnecting")
	require.Equal(t, ConnectionStateConnected, w.ConnectionStateForTest())
}

// TestConnectionState_DropDuringSwitchDoesNotFlashReconnecting
// verifies that when a live stream ends because of a deliberate
// workspace switch (switchRequested set + ctx cancelled), the loop
// reconnects without emitting a spurious Reconnecting state.
func TestConnectionState_DropDuringSwitchDoesNotFlashReconnecting(t *testing.T) {
	t.Parallel()

	w := newTestClientWorkspace()
	w.SetReconnectDelayForTest(5 * time.Millisecond)
	sub := &liveThenDoneSub{connected: make(chan struct{})}
	w.SetSubscribeEventsFnForTest(sub.fn)

	rec := &connEventRecorder{}
	done := make(chan struct{})
	go func() {
		w.SubscribeLoopForTest(rec.send)
		close(done)
	}()

	<-sub.connected
	require.Eventually(t, func() bool {
		return w.ConnectionStateForTest() == ConnectionStateConnected
	}, time.Second, time.Millisecond)

	// Switch while connected: closes the current stream and asks the
	// loop to reconnect. It must NOT report Reconnecting for this.
	w.RequestSwitchForTest()

	require.Eventually(t, func() bool {
		sub.mu.Lock()
		defer sub.mu.Unlock()
		return sub.calls >= 2 // reconnected to the "new" workspace
	}, time.Second, time.Millisecond)

	require.Eventually(t, func() bool {
		return w.ConnectionStateForTest() == ConnectionStateConnected
	}, time.Second, time.Millisecond)

	for _, s := range rec.snapshot() {
		require.NotEqual(t, ConnectionStateReconnecting, s,
			"a deliberate switch must not flash Reconnecting")
	}

	w.StopSubscribeLoopForTest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribeLoop did not exit after Shutdown")
	}
}
