package agent

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
)

func TestConvertToToolResult(t *testing.T) {
	t.Parallel()

	a := &sessionAgent{}

	t.Run("text result", func(t *testing.T) {
		t.Parallel()
		got := a.convertToToolResult(fantasy.ToolResultContent{
			ToolCallID:     "c1",
			ToolName:       "bash",
			ClientMetadata: "meta",
			Result:         fantasy.ToolResultOutputContentText{Text: "hello"},
		})
		require.Equal(t, "c1", got.ToolCallID)
		require.Equal(t, "bash", got.Name)
		require.Equal(t, "meta", got.Metadata)
		require.Equal(t, "hello", got.Content)
		require.False(t, got.IsError)
	})

	t.Run("error result", func(t *testing.T) {
		t.Parallel()
		got := a.convertToToolResult(fantasy.ToolResultContent{
			ToolCallID: "c2",
			ToolName:   "view",
			Result:     fantasy.ToolResultOutputContentError{Error: errors.New("boom")},
		})
		require.Equal(t, "boom", got.Content)
		require.True(t, got.IsError)
	})

	t.Run("valid media result", func(t *testing.T) {
		t.Parallel()
		data := base64.StdEncoding.EncodeToString([]byte("fake-image"))
		got := a.convertToToolResult(fantasy.ToolResultContent{
			ToolCallID: "c3",
			ToolName:   "view",
			Result: fantasy.ToolResultOutputContentMedia{
				Data:      data,
				MediaType: "image/png",
				Text:      "screenshot",
			},
		})
		require.False(t, got.IsError)
		require.Equal(t, "screenshot", got.Content)
		require.Equal(t, data, got.Data)
		require.Equal(t, "image/png", got.MIMEType)
	})

	t.Run("media result defaults content when text empty", func(t *testing.T) {
		t.Parallel()
		data := base64.StdEncoding.EncodeToString([]byte("img"))
		got := a.convertToToolResult(fantasy.ToolResultContent{
			Result: fantasy.ToolResultOutputContentMedia{
				Data:      data,
				MediaType: "image/jpeg",
			},
		})
		require.False(t, got.IsError)
		require.Equal(t, "Loaded image/jpeg content", got.Content)
		require.Equal(t, data, got.Data)
	})

	t.Run("invalid base64 media downgraded to error", func(t *testing.T) {
		t.Parallel()
		got := a.convertToToolResult(fantasy.ToolResultContent{
			ToolCallID: "c4",
			ToolName:   "view",
			Result: fantasy.ToolResultOutputContentMedia{
				Data:      "!!!not-base64!!!",
				MediaType: "image/png",
			},
		})
		require.True(t, got.IsError)
		require.Empty(t, got.Data, "invalid media data must not be forwarded")
		require.Contains(t, got.Content, "invalid encoding")
	})
}
