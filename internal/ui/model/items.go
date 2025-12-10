package model

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/lazylist"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/ui/toolrender"
)

// Identifiable is an interface for items that can provide a unique identifier.
type Identifiable interface {
	ID() string
}

// MessageItem represents a [message.Message] item that can be displayed in the
// UI and be part of a [lazylist.List] identifiable by a unique ID.
type MessageItem interface {
	lazylist.Item
	lazylist.Item
	Identifiable
}

// MessageContentItem represents rendered message content (text, markdown, errors, etc).
type MessageContentItem struct {
	id         string
	content    string
	role       message.MessageRole
	isMarkdown bool
	maxWidth   int
	sty        *styles.Styles
}

// NewMessageContentItem creates a new message content item.
func NewMessageContentItem(id, content string, role message.MessageRole, isMarkdown bool, sty *styles.Styles) *MessageContentItem {
	m := &MessageContentItem{
		id:         id,
		content:    content,
		isMarkdown: isMarkdown,
		role:       role,
		maxWidth:   120,
		sty:        sty,
	}
	return m
}

// ID implements Identifiable.
func (m *MessageContentItem) ID() string {
	return m.id
}

// FocusStyle returns the focus style.
func (m *MessageContentItem) FocusStyle() lipgloss.Style {
	if m.role == message.User {
		return m.sty.Chat.Message.UserFocused
	}
	return m.sty.Chat.Message.AssistantFocused
}

// BlurStyle returns the blur style.
func (m *MessageContentItem) BlurStyle() lipgloss.Style {
	if m.role == message.User {
		return m.sty.Chat.Message.UserBlurred
	}
	return m.sty.Chat.Message.AssistantBlurred
}

// HighlightStyle returns the highlight style.
func (m *MessageContentItem) HighlightStyle() lipgloss.Style {
	return m.sty.TextSelection
}

// Render renders the content at the given width, using cache if available.
//
// It implements [lazylist.Item].
func (m *MessageContentItem) Render(width int) string {
	contentWidth := width
	// Cap width to maxWidth for markdown
	cappedWidth := contentWidth
	if m.isMarkdown {
		cappedWidth = min(contentWidth, m.maxWidth)
	}

	var rendered string
	if m.isMarkdown {
		renderer := common.MarkdownRenderer(m.sty, cappedWidth)
		result, err := renderer.Render(m.content)
		if err != nil {
			rendered = m.content
		} else {
			rendered = strings.TrimSuffix(result, "\n")
		}
	} else {
		rendered = m.content
	}

	return rendered
}

// ToolCallItem represents a rendered tool call with its header and content.
type ToolCallItem struct {
	id         string
	toolCall   message.ToolCall
	toolResult message.ToolResult
	cancelled  bool
	isNested   bool
	maxWidth   int
	sty        *styles.Styles
}

// cachedToolRender stores both the rendered string and its height.
type cachedToolRender struct {
	content string
	height  int
}

// NewToolCallItem creates a new tool call item.
func NewToolCallItem(id string, toolCall message.ToolCall, toolResult message.ToolResult, cancelled bool, isNested bool, sty *styles.Styles) *ToolCallItem {
	t := &ToolCallItem{
		id:         id,
		toolCall:   toolCall,
		toolResult: toolResult,
		cancelled:  cancelled,
		isNested:   isNested,
		maxWidth:   120,
		sty:        sty,
	}
	return t
}

// generateCacheKey creates a key that changes when tool call content changes.
func generateCacheKey(toolCall message.ToolCall, toolResult message.ToolResult, cancelled bool) string {
	// Simple key based on result state - when result arrives or changes, key changes
	return fmt.Sprintf("%s:%s:%v", toolCall.ID, toolResult.ToolCallID, cancelled)
}

// ID implements Identifiable.
func (t *ToolCallItem) ID() string {
	return t.id
}

// FocusStyle returns the focus style.
func (t *ToolCallItem) FocusStyle() lipgloss.Style {
	return t.sty.Chat.Message.ToolCallFocused
}

// BlurStyle returns the blur style.
func (t *ToolCallItem) BlurStyle() lipgloss.Style {
	return t.sty.Chat.Message.ToolCallBlurred
}

// HighlightStyle returns the highlight style.
func (t *ToolCallItem) HighlightStyle() lipgloss.Style {
	return t.sty.TextSelection
}

// Render implements lazylist.Item.
func (t *ToolCallItem) Render(width int) string {
	// Render the tool call
	ctx := &toolrender.RenderContext{
		Call:      t.toolCall,
		Result:    t.toolResult,
		Cancelled: t.cancelled,
		IsNested:  t.isNested,
		Width:     width,
		Styles:    t.sty,
	}

	rendered := toolrender.Render(ctx)
	return rendered
}

// AttachmentItem represents a file attachment in a user message.
type AttachmentItem struct {
	id       string
	filename string
	path     string
	sty      *styles.Styles
}

// NewAttachmentItem creates a new attachment item.
func NewAttachmentItem(id, filename, path string, sty *styles.Styles) *AttachmentItem {
	a := &AttachmentItem{
		id:       id,
		filename: filename,
		path:     path,
		sty:      sty,
	}
	return a
}

// ID implements Identifiable.
func (a *AttachmentItem) ID() string {
	return a.id
}

// FocusStyle returns the focus style.
func (a *AttachmentItem) FocusStyle() lipgloss.Style {
	return a.sty.Chat.Message.AssistantFocused
}

// BlurStyle returns the blur style.
func (a *AttachmentItem) BlurStyle() lipgloss.Style {
	return a.sty.Chat.Message.AssistantBlurred
}

// HighlightStyle returns the highlight style.
func (a *AttachmentItem) HighlightStyle() lipgloss.Style {
	return a.sty.TextSelection
}

// Render implements lazylist.Item.
func (a *AttachmentItem) Render(width int) string {
	const maxFilenameWidth = 10
	content := a.sty.Chat.Message.Attachment.Render(fmt.Sprintf(
		" %s %s ",
		styles.DocumentIcon,
		ansi.Truncate(a.filename, maxFilenameWidth, "..."),
	))

	return content

	// return a.RenderWithHighlight(content, width, a.CurrentStyle())
}

// ThinkingItem represents thinking/reasoning content in assistant messages.
type ThinkingItem struct {
	id       string
	thinking string
	duration time.Duration
	finished bool
	maxWidth int
	sty      *styles.Styles
}

// NewThinkingItem creates a new thinking item.
func NewThinkingItem(id, thinking string, duration time.Duration, finished bool, sty *styles.Styles) *ThinkingItem {
	t := &ThinkingItem{
		id:       id,
		thinking: thinking,
		duration: duration,
		finished: finished,
		maxWidth: 120,
		sty:      sty,
	}
	return t
}

// ID implements Identifiable.
func (t *ThinkingItem) ID() string {
	return t.id
}

// FocusStyle returns the focus style.
func (t *ThinkingItem) FocusStyle() lipgloss.Style {
	return t.sty.Chat.Message.AssistantFocused
}

// BlurStyle returns the blur style.
func (t *ThinkingItem) BlurStyle() lipgloss.Style {
	return t.sty.Chat.Message.AssistantBlurred
}

// HighlightStyle returns the highlight style.
func (t *ThinkingItem) HighlightStyle() lipgloss.Style {
	return t.sty.TextSelection
}

// Render implements lazylist.Item.
func (t *ThinkingItem) Render(width int) string {
	cappedWidth := min(width, t.maxWidth)

	renderer := common.PlainMarkdownRenderer(cappedWidth - 1)
	rendered, err := renderer.Render(t.thinking)
	if err != nil {
		// Fallback to line-by-line rendering
		lines := strings.Split(t.thinking, "\n")
		var content strings.Builder
		lineStyle := t.sty.PanelMuted
		for i, line := range lines {
			if line == "" {
				continue
			}
			content.WriteString(lineStyle.Width(cappedWidth).Render(line))
			if i < len(lines)-1 {
				content.WriteString("\n")
			}
		}
		rendered = content.String()
	}

	fullContent := strings.TrimSpace(rendered)

	// Add footer if finished
	if t.finished && t.duration > 0 {
		footer := t.sty.Chat.Message.ThinkingFooter.Render(fmt.Sprintf("Thought for %s", t.duration.String()))
		fullContent = lipgloss.JoinVertical(lipgloss.Left, fullContent, "", footer)
	}

	result := t.sty.PanelMuted.Width(cappedWidth).Padding(0, 1).Render(fullContent)

	return result
}

// SectionHeaderItem represents a section header (e.g., assistant info).
type SectionHeaderItem struct {
	id              string
	modelName       string
	duration        time.Duration
	isSectionHeader bool
	sty             *styles.Styles
	content         string
}

// NewSectionHeaderItem creates a new section header item.
func NewSectionHeaderItem(id, modelName string, duration time.Duration, sty *styles.Styles) *SectionHeaderItem {
	s := &SectionHeaderItem{
		id:              id,
		modelName:       modelName,
		duration:        duration,
		isSectionHeader: true,
		sty:             sty,
	}
	return s
}

// ID implements Identifiable.
func (s *SectionHeaderItem) ID() string {
	return s.id
}

// IsSectionHeader returns true if this is a section header.
func (s *SectionHeaderItem) IsSectionHeader() bool {
	return s.isSectionHeader
}

// FocusStyle returns the focus style.
func (s *SectionHeaderItem) FocusStyle() lipgloss.Style {
	return s.sty.Chat.Message.AssistantFocused
}

// BlurStyle returns the blur style.
func (s *SectionHeaderItem) BlurStyle() lipgloss.Style {
	return s.sty.Chat.Message.AssistantBlurred
}

// Render implements lazylist.Item.
func (s *SectionHeaderItem) Render(width int) string {
	content := fmt.Sprintf("%s %s %s",
		s.sty.Subtle.Render(styles.ModelIcon),
		s.sty.Muted.Render(s.modelName),
		s.sty.Subtle.Render(s.duration.String()),
	)

	return s.sty.Chat.Message.SectionHeader.Render(content)
}

// GetMessageItems extracts [MessageItem]s from a [message.Message]. It returns
// all parts of the message as [MessageItem]s.
//
// For assistant messages with tool calls, pass a toolResults map to link results.
// Use BuildToolResultMap to create this map from all messages in a session.
func GetMessageItems(sty *styles.Styles, msg *message.Message, toolResults map[string]message.ToolResult) []MessageItem {
	var items []MessageItem

	// Skip tool result messages - they're displayed inline with tool calls
	if msg.Role == message.Tool {
		return items
	}

	// Process user messages
	if msg.Role == message.User {
		// Add main text content
		content := msg.Content().String()
		if content != "" {
			item := NewMessageContentItem(
				fmt.Sprintf("%s-content", msg.ID),
				content,
				msg.Role,
				true, // User messages are markdown
				sty,
			)
			items = append(items, item)
		}

		// Add attachments
		for i, attachment := range msg.BinaryContent() {
			filename := filepath.Base(attachment.Path)
			item := NewAttachmentItem(
				fmt.Sprintf("%s-attachment-%d", msg.ID, i),
				filename,
				attachment.Path,
				sty,
			)
			items = append(items, item)
		}

		return items
	}

	// Process assistant messages
	if msg.Role == message.Assistant {
		// Check if we need to add a section header
		finishData := msg.FinishPart()
		if finishData != nil && msg.Model != "" {
			model := config.Get().GetModel(msg.Provider, msg.Model)
			modelName := "Unknown Model"
			if model != nil {
				modelName = model.Name
			}

			// Calculate duration (this would need the last user message time)
			duration := time.Duration(0)
			if finishData.Time > 0 {
				duration = time.Duration(finishData.Time-msg.CreatedAt) * time.Second
			}

			header := NewSectionHeaderItem(
				fmt.Sprintf("%s-header", msg.ID),
				modelName,
				duration,
				sty,
			)
			items = append(items, header)
		}

		// Add thinking content if present
		reasoning := msg.ReasoningContent()
		if strings.TrimSpace(reasoning.Thinking) != "" {
			duration := time.Duration(0)
			if reasoning.StartedAt > 0 && reasoning.FinishedAt > 0 {
				duration = time.Duration(reasoning.FinishedAt-reasoning.StartedAt) * time.Second
			}

			item := NewThinkingItem(
				fmt.Sprintf("%s-thinking", msg.ID),
				reasoning.Thinking,
				duration,
				reasoning.FinishedAt > 0,
				sty,
			)
			items = append(items, item)
		}

		// Add main text content
		content := msg.Content().String()
		finished := msg.IsFinished()

		// Handle special finish states
		if finished && content == "" && finishData != nil {
			switch finishData.Reason {
			case message.FinishReasonEndTurn:
				// No content to show
			case message.FinishReasonCanceled:
				item := NewMessageContentItem(
					fmt.Sprintf("%s-content", msg.ID),
					"*Canceled*",
					msg.Role,
					true,
					sty,
				)
				items = append(items, item)
			case message.FinishReasonError:
				// Render error
				errTag := sty.Chat.Message.ErrorTag.Render("ERROR")
				truncated := ansi.Truncate(finishData.Message, 100, "...")
				title := fmt.Sprintf("%s %s", errTag, sty.Chat.Message.ErrorTitle.Render(truncated))
				details := sty.Chat.Message.ErrorDetails.Render(finishData.Details)
				errorContent := fmt.Sprintf("%s\n\n%s", title, details)

				item := NewMessageContentItem(
					fmt.Sprintf("%s-error", msg.ID),
					errorContent,
					msg.Role,
					false,
					sty,
				)
				items = append(items, item)
			}
		} else if content != "" {
			item := NewMessageContentItem(
				fmt.Sprintf("%s-content", msg.ID),
				content,
				msg.Role,
				true, // Assistant messages are markdown
				sty,
			)
			items = append(items, item)
		}

		// Add tool calls
		toolCalls := msg.ToolCalls()

		// Use passed-in tool results map (if nil, use empty map)
		resultMap := toolResults
		if resultMap == nil {
			resultMap = make(map[string]message.ToolResult)
		}

		for _, tc := range toolCalls {
			result, hasResult := resultMap[tc.ID]
			if !hasResult {
				result = message.ToolResult{}
			}

			item := NewToolCallItem(
				fmt.Sprintf("%s-tool-%s", msg.ID, tc.ID),
				tc,
				result,
				false, // cancelled state would need to be tracked separately
				false, // nested state would be detected from tool results
				sty,
			)

			items = append(items, item)
		}

		return items
	}

	return items
}

// BuildToolResultMap creates a map of tool call IDs to their results from a list of messages.
// Tool result messages (role == message.Tool) contain the results that should be linked
// to tool calls in assistant messages.
func BuildToolResultMap(messages []*message.Message) map[string]message.ToolResult {
	resultMap := make(map[string]message.ToolResult)
	for _, msg := range messages {
		if msg.Role == message.Tool {
			for _, result := range msg.ToolResults() {
				if result.ToolCallID != "" {
					resultMap[result.ToolCallID] = result
				}
			}
		}
	}
	return resultMap
}
