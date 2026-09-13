package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/message"
)

func TestEmptyResponseError(t *testing.T) {
	t.Parallel()

	empty := message.Message{Parts: []message.ContentPart{
		message.ReasoningContent{Signature: "sig"},
	}}
	withText := message.Message{Parts: []message.ContentPart{
		message.TextContent{Text: "hello"},
	}}
	withTool := message.Message{Parts: []message.ContentPart{
		message.ToolCall{ID: "1", Name: "bash"},
	}}

	title, _, ok := emptyResponseError(fantasy.FinishReasonContentFilter, withText)
	require.True(t, ok)
	require.Equal(t, "Response blocked", title)

	title, _, ok = emptyResponseError(fantasy.FinishReasonUnknown, empty)
	require.True(t, ok)
	require.Equal(t, "Empty response", title)

	_, _, ok = emptyResponseError(fantasy.FinishReasonUnknown, withText)
	require.False(t, ok)
	_, _, ok = emptyResponseError(fantasy.FinishReasonUnknown, withTool)
	require.False(t, ok)
	_, _, ok = emptyResponseError(fantasy.FinishReasonStop, empty)
	require.False(t, ok)
}
