package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type fakeCapture struct {
	stopped atomic.Bool
	stream  *pcmStream
}

func newFakeCapture() *fakeCapture {
	return &fakeCapture{stream: newPCMStream(captureBuffer)}
}

func (f *fakeCapture) Stop() {
	f.stopped.Store(true)
	f.stream.close()
}

func fakeDeps() deps {
	return deps{
		tls: insecureTLS,
		capture: func(uint32, string) (CaptureHandle, <-chan []byte, error) {
			c := newFakeCapture()
			return c, c.stream.C(), nil
		},
	}
}

// releaseTestServer accepts audio, and when it sees `audio.done` replies
// with a final transcript.done.
func releaseTestServer(t *testing.T) (wsBase string, gotDone *atomic.Bool) {
	t.Helper()
	gotDone = &atomic.Bool{}
	up := websocket.Upgrader{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.created"}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.partial","text":"hello wor","is_final":false}`))
		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if typ != websocket.TextMessage {
				continue
			}
			var msg struct{ Type string }
			_ = json.Unmarshal(data, &msg)
			if msg.Type == "audio.done" {
				gotDone.Store(true)
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.done","text":"hello world"}`))
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "wss" + strings.TrimPrefix(srv.URL, "https"), gotDone
}

func TestReleaseDrainsFinalTranscriptThenStops(t *testing.T) {
	wsBase, gotDone := releaseTestServer(t)
	cfg := insecureTestConfig(wsBase)

	capture := newFakeCapture()
	var openedDevice atomic.Value
	d := deps{tls: insecureTLS, capture: func(_ uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
		openedDevice.Store(deviceID)
		return capture, capture.stream.C(), nil
	}}
	cfg.InputDevice = "mic-42"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmdCh := make(chan Command, 4)
	eventCh := make(chan Event, 16)
	bearerFn := func(context.Context, bool) (string, error) { return "tok", nil }
	go runPipeline(ctx, cfg, bearerFn, cmdCh, eventCh, d)

	cmdCh <- Press(1)
	ev := recvEvent(t, eventCh)
	require.Equal(t, EventInterim, ev.Kind)
	require.Equal(t, "hello wor", ev.Text)

	cmdCh <- Release()
	ev = recvEvent(t, eventCh)
	require.Equal(t, EventFinal, ev.Kind)
	require.Equal(t, "hello world", ev.Text)
	ev = recvEvent(t, eventCh)
	require.Equal(t, EventStopped, ev.Kind)
	require.True(t, gotDone.Load(), "audio.done must be sent on release")
	require.True(t, capture.stopped.Load(), "microphone must be released")
	require.Equal(t, "mic-42", openedDevice.Load())
	cmdCh <- Shutdown()
}

func TestPressWhileActiveCancelsAndReportsOldTurn(t *testing.T) {
	wsBase, _ := releaseTestServer(t)
	cfg := insecureTestConfig(wsBase)
	d := fakeDeps()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmdCh := make(chan Command, 4)
	eventCh := make(chan Event, 16)
	go runPipeline(ctx, cfg, func(context.Context, bool) (string, error) { return "tok", nil }, cmdCh, eventCh, d)

	cmdCh <- Press(1)
	require.Equal(t, EventInterim, recvEvent(t, eventCh).Kind)
	cmdCh <- Press(2)
	// The cancelled turn reports Stopped (so the UI releases its range)
	// and the new turn starts; their relative order is not guaranteed.
	var seen []Event
	for len(seen) < 2 {
		seen = append(seen, recvEvent(t, eventCh))
	}
	require.Contains(t, seen, Event{Kind: EventStopped, Turn: 1})
	require.Contains(t, seen, Event{Kind: EventInterim, Turn: 2, Text: "hello wor"})
	cmdCh <- Release()
	require.Equal(t, Event{Kind: EventFinal, Turn: 2, Text: "hello world"}, recvEvent(t, eventCh))
	require.Equal(t, Event{Kind: EventStopped, Turn: 2}, recvEvent(t, eventCh))
	select {
	case ev := <-eventCh:
		t.Fatalf("unexpected extra event %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
	cmdCh <- Shutdown()
}

func recvEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestPressDuringDrainWaitsForFinal(t *testing.T) {
	wsBase, _ := releaseTestServer(t)
	cfg := insecureTestConfig(wsBase)
	d := fakeDeps()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmdCh := make(chan Command, 4)
	eventCh := make(chan Event, 16)
	go runPipeline(ctx, cfg, func(context.Context, bool) (string, error) { return "tok", nil }, cmdCh, eventCh, d)

	cmdCh <- Press(1)
	require.Equal(t, EventInterim, recvEvent(t, eventCh).Kind)
	cmdCh <- Release()
	cmdCh <- Press(2)
	evs := []Event{recvEvent(t, eventCh), recvEvent(t, eventCh), recvEvent(t, eventCh)}
	require.Equal(t, []EventKind{EventFinal, EventStopped, EventInterim}, []EventKind{evs[0].Kind, evs[1].Kind, evs[2].Kind}, "first turn completes before the second starts")
	require.Equal(t, []int{1, 1, 2}, []int{evs[0].Turn, evs[1].Turn, evs[2].Turn})
	cmdCh <- Release()
	require.Equal(t, EventFinal, recvEvent(t, eventCh).Kind)
	require.Equal(t, EventStopped, recvEvent(t, eventCh).Kind)
	cmdCh <- Shutdown()
}

func TestShutdownDuringDrainIsImmediate(t *testing.T) {
	wsBase, _ := releaseTestServer(t)
	cfg := insecureTestConfig(wsBase)
	d := fakeDeps()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmdCh := make(chan Command, 4)
	eventCh := make(chan Event, 16)
	exited := make(chan struct{})
	go func() {
		runPipeline(ctx, cfg, func(context.Context, bool) (string, error) { return "tok", nil }, cmdCh, eventCh, d)
		close(exited)
	}()

	cmdCh <- Press(1)
	require.Equal(t, EventInterim, recvEvent(t, eventCh).Kind)
	cmdCh <- Release()
	cmdCh <- Press(2)
	// Release the pending press before it starts: it is dropped but still
	// reported as stopped, and shutdown must not wait on the drain.
	cmdCh <- Release()
	cmdCh <- Shutdown()
	var kinds []Event
	for range 3 {
		select {
		case ev := <-eventCh:
			kinds = append(kinds, ev)
		case <-time.After(2 * time.Second):
		}
	}
	require.Contains(t, kinds, Event{Kind: EventStopped, Turn: 2}, "dropped pending press must report EventStopped")
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not shut down promptly while draining")
	}
}

// noisyCapture keeps pushing PCM after Stop, like a real backend whose
// callback fires once more or whose pipe still has bytes in flight.
type noisyCapture struct {
	stream *pcmStream
	quit   chan struct{}
}

func (c *noisyCapture) Stop() {
	select {
	case <-c.quit:
	default:
		close(c.quit)
	}
}

func startNoisyCapture() *noisyCapture {
	c := &noisyCapture{stream: newPCMStream(captureBuffer), quit: make(chan struct{})}
	go func() {
		chunk := make([]byte, 2048)
		for {
			c.stream.push(chunk)
			select {
			case <-c.quit:
				// Stragglers after Stop, then the stream ends like a pipe
				// draining after the recorder is killed.
				for range 8 {
					c.stream.push(chunk)
				}
				c.stream.close()
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return c
}

func TestReleaseWithLivePCMDoesNotPanic(t *testing.T) {
	wsBase, gotDone := releaseTestServer(t)
	cfg := insecureTestConfig(wsBase)
	d := deps{tls: insecureTLS, capture: func(uint32, string) (CaptureHandle, <-chan []byte, error) {
		c := startNoisyCapture()
		return c, c.stream.C(), nil
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmdCh := make(chan Command, 4)
	eventCh := make(chan Event, 64)
	go runPipeline(ctx, cfg, func(context.Context, bool) (string, error) { return "tok", nil }, cmdCh, eventCh, d)

	for range 5 {
		cmdCh <- Press(1)
		require.Equal(t, EventInterim, recvEvent(t, eventCh).Kind)
		time.Sleep(20 * time.Millisecond)
		cmdCh <- Release()
		require.Equal(t, EventFinal, recvEvent(t, eventCh).Kind)
		require.Equal(t, EventStopped, recvEvent(t, eventCh).Kind)
	}
	require.True(t, gotDone.Load())
	cmdCh <- Shutdown()
}

// stalledServer accepts the socket, sends transcript.created, then never
// reads again so the client's writes back up.
func stalledServer(t *testing.T) (wsBase string) {
	t.Helper()
	up := websocket.Upgrader{}
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.created"}`))
		<-block
	}))
	t.Cleanup(srv.Close)
	return "wss" + strings.TrimPrefix(srv.URL, "https")
}

func TestReleaseWithStalledSocketStillStops(t *testing.T) {
	wsBase := stalledServer(t)
	cfg := insecureTestConfig(wsBase)
	d := deps{tls: insecureTLS, capture: func(uint32, string) (CaptureHandle, <-chan []byte, error) {
		c := startNoisyCapture()
		return c, c.stream.C(), nil
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmdCh := make(chan Command, 4)
	eventCh := make(chan Event, 64)
	go runPipeline(ctx, cfg, func(context.Context, bool) (string, error) { return "tok", nil }, cmdCh, eventCh, d)

	cmdCh <- Press(1)
	// Let far more than 64 chunks pile up against a peer that never reads.
	time.Sleep(300 * time.Millisecond)
	cmdCh <- Release()
	deadline := time.After(drainTimeout + 3*time.Second)
	for {
		select {
		case ev := <-eventCh:
			if ev.Kind == EventStopped || ev.Kind == EventError {
				cmdCh <- Shutdown()
				return
			}
		case <-deadline:
			t.Fatal("release never settled against a stalled socket")
		}
	}
}
