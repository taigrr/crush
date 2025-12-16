package chat

import "github.com/charmbracelet/crush/internal/message"

// SendMsg represents a message to send a chat message.
type SendMsg struct {
	Text        string
	Attachments []message.Attachment
}
