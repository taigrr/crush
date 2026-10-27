package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// encodeStream runs events through a stream encoder exactly as the SSE
// handler does, flushing frames every flushEvery events.
func encodeStream(t *testing.T, opts streamOptions, events []any, flushEvery int) ([]byte, int) {
	t.Helper()
	enc := newStreamEncoder(opts)
	var buf bytes.Buffer
	n := 0
	write := func(ps []*pubsub.Payload) {
		for _, p := range ps {
			data, err := json.Marshal(p)
			require.NoError(t, err)
			fmt.Fprintf(&buf, "data: %s\n\n", data)
			n++
		}
	}
	for i, ev := range events {
		write(enc.add(ev))
		if flushEvery > 0 && i%flushEvery == 0 {
			write(enc.flush())
		}
	}
	write(enc.flush())
	return buf.Bytes(), n
}

// decodeFinal decodes a stream and returns the last state of each message.
func decodeFinal(t *testing.T, data []byte) map[string]proto.Message {
	t.Helper()
	out := map[string]proto.Message{}
	for ev := range client.DecodeEventStream(t.Context(), bytes.NewReader(data)) {
		if me, ok := ev.(pubsub.Event[proto.Message]); ok {
			out[me.Payload.ID] = me.Payload
		}
	}
	return out
}

func streamedTurn() ([]any, message.Message) {
	updates := streamUpdates(wireScenarios[1])
	events := []any{pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{ID: updates[0].ID, SessionID: updates[0].SessionID, Role: message.Assistant}}}
	for _, u := range updates {
		events = append(events, pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: u})
	}
	final := updates[len(updates)-1]
	final.Parts = append(final.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
	events = append(events, pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: final})
	return events, final
}

func TestWire2ReconstructsIdenticalMessages(t *testing.T) {
	t.Parallel()
	events, final := streamedTurn()
	want := messageToProto(final)

	v1, n1 := encodeStream(t, streamOptions{wire: 1}, events, 0)
	v2, n2 := encodeStream(t, streamOptions{wire: 2}, events, 0)
	framed, n3 := encodeStream(t, streamOptions{wire: 2, frame: 1}, events, 5)

	for name, data := range map[string][]byte{"wire1": v1, "wire2": v2, "framed": framed} {
		got := decodeFinal(t, data)[want.ID]
		require.Equal(t, want.Parts, got.Parts, name)
	}
	require.Equal(t, n1, n2, "deltas replace updates one for one")
	require.Less(t, n3, n2/4, "frames coalesce updates")
	require.Less(t, len(v2)*15, len(v1), "deltas are far smaller (got v1=%d v2=%d)", len(v1), len(v2))
	t.Logf("wire1 %d bytes, wire2 %d bytes, wire2+frames %d bytes", len(v1), len(v2), len(framed))
}

func TestWire2KeyframesAfterNonAppendChange(t *testing.T) {
	t.Parallel()
	m := message.Message{ID: "m1", Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "draft"}}}
	edited := m
	edited.Parts = []message.ContentPart{message.TextContent{Text: "rewritten"}}
	events := []any{
		pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: m},
		pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: edited},
	}
	data, _ := encodeStream(t, streamOptions{wire: 2}, events, 0)
	require.Contains(t, string(data), `"type":"message"`)
	require.Equal(t, "rewritten", decodeFinal(t, data)["m1"].Parts[0].(proto.TextContent).Text)
}

func TestWire2OtherEventsFlushPendingFirst(t *testing.T) {
	t.Parallel()
	m := message.Message{ID: "m1", Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "a"}}}
	enc := newStreamEncoder(streamOptions{wire: 2, frame: 1})
	require.Empty(t, enc.add(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: m}))
	out := enc.add(pubsub.Event[proto.ConfigChanged]{Type: pubsub.UpdatedEvent})
	require.Len(t, out, 2)
	require.Equal(t, pubsub.PayloadTypeMessage, out[0].Type, "held update goes out before the next event")
}

// TestWire2GzipStreamOverHTTP drives the real SSE handler with the
// options a remote client negotiates (wire 2, frames, gzip) and checks
// the decoded stream yields the final message.
func TestWire2GzipStreamOverHTTP(t *testing.T) {
	h := newE2EHarness(t)
	events, final := streamedTurn()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		h.httpSrv.URL+"/v1/workspaces/"+h.workspace.ID+"/events?client_id="+uuid.NewString()+"&wire=2&frame_ms=20", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	tr := &http.Transport{DisableCompression: true} // see the raw encoding
	rsp, err := (&http.Client{Transport: tr}).Do(req)
	require.NoError(t, err)
	defer rsp.Body.Close()
	require.Equal(t, "gzip", rsp.Header.Get("Content-Encoding"))
	require.Equal(t, "2", rsp.Header.Get("X-Crush-Wire"))

	zr, err := gzip.NewReader(rsp.Body)
	require.NoError(t, err)
	decoded := client.DecodeEventStream(t.Context(), zr)

	for _, ev := range events {
		h.app.SendEvent(ev.(tea.Msg))
	}
	want := messageToProto(final)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-decoded:
			require.True(t, ok, "stream ended early")
			if me, isMsg := ev.(pubsub.Event[proto.Message]); isMsg && me.Payload.ID == want.ID && me.Payload.IsFinished() {
				require.Equal(t, want.Parts, me.Payload.Parts)
				return
			}
		case <-deadline:
			t.Fatal("finished message never arrived")
		}
	}
}
