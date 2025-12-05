package model

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/ui/toolrender"
)

// Identifiable is an interface for items that can provide a unique identifier.
type Identifiable interface {
	ID() string
}

// MessageItem represents a [message.Message] item that can be displayed in the
// UI and be part of a [list.List] identifiable by a unique ID.
type MessageItem interface {
	list.Item
	list.Focusable
	list.Highlightable
	Identifiable
}

// MessageContentItem represents rendered message content (text, markdown, errors, etc).
type MessageContentItem struct {
	list.BaseFocusable
	list.BaseHighlightable
	id         string
	content    string
	isMarkdown bool
	maxWidth   int
	cache      map[int]string // Cache for rendered content at different widths
	sty        *styles.Styles
}

// NewMessageContentItem creates a new message content item.
func NewMessageContentItem(id, content string, isMarkdown bool, sty *styles.Styles) *MessageContentItem {
	m := &MessageContentItem{
		id:         id,
		content:    content,
		isMarkdown: isMarkdown,
		maxWidth:   120,
		cache:      make(map[int]string),
		sty:        sty,
	}
	m.InitHighlight()
	return m
}

// ID implements Identifiable.
func (m *MessageContentItem) ID() string {
	return m.id
}

// Height implements list.Item.
func (m *MessageContentItem) Height(width int) int {
	// Calculate content width accounting for frame size
	contentWidth := width
	if style := m.CurrentStyle(); style != nil {
		contentWidth -= style.GetHorizontalFrameSize()
	}

	rendered := m.render(contentWidth)

	// Apply focus/blur styling if configured to get accurate height
	if style := m.CurrentStyle(); style != nil {
		rendered = style.Render(rendered)
	}

	return strings.Count(rendered, "\n") + 1
}

// Draw implements list.Item.
func (m *MessageContentItem) Draw(scr uv.Screen, area uv.Rectangle) {
	width := area.Dx()
	height := area.Dy()

	// Calculate content width accounting for frame size
	contentWidth := width
	style := m.CurrentStyle()
	if style != nil {
		contentWidth -= style.GetHorizontalFrameSize()
	}

	rendered := m.render(contentWidth)

	// Apply focus/blur styling if configured
	if style != nil {
		rendered = style.Render(rendered)
	}

	// Create temp buffer to draw content with highlighting
	tempBuf := uv.NewScreenBuffer(width, height)

	// Draw the rendered content to temp buffer
	styled := uv.NewStyledString(rendered)
	styled.Draw(&tempBuf, uv.Rect(0, 0, width, height))

	// Apply highlighting if active
	m.ApplyHighlight(&tempBuf, width, height, style)

	// Copy temp buffer to actual screen at the target area
	tempBuf.Draw(scr, area)
}

// render renders the content at the given width, using cache if available.
func (m *MessageContentItem) render(width int) string {
	// Cap width to maxWidth for markdown
	cappedWidth := width
	if m.isMarkdown {
		cappedWidth = min(width, m.maxWidth)
	}

	// Check cache first
	if cached, ok := m.cache[cappedWidth]; ok {
		return cached
	}

	// Not cached - render now
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

	// Cache the result
	m.cache[cappedWidth] = rendered
	return rendered
}

// SetHighlight implements list.Highlightable and extends BaseHighlightable.
func (m *MessageContentItem) SetHighlight(startLine, startCol, endLine, endCol int) {
	m.BaseHighlightable.SetHighlight(startLine, startCol, endLine, endCol)
	// Clear cache when highlight changes
	m.cache = make(map[int]string)
}

// ToolCallItem represents a rendered tool call with its header and content.
type ToolCallItem struct {
	list.BaseFocusable
	list.BaseHighlightable
	id         string
	toolCall   message.ToolCall
	toolResult message.ToolResult
	cancelled  bool
	isNested   bool
	maxWidth   int
	cache      map[int]cachedToolRender // Cache for rendered content at different widths
	cacheKey   string                   // Key to invalidate cache when content changes
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
		cache:      make(map[int]cachedToolRender),
		cacheKey:   generateCacheKey(toolCall, toolResult, cancelled),
		sty:        sty,
	}
	t.InitHighlight()
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

// Height implements list.Item.
func (t *ToolCallItem) Height(width int) int {
	// Calculate content width accounting for frame size
	contentWidth := width
	frameSize := 0
	if style := t.CurrentStyle(); style != nil {
		frameSize = style.GetHorizontalFrameSize()
		contentWidth -= frameSize
	}

	cached := t.renderCached(contentWidth)

	// Add frame size to height if needed
	height := cached.height
	if frameSize > 0 {
		// Frame can add to height (borders, padding)
		if style := t.CurrentStyle(); style != nil {
			// Quick render to get accurate height with frame
			rendered := style.Render(cached.content)
			height = strings.Count(rendered, "\n") + 1
		}
	}

	return height
}

// Draw implements list.Item.
func (t *ToolCallItem) Draw(scr uv.Screen, area uv.Rectangle) {
	width := area.Dx()
	height := area.Dy()

	// Calculate content width accounting for frame size
	contentWidth := width
	style := t.CurrentStyle()
	if style != nil {
		contentWidth -= style.GetHorizontalFrameSize()
	}

	cached := t.renderCached(contentWidth)
	rendered := cached.content

	if style != nil {
		rendered = style.Render(rendered)
	}

	tempBuf := uv.NewScreenBuffer(width, height)
	styled := uv.NewStyledString(rendered)
	styled.Draw(&tempBuf, uv.Rect(0, 0, width, height))

	t.ApplyHighlight(&tempBuf, width, height, style)
	tempBuf.Draw(scr, area)
}

// renderCached renders the tool call at the given width with caching.
func (t *ToolCallItem) renderCached(width int) cachedToolRender {
	cappedWidth := min(width, t.maxWidth)

	// Check if we have a valid cache entry
	if cached, ok := t.cache[cappedWidth]; ok {
		return cached
	}

	// Render the tool call
	ctx := &toolrender.RenderContext{
		Call:      t.toolCall,
		Result:    t.toolResult,
		Cancelled: t.cancelled,
		IsNested:  t.isNested,
		Width:     cappedWidth,
		Styles:    t.sty,
	}

	rendered := toolrender.Render(ctx)
	height := strings.Count(rendered, "\n") + 1

	cached := cachedToolRender{
		content: rendered,
		height:  height,
	}
	t.cache[cappedWidth] = cached
	return cached
}

// SetHighlight implements list.Highlightable.
func (t *ToolCallItem) SetHighlight(startLine, startCol, endLine, endCol int) {
	t.BaseHighlightable.SetHighlight(startLine, startCol, endLine, endCol)
	// Clear cache when highlight changes
	t.cache = make(map[int]cachedToolRender)
}

// UpdateResult updates the tool result and invalidates the cache if needed.
func (t *ToolCallItem) UpdateResult(result message.ToolResult) {
	newKey := generateCacheKey(t.toolCall, result, t.cancelled)
	if newKey != t.cacheKey {
		t.toolResult = result
		t.cacheKey = newKey
		t.cache = make(map[int]cachedToolRender)
	}
}

// AttachmentItem represents a file attachment in a user message.
type AttachmentItem struct {
	list.BaseFocusable
	list.BaseHighlightable
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
	a.InitHighlight()
	return a
}

// ID implements Identifiable.
func (a *AttachmentItem) ID() string {
	return a.id
}

// Height implements list.Item.
func (a *AttachmentItem) Height(width int) int {
	return 1
}

// Draw implements list.Item.
func (a *AttachmentItem) Draw(scr uv.Screen, area uv.Rectangle) {
	width := area.Dx()
	height := area.Dy()

	// Calculate content width accounting for frame size
	contentWidth := width
	style := a.CurrentStyle()
	if style != nil {
		contentWidth -= style.GetHorizontalFrameSize()
	}

	const maxFilenameWidth = 10
	content := a.sty.Chat.Message.Attachment.Render(fmt.Sprintf(
		" %s %s ",
		styles.DocumentIcon,
		ansi.Truncate(a.filename, maxFilenameWidth, "..."),
	))

	if style != nil {
		content = style.Render(content)
	}

	tempBuf := uv.NewScreenBuffer(width, height)
	styled := uv.NewStyledString(content)
	styled.Draw(&tempBuf, uv.Rect(0, 0, width, height))

	a.ApplyHighlight(&tempBuf, width, height, style)
	tempBuf.Draw(scr, area)
}

// ThinkingItem represents thinking/reasoning content in assistant messages.
type ThinkingItem struct {
	list.BaseFocusable
	list.BaseHighlightable
	id       string
	thinking string
	duration time.Duration
	finished bool
	maxWidth int
	cache    map[int]string
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
		cache:    make(map[int]string),
		sty:      sty,
	}
	t.InitHighlight()
	return t
}

// ID implements Identifiable.
func (t *ThinkingItem) ID() string {
	return t.id
}

// Height implements list.Item.
func (t *ThinkingItem) Height(width int) int {
	// Calculate content width accounting for frame size
	contentWidth := width
	if style := t.CurrentStyle(); style != nil {
		contentWidth -= style.GetHorizontalFrameSize()
	}

	rendered := t.render(contentWidth)
	return strings.Count(rendered, "\n") + 1
}

// Draw implements list.Item.
func (t *ThinkingItem) Draw(scr uv.Screen, area uv.Rectangle) {
	width := area.Dx()
	height := area.Dy()

	// Calculate content width accounting for frame size
	contentWidth := width
	style := t.CurrentStyle()
	if style != nil {
		contentWidth -= style.GetHorizontalFrameSize()
	}

	rendered := t.render(contentWidth)

	if style != nil {
		rendered = style.Render(rendered)
	}

	tempBuf := uv.NewScreenBuffer(width, height)
	styled := uv.NewStyledString(rendered)
	styled.Draw(&tempBuf, uv.Rect(0, 0, width, height))

	t.ApplyHighlight(&tempBuf, width, height, style)
	tempBuf.Draw(scr, area)
}

// render renders the thinking content.
func (t *ThinkingItem) render(width int) string {
	cappedWidth := min(width, t.maxWidth)

	if cached, ok := t.cache[cappedWidth]; ok {
		return cached
	}

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

	t.cache[cappedWidth] = result
	return result
}

// SetHighlight implements list.Highlightable.
func (t *ThinkingItem) SetHighlight(startLine, startCol, endLine, endCol int) {
	t.BaseHighlightable.SetHighlight(startLine, startCol, endLine, endCol)
	t.cache = make(map[int]string)
}

// SectionHeaderItem represents a section header (e.g., assistant info).
type SectionHeaderItem struct {
	list.BaseFocusable
	list.BaseHighlightable
	id              string
	modelName       string
	duration        time.Duration
	isSectionHeader bool
	sty             *styles.Styles
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
	s.InitHighlight()
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

// Height implements list.Item.
func (s *SectionHeaderItem) Height(width int) int {
	return 1
}

// Draw implements list.Item.
func (s *SectionHeaderItem) Draw(scr uv.Screen, area uv.Rectangle) {
	width := area.Dx()
	height := area.Dy()

	// Calculate content width accounting for frame size
	contentWidth := width
	style := s.CurrentStyle()
	if style != nil {
		contentWidth -= style.GetHorizontalFrameSize()
	}

	infoMsg := s.sty.Subtle.Render(s.duration.String())
	icon := s.sty.Subtle.Render(styles.ModelIcon)
	modelFormatted := s.sty.Muted.Render(s.modelName)
	content := fmt.Sprintf("%s %s %s", icon, modelFormatted, infoMsg)

	content = s.sty.Chat.Message.SectionHeader.Render(content)

	if style != nil {
		content = style.Render(content)
	}

	tempBuf := uv.NewScreenBuffer(width, height)
	styled := uv.NewStyledString(content)
	styled.Draw(&tempBuf, uv.Rect(0, 0, width, height))

	s.ApplyHighlight(&tempBuf, width, height, style)
	tempBuf.Draw(scr, area)
}

// GetMessageItems extracts [MessageItem]s from a [message.Message]. It returns
// all parts of the message as [MessageItem]s.
//
// For assistant messages with tool calls, pass a toolResults map to link results.
// Use BuildToolResultMap to create this map from all messages in a session.
func GetMessageItems(msg *message.Message, toolResults map[string]message.ToolResult) []MessageItem {
	sty := styles.DefaultStyles()
	var items []MessageItem

	// Skip tool result messages - they're displayed inline with tool calls
	if msg.Role == message.Tool {
		return items
	}

	// Create base styles for the message
	var focusStyle, blurStyle lipgloss.Style
	if msg.Role == message.User {
		focusStyle = sty.Chat.Message.UserFocused
		blurStyle = sty.Chat.Message.UserBlurred
	} else {
		focusStyle = sty.Chat.Message.AssistantFocused
		blurStyle = sty.Chat.Message.AssistantBlurred
	}

	// Process user messages
	if msg.Role == message.User {
		// Add main text content
		content := msg.Content().String()
		if content != "" {
			item := NewMessageContentItem(
				fmt.Sprintf("%s-content", msg.ID),
				content,
				true, // User messages are markdown
				&sty,
			)
			item.SetFocusStyles(&focusStyle, &blurStyle)
			items = append(items, item)
		}

		// Add attachments
		for i, attachment := range msg.BinaryContent() {
			filename := filepath.Base(attachment.Path)
			item := NewAttachmentItem(
				fmt.Sprintf("%s-attachment-%d", msg.ID, i),
				filename,
				attachment.Path,
				&sty,
			)
			item.SetFocusStyles(&focusStyle, &blurStyle)
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
				&sty,
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
				&sty,
			)
			item.SetFocusStyles(&focusStyle, &blurStyle)
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
					true,
					&sty,
				)
				item.SetFocusStyles(&focusStyle, &blurStyle)
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
					false,
					&sty,
				)
				item.SetFocusStyles(&focusStyle, &blurStyle)
				items = append(items, item)
			}
		} else if content != "" {
			item := NewMessageContentItem(
				fmt.Sprintf("%s-content", msg.ID),
				content,
				true, // Assistant messages are markdown
				&sty,
			)
			item.SetFocusStyles(&focusStyle, &blurStyle)
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
				&sty,
			)

			// Tool calls use muted style with optional focus border
			item.SetFocusStyles(&sty.Chat.Message.ToolCallFocused, &sty.Chat.Message.ToolCallBlurred)

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
