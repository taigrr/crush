package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshalUnmarshalPartsRoundTrip(t *testing.T) {
	t.Parallel()

	parts := []ContentPart{
		ReasoningContent{Thinking: "think", Signature: "sig"},
		TextContent{Text: "hello <b>&</b>"},
		SwarmMessage{},
		ImageURLContent{URL: "https://x/y.png", Detail: "high"},
		BinaryContent{Path: "a.png", MIMEType: "image/png", Data: []byte{1, 2, 3}},
		ToolCall{ID: "1", Name: "bash", Input: `{"a":1}`, Finished: true},
		ToolResult{ToolCallID: "1", Name: "bash", Content: "ok", IsError: true},
		Finish{Reason: FinishReasonEndTurn, Time: 42, Message: "m"},
	}
	data, err := MarshalParts(parts)
	require.NoError(t, err)
	got, err := UnmarshalParts(data)
	require.NoError(t, err)
	require.Equal(t, parts, got)

	empty, err := UnmarshalParts([]byte("[]"))
	require.NoError(t, err)
	require.Empty(t, empty)

	_, err = UnmarshalParts([]byte(`[{"type":"bogus","data":{}}]`))
	require.Error(t, err)
	_, err = UnmarshalParts([]byte(`[null]`))
	require.Error(t, err)
}
