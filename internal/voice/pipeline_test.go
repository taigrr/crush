package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func releaseTestServer(t *testing.T, stall bool) (wsBase string) {
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
		if stall {
			<-block
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.partial","text":"hello wor"}`))
		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct{ Type string }
			if typ == websocket.TextMessage && json.Unmarshal(data, &msg) == nil && msg.Type == "audio.done" {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.done","text":"hello world"}`))
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "wss" + strings.TrimPrefix(srv.URL, "https")
}

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

func noisyDeps() deps {
	return deps{tls: insecureTLS, capture: func(uint32, string) (CaptureHandle, <-chan []byte, error) {
		c := &noisyCapture{stream: newPCMStream(captureBuffer), quit: make(chan struct{})}
		go func() {
			chunk := make([]byte, 2048)
			for {
				c.stream.push(chunk)
				select {
				case <-c.quit:
					for range 8 {
						c.stream.push(chunk)
					}
					c.stream.close()
					return
				case <-time.After(time.Millisecond):
				}
			}
		}()
		return c, c.stream.C(), nil
	}}
}

func startPipeline(t *testing.T, wsBase string, d deps) (chan<- Command, <-chan Event) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmdCh := make(chan Command, 8)
	eventCh := make(chan Event, 64)
	go runPipeline(ctx, Config{APIBase: wsBase}.Normalize(), func(context.Context, bool) (string, error) { return "tok", nil }, cmdCh, eventCh, d)
	return cmdCh, eventCh
}

func recvEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(drainTimeout + 3*time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func kinds(evs ...Event) []EventKind {
	out := make([]EventKind, len(evs))
	for i, ev := range evs {
		out[i] = ev.Kind
	}
	return out
}

func TestReleaseDrainsFinalThenStopsAcrossTurns(t *testing.T) {
	cmdCh, eventCh := startPipeline(t, releaseTestServer(t, false), noisyDeps())

	for range 5 {
		cmdCh <- Press(1)
		require.Equal(t, EventInterim, recvEvent(t, eventCh).Kind)
		time.Sleep(20 * time.Millisecond)
		cmdCh <- Release()
		require.Equal(t, Event{Kind: EventFinal, Turn: 1, Text: "hello world"}, recvEvent(t, eventCh))
		require.Equal(t, Event{Kind: EventStopped, Turn: 1}, recvEvent(t, eventCh))
	}

	cmdCh <- Press(2)
	require.Equal(t, EventInterim, recvEvent(t, eventCh).Kind)
	cmdCh <- Release()
	cmdCh <- Press(3)
	evs := []Event{recvEvent(t, eventCh), recvEvent(t, eventCh), recvEvent(t, eventCh)}
	require.Equal(t, []EventKind{EventFinal, EventStopped, EventInterim}, kinds(evs...))
	require.Equal(t, []int{2, 2, 3}, []int{evs[0].Turn, evs[1].Turn, evs[2].Turn})
	cmdCh <- Release()
	cmdCh <- Press(4)
	cmdCh <- Release()
	evs = []Event{recvEvent(t, eventCh), recvEvent(t, eventCh), recvEvent(t, eventCh)}
	require.Contains(t, evs, Event{Kind: EventFinal, Turn: 3, Text: "hello world"})
	require.Contains(t, evs, Event{Kind: EventStopped, Turn: 3})
	require.Contains(t, evs, Event{Kind: EventStopped, Turn: 4})
	cmdCh <- Shutdown()
}

func TestReleaseWithStalledSocketStillStops(t *testing.T) {
	cmdCh, eventCh := startPipeline(t, releaseTestServer(t, true), noisyDeps())
	cmdCh <- Press(1)
	time.Sleep(300 * time.Millisecond)
	cmdCh <- Release()
	ev := recvEvent(t, eventCh)
	require.Contains(t, []EventKind{EventStopped, EventError}, ev.Kind)
	cmdCh <- Shutdown()
}
