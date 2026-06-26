package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

func TestParseSSELine(t *testing.T) {
	t.Parallel()

	valid := string(marshalSSEPayload(t))

	cases := []struct {
		name   string
		line   string
		wantOK bool
	}{
		{"blank line", "", false},
		{"whitespace only", "   \n", false},
		{"missing data prefix", "event: foo", false},
		{"malformed envelope json", "data: {not json", false},
		{"unknown payload type", `data: {"type":"bogus","payload":{}}`, false},
		{"valid agent event", "data: " + valid, true},
		{"valid with trailing space", "data:   " + valid + "  ", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ok := parseSSELine([]byte(tc.line))
			require.Equal(t, tc.wantOK, ok)
		})
	}
}

func TestDecodeEventDispatchesByType(t *testing.T) {
	t.Parallel()

	inner, err := json.Marshal(pubsub.Event[proto.Message]{Type: pubsub.CreatedEvent})
	require.NoError(t, err)

	ev, ok := decodeEvent(pubsub.Payload{Type: pubsub.PayloadTypeMessage, Payload: inner})
	require.True(t, ok)
	_, isMsg := ev.(pubsub.Event[proto.Message])
	require.True(t, isMsg, "expected pubsub.Event[proto.Message], got %T", ev)

	_, ok = decodeEvent(pubsub.Payload{Type: "nope"})
	require.False(t, ok)
}

// TestSubscribeEventsDeliversFinalUnterminatedEvent verifies the regression
// fix: an SSE frame that arrives without a trailing newline (the server
// closes the stream right after writing it) must still be delivered. Before
// the fix, ReadBytes returned the data together with io.EOF and the loop
// broke before parsing it, dropping the last event.
func TestSubscribeEventsDeliversFinalUnterminatedEvent(t *testing.T) {
	t.Parallel()

	payload := marshalSSEPayload(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// No trailing newline; the handler returns immediately, closing
		// the response body right after this write.
		_, _ = fmt.Fprintf(w, "data: %s", payload)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	events, err := c.SubscribeEvents(context.Background(), "ws1")
	require.NoError(t, err)

	select {
	case ev, ok := <-events:
		require.True(t, ok, "channel closed without delivering final event")
		_, isAgent := ev.(pubsub.Event[proto.AgentEvent])
		require.True(t, isAgent, "expected agent event, got %T", ev)
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for final unterminated event")
	}
}
