package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/message"
)

func TestPromptHistoryTextsSkipsSwarmMessages(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "first prompt"}}},
		{Role: message.User, Parts: []message.ContentPart{message.SwarmMessage{
			Text: "message from fuchsia-frog: do the thing",
			Body: "do the thing",
		}}},
		{Role: message.Shell, Parts: []message.ContentPart{message.TextContent{Text: "ls -la"}}},
		{Role: message.User},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "second prompt"}}},
	}

	require.Equal(t, []string{"first prompt", "!ls -la", "second prompt"}, promptHistoryTexts(msgs))
}
