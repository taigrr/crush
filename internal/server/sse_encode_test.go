package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pubsub"
)

func TestAppendSSEMatchesMarshal(t *testing.T) {
	t.Parallel()
	cases := []*pubsub.Payload{
		{Type: pubsub.PayloadTypeMessage},
		{Type: `we<ird>&"type"`, Payload: json.RawMessage(`{"a":"\u003cb\u003e\u0026"}`)},
	}
	for _, u := range streamUpdates(wireScenarios[0])[:50] {
		u.Parts = append(u.Parts, message.TextContent{Text: "<html> & \u2028 \"q\""})
		cases = append(cases, wrapEvent(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: u}))
	}
	for _, p := range cases {
		want, err := json.Marshal(p)
		require.NoError(t, err)
		got, err := appendSSE(nil, p)
		require.NoError(t, err)
		require.Equal(t, "data: "+string(want)+"\n\n", string(got))
	}
}
