package message

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/stringext"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
	"github.com/taigrr/fantasy/providers/google"
	"github.com/taigrr/fantasy/providers/openai"
)

type MessageRole string

const (
	Assistant MessageRole = "assistant"
	User      MessageRole = "user"
	System    MessageRole = "system"
	Tool      MessageRole = "tool"
	Shell     MessageRole = "shell"
)

// mediaLoadFailedPlaceholder is the text substituted for image data that
// cannot be decoded during session replay.
const mediaLoadFailedPlaceholder = "[Image data could not be loaded]"

type FinishReason string

const (
	FinishReasonEndTurn   FinishReason = "end_turn"
	FinishReasonMaxTokens FinishReason = "max_tokens"
	FinishReasonToolUse   FinishReason = "tool_use"
	FinishReasonCanceled  FinishReason = "canceled"
	FinishReasonError     FinishReason = "error"

	// Should never happen
	FinishReasonUnknown FinishReason = "unknown"
)

type ContentPart interface {
	isPart()
}

type ReasoningContent struct {
	Thinking         string                             `json:"thinking"`
	Signature        string                             `json:"signature"`
	ThoughtSignature string                             `json:"thought_signature"` // Used for google
	ToolID           string                             `json:"tool_id"`           // Used for openrouter google models
	ResponsesData    *openai.ResponsesReasoningMetadata `json:"responses_data"`
	StartedAt        int64                              `json:"started_at,omitempty"`
	FinishedAt       int64                              `json:"finished_at,omitempty"`
}

func (tc ReasoningContent) String() string {
	return tc.Thinking
}
func (ReasoningContent) isPart() {}

type TextContent struct {
	Text string `json:"text"`
}

func (tc TextContent) String() string {
	return tc.Text
}

func (TextContent) isPart() {}

// SwarmMessage is a user-role content part injected by the swarm tool
// when another session sends a message to this one. It carries both
// the plain-text body (so the LLM sees the message as ordinary user
// text) and structured metadata about the sender so the UI can render
// a "colored square + name" header without regex-parsing text.
//
// The Text field is what the model reads (already prefixed with
// "message from <color-animal>:"). Body is the un-prefixed original
// message. Sender fields identify the origin session across
// workspaces.
type SwarmMessage struct {
	Text            string `json:"text"`
	Body            string `json:"body"`
	SenderSessionID string `json:"sender_session_id"`
	SenderColor     string `json:"sender_color"`
	SenderAnimal    string `json:"sender_animal"`
	// SenderWorkspaceID is the workspace the message originated from.
	// Empty if the sender workspace is unknown (e.g. a message
	// injected by a legacy path).
	SenderWorkspaceID string `json:"sender_workspace_id,omitempty"`
	// BTW is true when the sender used btw mode (message folded into
	// the receiver's current turn) rather than the default queued
	// mode.
	BTW bool `json:"btw,omitempty"`
	// RequireReply is true when the sender asked for a guaranteed
	// reply: the receiving session may not end its turn until it has
	// sent a swarm message back to SenderSessionID. The coordinator
	// enforces this by nudging the agent and, as a last resort,
	// replying on its behalf.
	RequireReply bool `json:"require_reply,omitempty"`
}

func (sm SwarmMessage) String() string {
	return sm.Text
}

func (SwarmMessage) isPart() {}

type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (iuc ImageURLContent) String() string {
	return iuc.URL
}

func (ImageURLContent) isPart() {}

type BinaryContent struct {
	Path     string
	MIMEType string
	Data     []byte
}

func (bc BinaryContent) String(p catwalk.InferenceProvider) string {
	base64Encoded := base64.StdEncoding.EncodeToString(bc.Data)
	if p == catwalk.InferenceProviderOpenAI {
		return "data:" + bc.MIMEType + ";base64," + base64Encoded
	}
	return base64Encoded
}

func (BinaryContent) isPart() {}

type ToolCall struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Input            string `json:"input"`
	ProviderExecuted bool   `json:"provider_executed"`
	Finished         bool   `json:"finished"`
}

func (ToolCall) isPart() {}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	Data       string `json:"data"`
	MIMEType   string `json:"mime_type"`
	Metadata   string `json:"metadata"`
	IsError    bool   `json:"is_error"`
}

func (ToolResult) isPart() {}

type Finish struct {
	Reason  FinishReason `json:"reason"`
	Time    int64        `json:"time"`
	Message string       `json:"message,omitempty"`
	Details string       `json:"details,omitempty"`
}

func (Finish) isPart() {}

type Message struct {
	ID               string
	Role             MessageRole
	SessionID        string
	Parts            []ContentPart
	Model            string
	Provider         string
	CreatedAt        int64
	UpdatedAt        int64
	IsSummaryMessage bool
}

func (m *Message) Content() TextContent {
	for _, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			return c
		}
		if sm, ok := part.(SwarmMessage); ok {
			// Return the full prefixed text so LLM-facing paths
			// (userToAIMessage, IsThinking, title generation) all
			// preserve the "message from <sender>:" header that
			// tells the model a cross-session swarm message
			// arrived. Callers that need the un-prefixed body
			// specifically should walk m.Parts and match
			// SwarmMessage explicitly.
			return TextContent{Text: sm.Text}
		}
	}
	return TextContent{}
}

func (m *Message) ReasoningContent() ReasoningContent {
	for _, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			return c
		}
	}
	return ReasoningContent{}
}

func (m *Message) ImageURLContent() []ImageURLContent {
	imageURLContents := make([]ImageURLContent, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ImageURLContent); ok {
			imageURLContents = append(imageURLContents, c)
		}
	}
	return imageURLContents
}

func (m *Message) BinaryContent() []BinaryContent {
	binaryContents := make([]BinaryContent, 0)
	for _, part := range m.Parts {
		if c, ok := part.(BinaryContent); ok {
			binaryContents = append(binaryContents, c)
		}
	}
	return binaryContents
}

func (m *Message) ToolCalls() []ToolCall {
	toolCalls := make([]ToolCall, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			toolCalls = append(toolCalls, c)
		}
	}
	return toolCalls
}

func (m *Message) ToolResults() []ToolResult {
	toolResults := make([]ToolResult, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolResult); ok {
			toolResults = append(toolResults, c)
		}
	}
	return toolResults
}

func (m *Message) IsFinished() bool {
	for _, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			return true
		}
	}
	return false
}

func (m *Message) FinishPart() *Finish {
	for _, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			return &c
		}
	}
	return nil
}

func (m *Message) FinishReason() FinishReason {
	for _, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			return c.Reason
		}
	}
	return ""
}

func (m *Message) IsThinking() bool {
	if m.ReasoningContent().Thinking != "" && m.Content().Text == "" && !m.IsFinished() {
		return true
	}
	return false
}

func (m *Message) AppendContent(delta string) {
	found := false
	for i, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			m.Parts[i] = TextContent{Text: c.Text + delta}
			found = true
		}
	}
	if !found {
		m.Parts = append(m.Parts, TextContent{Text: delta})
	}
}

func (m *Message) AppendReasoningContent(delta string) {
	found := false
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			m.Parts[i] = ReasoningContent{
				Thinking:   c.Thinking + delta,
				Signature:  c.Signature,
				StartedAt:  c.StartedAt,
				FinishedAt: c.FinishedAt,
			}
			found = true
		}
	}
	if !found {
		m.Parts = append(m.Parts, ReasoningContent{
			Thinking:  delta,
			StartedAt: time.Now().Unix(),
		})
	}
}

func (m *Message) AppendThoughtSignature(signature string, toolCallID string) {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			m.Parts[i] = ReasoningContent{
				Thinking:         c.Thinking,
				ThoughtSignature: c.ThoughtSignature + signature,
				ToolID:           toolCallID,
				Signature:        c.Signature,
				StartedAt:        c.StartedAt,
				FinishedAt:       c.FinishedAt,
			}
			return
		}
	}
	m.Parts = append(m.Parts, ReasoningContent{ThoughtSignature: signature})
}

func (m *Message) AppendReasoningSignature(signature string) {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			m.Parts[i] = ReasoningContent{
				Thinking:   c.Thinking,
				Signature:  c.Signature + signature,
				StartedAt:  c.StartedAt,
				FinishedAt: c.FinishedAt,
			}
			return
		}
	}
	m.Parts = append(m.Parts, ReasoningContent{Signature: signature})
}

func (m *Message) SetReasoningResponsesData(data *openai.ResponsesReasoningMetadata) {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			m.Parts[i] = ReasoningContent{
				Thinking:      c.Thinking,
				ResponsesData: data,
				StartedAt:     c.StartedAt,
				FinishedAt:    c.FinishedAt,
			}
			return
		}
	}
}

func (m *Message) FinishThinking() {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			if c.FinishedAt == 0 {
				m.Parts[i] = ReasoningContent{
					Thinking:   c.Thinking,
					Signature:  c.Signature,
					StartedAt:  c.StartedAt,
					FinishedAt: time.Now().Unix(),
				}
			}
			return
		}
	}
}

func (m *Message) ThinkingDuration() time.Duration {
	reasoning := m.ReasoningContent()
	if reasoning.StartedAt == 0 {
		return 0
	}

	endTime := reasoning.FinishedAt
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	return time.Duration(endTime-reasoning.StartedAt) * time.Second
}

func (m *Message) FinishToolCall(toolCallID string) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == toolCallID {
				m.Parts[i] = ToolCall{
					ID:       c.ID,
					Name:     c.Name,
					Input:    c.Input,
					Finished: true,
				}
				return
			}
		}
	}
}

func (m *Message) AppendToolCallInput(toolCallID string, inputDelta string) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == toolCallID {
				m.Parts[i] = ToolCall{
					ID:       c.ID,
					Name:     c.Name,
					Input:    c.Input + inputDelta,
					Finished: c.Finished,
				}
				return
			}
		}
	}
}

func (m *Message) AddToolCall(tc ToolCall) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == tc.ID {
				m.Parts[i] = tc
				return
			}
		}
	}
	m.Parts = append(m.Parts, tc)
}

func (m *Message) SetToolCalls(tc []ToolCall) {
	// remove any existing tool call part it could have multiple
	parts := make([]ContentPart, 0)
	for _, part := range m.Parts {
		if _, ok := part.(ToolCall); ok {
			continue
		}
		parts = append(parts, part)
	}
	m.Parts = parts
	for _, toolCall := range tc {
		m.Parts = append(m.Parts, toolCall)
	}
}

func (m *Message) AddToolResult(tr ToolResult) {
	m.Parts = append(m.Parts, tr)
}

func (m *Message) SetToolResults(tr []ToolResult) {
	for _, toolResult := range tr {
		m.Parts = append(m.Parts, toolResult)
	}
}

// Clone returns a deep copy of the message with an independent Parts slice.
// This prevents race conditions when the message is modified concurrently.
func (m *Message) Clone() Message {
	clone := *m
	clone.Parts = make([]ContentPart, len(m.Parts))
	copy(clone.Parts, m.Parts)
	return clone
}

// ResetStreamedContent removes all parts that were added during streaming
// (text, reasoning, tool calls, finish) so the message is ready for a
// retry. Non-streamed parts (images, binary attachments, tool results,
// shell commands) are preserved.
func (m *Message) ResetStreamedContent() {
	kept := m.Parts[:0]
	for _, part := range m.Parts {
		switch part.(type) {
		case TextContent, ReasoningContent, ToolCall, Finish:
			// Drop streamed parts.
		default:
			kept = append(kept, part)
		}
	}
	m.Parts = kept
}

func (m *Message) AddFinish(reason FinishReason, message, details string) {
	// remove any existing finish part
	for i, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			m.Parts = slices.Delete(m.Parts, i, i+1)
			break
		}
	}
	m.Parts = append(m.Parts, Finish{Reason: reason, Time: time.Now().Unix(), Message: message, Details: details})
}

func (m *Message) AddImageURL(url, detail string) {
	m.Parts = append(m.Parts, ImageURLContent{URL: url, Detail: detail})
}

func (m *Message) AddBinary(mimeType string, data []byte) {
	m.Parts = append(m.Parts, BinaryContent{MIMEType: mimeType, Data: data})
}

func PromptWithTextAttachments(prompt string, attachments []Attachment) string {
	var sb strings.Builder
	sb.WriteString(prompt)
	addedAttachments := false
	for _, content := range attachments {
		if !content.IsText() {
			continue
		}
		if !addedAttachments {
			sb.WriteString("\n<system_info>The files below have been attached by the user, consider them in your response</system_info>\n")
			addedAttachments = true
		}
		if content.FilePath != "" {
			fmt.Fprintf(&sb, "<file path='%s'>\n", content.FilePath)
		} else {
			sb.WriteString("<file>\n")
		}
		sb.WriteString("\n")
		sb.Write(content.Content)
		sb.WriteString("\n</file>\n")
	}
	return sb.String()
}

func (m *Message) ToAIMessage() []fantasy.Message {
	switch m.Role {
	case Shell:
		return []fantasy.Message{m.shellToAIMessage()}
	case User:
		return []fantasy.Message{m.userToAIMessage()}
	case Assistant:
		return []fantasy.Message{m.assistantToAIMessage()}
	case Tool:
		return []fantasy.Message{m.toolToAIMessage()}
	}
	return nil
}

// shellToAIMessage renders a shell message — command (part 0) and output
// (part 1) — as a single user message of the form "$ cmd\noutput".
func (m *Message) shellToAIMessage() fantasy.Message {
	var command, output string
	for i, part := range m.Parts {
		if tc, ok := part.(TextContent); ok {
			if i == 0 {
				command = tc.Text
			} else {
				output = tc.Text
			}
		}
	}
	text := "$ " + command
	if output != "" {
		text += "\n" + output
	}
	return fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: text}},
	}
}

// userToAIMessage renders a user message: trimmed text (with text/* attachments
// inlined into the prompt) followed by non-text binary attachments as file
// parts.
func (m *Message) userToAIMessage() fantasy.Message {
	var parts []fantasy.MessagePart
	text := strings.TrimSpace(m.Content().Text)
	var textAttachments []Attachment
	for _, content := range m.BinaryContent() {
		if !strings.HasPrefix(content.MIMEType, "text/") {
			continue
		}
		textAttachments = append(textAttachments, Attachment{
			FilePath: content.Path,
			MimeType: content.MIMEType,
			Content:  content.Data,
		})
	}
	text = PromptWithTextAttachments(text, textAttachments)
	if text != "" {
		parts = append(parts, fantasy.TextPart{Text: text})
	}
	for _, content := range m.BinaryContent() {
		// skip text attachements
		if strings.HasPrefix(content.MIMEType, "text/") {
			continue
		}
		parts = append(parts, fantasy.FilePart{
			Filename:  content.Path,
			Data:      content.Data,
			MediaType: content.MIMEType,
		})
	}
	return fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: parts,
	}
}

// assistantToAIMessage renders an assistant message: text, then reasoning (with
// provider-specific signature metadata), then tool calls.
func (m *Message) assistantToAIMessage() fantasy.Message {
	var parts []fantasy.MessagePart
	text := strings.TrimSpace(m.Content().Text)
	if text != "" {
		parts = append(parts, fantasy.TextPart{Text: text})
	}
	reasoning := m.ReasoningContent()
	if reasoning.Thinking != "" {
		reasoningPart := fantasy.ReasoningPart{Text: reasoning.Thinking, ProviderOptions: fantasy.ProviderOptions{}}
		if reasoning.Signature != "" {
			reasoningPart.ProviderOptions[anthropic.Name] = &anthropic.ReasoningOptionMetadata{
				Signature: reasoning.Signature,
			}
		}
		if reasoning.ResponsesData != nil {
			reasoningPart.ProviderOptions[openai.Name] = reasoning.ResponsesData
		}
		if reasoning.ThoughtSignature != "" {
			reasoningPart.ProviderOptions[google.Name] = &google.ReasoningMetadata{
				Signature: reasoning.ThoughtSignature,
				ToolID:    reasoning.ToolID,
			}
		}
		parts = append(parts, reasoningPart)
	}
	for _, call := range m.ToolCalls() {
		parts = append(parts, fantasy.ToolCallPart{
			ToolCallID:       call.ID,
			ToolName:         call.Name,
			Input:            call.Input,
			ProviderExecuted: call.ProviderExecuted,
		})
	}
	return fantasy.Message{
		Role:    fantasy.MessageRoleAssistant,
		Content: parts,
	}
}

// toolToAIMessage renders tool results, classifying each as error, media, or
// text output. Media whose data is not valid base64 is downgraded to a
// placeholder so a corrupt payload can't break the provider request.
func (m *Message) toolToAIMessage() fantasy.Message {
	var parts []fantasy.MessagePart
	for _, result := range m.ToolResults() {
		var content fantasy.ToolResultOutputContent
		if result.IsError {
			content = fantasy.ToolResultOutputContentError{
				Error: errors.New(result.Content),
			}
		} else if result.Data != "" {
			if stringext.IsValidBase64(result.Data) {
				content = fantasy.ToolResultOutputContentMedia{
					Data:      result.Data,
					MediaType: result.MIMEType,
				}
			} else {
				content = fantasy.ToolResultOutputContentText{
					Text: mediaLoadFailedPlaceholder,
				}
			}
		} else {
			content = fantasy.ToolResultOutputContentText{
				Text: result.Content,
			}
		}
		parts = append(parts, fantasy.ToolResultPart{
			ToolCallID: result.ToolCallID,
			Output:     content,
		})
	}
	return fantasy.Message{
		Role:    fantasy.MessageRoleTool,
		Content: parts,
	}
}
