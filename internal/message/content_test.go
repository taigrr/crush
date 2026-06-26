package message

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
)

func makeTestAttachments(n int, contentSize int) []Attachment {
	attachments := make([]Attachment, n)
	content := []byte(strings.Repeat("x", contentSize))
	for i := range n {
		attachments[i] = Attachment{
			FilePath: fmt.Sprintf("/path/to/file%d.txt", i),
			MimeType: "text/plain",
			Content:  content,
		}
	}
	return attachments
}

func TestToAIMessage_CorruptedMediaData(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_123",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       "abc\x80def",
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_123", part.ToolCallID)

	textContent, ok := part.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "corrupted media should be downgraded to text")
	require.Equal(t, mediaLoadFailedPlaceholder, textContent.Text)
}

func TestToAIMessage_ValidMediaData(t *testing.T) {
	t.Parallel()

	validBase64 := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47})

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_456",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       validBase64,
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_456", part.ToolCallID)

	mediaContent, ok := part.Output.(fantasy.ToolResultOutputContentMedia)
	require.True(t, ok, "valid media should remain as media")
	require.Equal(t, validBase64, mediaContent.Data)
	require.Equal(t, "image/png", mediaContent.MediaType)
}

func TestToAIMessage_ASCIIButInvalidBase64(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_789",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       "not-valid-base64!!!",
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_789", part.ToolCallID)

	textContent, ok := part.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "ASCII but invalid base64 should be downgraded to text")
	require.Equal(t, mediaLoadFailedPlaceholder, textContent.Text)
}

func BenchmarkPromptWithTextAttachments(b *testing.B) {
	cases := []struct {
		name        string
		numFiles    int
		contentSize int
	}{
		{"1file_100bytes", 1, 100},
		{"5files_1KB", 5, 1024},
		{"10files_10KB", 10, 10 * 1024},
		{"20files_50KB", 20, 50 * 1024},
	}

	for _, tc := range cases {
		attachments := makeTestAttachments(tc.numFiles, tc.contentSize)
		prompt := "Process these files"

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = PromptWithTextAttachments(prompt, attachments)
			}
		})
	}
}

func TestToAIMessage_Shell(t *testing.T) {
	t.Parallel()
	msg := &Message{
		Role: Shell,
		Parts: []ContentPart{
			TextContent{Text: "ls -la"},
			TextContent{Text: "total 0"},
		},
	}
	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Equal(t, fantasy.MessageRoleUser, messages[0].Role)
	require.Len(t, messages[0].Content, 1)
	text, ok := messages[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Equal(t, "$ ls -la\ntotal 0", text.Text)
}

func TestToAIMessage_ShellNoOutput(t *testing.T) {
	t.Parallel()
	msg := &Message{
		Role:  Shell,
		Parts: []ContentPart{TextContent{Text: "pwd"}},
	}
	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	text := messages[0].Content[0].(fantasy.TextPart)
	require.Equal(t, "$ pwd", text.Text)
}

func TestToAIMessage_UserTextOnly(t *testing.T) {
	t.Parallel()
	msg := &Message{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "  hello  "}},
	}
	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Equal(t, fantasy.MessageRoleUser, messages[0].Role)
	require.Len(t, messages[0].Content, 1)
	text := messages[0].Content[0].(fantasy.TextPart)
	require.Equal(t, "hello", text.Text)
}

func TestToAIMessage_UserWithImageAttachment(t *testing.T) {
	t.Parallel()
	msg := &Message{
		Role: User,
		Parts: []ContentPart{
			TextContent{Text: "look"},
			BinaryContent{Path: "/img.png", MIMEType: "image/png", Data: []byte{1, 2, 3}},
		},
	}
	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 2)
	_, ok := messages[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	file, ok := messages[0].Content[1].(fantasy.FilePart)
	require.True(t, ok)
	require.Equal(t, "image/png", file.MediaType)
	require.Equal(t, "/img.png", file.Filename)
}

func TestToAIMessage_AssistantTextAndToolCall(t *testing.T) {
	t.Parallel()
	msg := &Message{
		Role: Assistant,
		Parts: []ContentPart{
			TextContent{Text: "doing it"},
			ToolCall{ID: "c1", Name: "bash", Input: `{"command":"ls"}`},
		},
	}
	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Equal(t, fantasy.MessageRoleAssistant, messages[0].Role)
	require.Len(t, messages[0].Content, 2)
	text := messages[0].Content[0].(fantasy.TextPart)
	require.Equal(t, "doing it", text.Text)
	call := messages[0].Content[1].(fantasy.ToolCallPart)
	require.Equal(t, "c1", call.ToolCallID)
	require.Equal(t, "bash", call.ToolName)
}

func TestToAIMessage_AssistantReasoningSignature(t *testing.T) {
	t.Parallel()
	msg := &Message{
		Role: Assistant,
		Parts: []ContentPart{
			ReasoningContent{Thinking: "hmm", Signature: "sig"},
			TextContent{Text: "answer"},
		},
	}
	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	// reasoning part comes after text in the output ordering.
	var reasoning fantasy.ReasoningPart
	var found bool
	for _, p := range messages[0].Content {
		if rp, ok := p.(fantasy.ReasoningPart); ok {
			reasoning = rp
			found = true
		}
	}
	require.True(t, found)
	require.Equal(t, "hmm", reasoning.Text)
}
