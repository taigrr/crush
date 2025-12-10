package model

import (
	"fmt"
	"image"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
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
	BaseFocusable
	BaseHighlightable
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

// FocusStyle returns the focus style.
func (t *ToolCallItem) FocusStyle() lipgloss.Style {
	if t.focusStyle != nil {
		return *t.focusStyle
	}
	return lipgloss.Style{}
}

// BlurStyle returns the blur style.
func (t *ToolCallItem) BlurStyle() lipgloss.Style {
	if t.blurStyle != nil {
		return *t.blurStyle
	}
	return lipgloss.Style{}
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

	// return t.RenderWithHighlight(rendered, width, style)
}

// SetHighlight implements list.Highlightable.
func (t *ToolCallItem) SetHighlight(startLine, startCol, endLine, endCol int) {
	t.BaseHighlightable.SetHighlight(startLine, startCol, endLine, endCol)
}

// UpdateResult updates the tool result and invalidates the cache if needed.
func (t *ToolCallItem) UpdateResult(result message.ToolResult) {
	t.toolResult = result
}

// AttachmentItem represents a file attachment in a user message.
type AttachmentItem struct {
	BaseFocusable
	BaseHighlightable
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

// FocusStyle returns the focus style.
func (a *AttachmentItem) FocusStyle() lipgloss.Style {
	if a.focusStyle != nil {
		return *a.focusStyle
	}
	return lipgloss.Style{}
}

// BlurStyle returns the blur style.
func (a *AttachmentItem) BlurStyle() lipgloss.Style {
	if a.blurStyle != nil {
		return *a.blurStyle
	}
	return lipgloss.Style{}
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
			item.SetHighlightStyle(ToStyler(sty.TextSelection))
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

			item.SetHighlightStyle(ToStyler(sty.TextSelection))

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

// BaseFocusable provides common focus state and styling for items.
// Embed this type to add focus behavior to any item.
type BaseFocusable struct {
	focused    bool
	focusStyle *lipgloss.Style
	blurStyle  *lipgloss.Style
}

// Focus implements Focusable interface.
func (b *BaseFocusable) Focus(width int, content string) string {
	if b.focusStyle != nil {
		return b.focusStyle.Render(content)
	}
	return content
}

// Blur implements Focusable interface.
func (b *BaseFocusable) Blur(width int, content string) string {
	if b.blurStyle != nil {
		return b.blurStyle.Render(content)
	}
	return content
}

// Focus implements Focusable interface.
// func (b *BaseFocusable) Focus() {
// 	b.focused = true
// }

// Blur implements Focusable interface.
// func (b *BaseFocusable) Blur() {
// 	b.focused = false
// }

// Focused implements Focusable interface.
func (b *BaseFocusable) Focused() bool {
	return b.focused
}

// HasFocusStyles returns true if both focus and blur styles are configured.
func (b *BaseFocusable) HasFocusStyles() bool {
	return b.focusStyle != nil && b.blurStyle != nil
}

// CurrentStyle returns the current style based on focus state.
// Returns nil if no styles are configured, or if the current state's style is nil.
func (b *BaseFocusable) CurrentStyle() *lipgloss.Style {
	if b.focused {
		return b.focusStyle
	}
	return b.blurStyle
}

// SetFocusStyles sets the focus and blur styles.
func (b *BaseFocusable) SetFocusStyles(focusStyle, blurStyle *lipgloss.Style) {
	b.focusStyle = focusStyle
	b.blurStyle = blurStyle
}

// CellStyler defines a function that styles a [uv.Style].
type CellStyler func(uv.Style) uv.Style

// BaseHighlightable provides common highlight state for items.
// Embed this type to add highlight behavior to any item.
type BaseHighlightable struct {
	highlightStartLine int
	highlightStartCol  int
	highlightEndLine   int
	highlightEndCol    int
	highlightStyle     CellStyler
}

// SetHighlight implements Highlightable interface.
func (b *BaseHighlightable) SetHighlight(startLine, startCol, endLine, endCol int) {
	b.highlightStartLine = startLine
	b.highlightStartCol = startCol
	b.highlightEndLine = endLine
	b.highlightEndCol = endCol
}

// GetHighlight implements Highlightable interface.
func (b *BaseHighlightable) GetHighlight() (startLine, startCol, endLine, endCol int) {
	return b.highlightStartLine, b.highlightStartCol, b.highlightEndLine, b.highlightEndCol
}

// HasHighlight returns true if a highlight region is set.
func (b *BaseHighlightable) HasHighlight() bool {
	return b.highlightStartLine >= 0 || b.highlightStartCol >= 0 ||
		b.highlightEndLine >= 0 || b.highlightEndCol >= 0
}

// SetHighlightStyle sets the style function used for highlighting.
func (b *BaseHighlightable) SetHighlightStyle(style CellStyler) {
	b.highlightStyle = style
}

// GetHighlightStyle returns the current highlight style function.
func (b *BaseHighlightable) GetHighlightStyle() CellStyler {
	return b.highlightStyle
}

// InitHighlight initializes the highlight fields with default values.
func (b *BaseHighlightable) InitHighlight() {
	b.highlightStartLine = -1
	b.highlightStartCol = -1
	b.highlightEndLine = -1
	b.highlightEndCol = -1
	b.highlightStyle = ToStyler(lipgloss.NewStyle().Reverse(true))
}

// Highlight implements Highlightable interface.
func (b *BaseHighlightable) Highlight(width int, content string, startLine, startCol, endLine, endCol int) string {
	b.SetHighlight(startLine, startCol, endLine, endCol)
	return b.RenderWithHighlight(content, width, nil)
}

// RenderWithHighlight renders content with optional focus styling and highlighting.
// This is a helper that combines common rendering logic for all items.
// The content parameter should be the raw rendered content before focus styling.
// The style parameter should come from CurrentStyle() and may be nil.
func (b *BaseHighlightable) RenderWithHighlight(content string, width int, style *lipgloss.Style) string {
	// Apply focus/blur styling if configured
	rendered := content
	if style != nil {
		rendered = style.Render(rendered)
	}

	if !b.HasHighlight() {
		return rendered
	}

	height := lipgloss.Height(rendered)

	// Create temp buffer to draw content with highlighting
	tempBuf := uv.NewScreenBuffer(width, height)

	// Draw the rendered content to temp buffer
	styled := uv.NewStyledString(rendered)
	styled.Draw(&tempBuf, uv.Rect(0, 0, width, height))

	// Apply highlighting if active
	b.ApplyHighlight(&tempBuf, width, height, style)

	return tempBuf.Render()
}

// ApplyHighlight applies highlighting to a screen buffer.
// This should be called after drawing content to the buffer.
func (b *BaseHighlightable) ApplyHighlight(buf *uv.ScreenBuffer, width, height int, style *lipgloss.Style) {
	if b.highlightStartLine < 0 {
		return
	}

	var (
		topMargin, topBorder, topPadding          int
		rightMargin, rightBorder, rightPadding    int
		bottomMargin, bottomBorder, bottomPadding int
		leftMargin, leftBorder, leftPadding       int
	)
	if style != nil {
		topMargin, rightMargin, bottomMargin, leftMargin = style.GetMargin()
		topBorder, rightBorder, bottomBorder, leftBorder = style.GetBorderTopSize(),
			style.GetBorderRightSize(),
			style.GetBorderBottomSize(),
			style.GetBorderLeftSize()
		topPadding, rightPadding, bottomPadding, leftPadding = style.GetPadding()
	}

	slog.Info("Applying highlight",
		"highlightStartLine", b.highlightStartLine,
		"highlightStartCol", b.highlightStartCol,
		"highlightEndLine", b.highlightEndLine,
		"highlightEndCol", b.highlightEndCol,
		"width", width,
		"height", height,
		"margins", fmt.Sprintf("%d,%d,%d,%d", topMargin, rightMargin, bottomMargin, leftMargin),
		"borders", fmt.Sprintf("%d,%d,%d,%d", topBorder, rightBorder, bottomBorder, leftBorder),
		"paddings", fmt.Sprintf("%d,%d,%d,%d", topPadding, rightPadding, bottomPadding, leftPadding),
	)

	// Calculate content area offsets
	contentArea := image.Rectangle{
		Min: image.Point{
			X: leftMargin + leftBorder + leftPadding,
			Y: topMargin + topBorder + topPadding,
		},
		Max: image.Point{
			X: width - (rightMargin + rightBorder + rightPadding),
			Y: height - (bottomMargin + bottomBorder + bottomPadding),
		},
	}

	for y := b.highlightStartLine; y <= b.highlightEndLine && y < height; y++ {
		if y >= buf.Height() {
			break
		}

		line := buf.Line(y)

		// Determine column range for this line
		startCol := 0
		if y == b.highlightStartLine {
			startCol = min(b.highlightStartCol, len(line))
		}

		endCol := len(line)
		if y == b.highlightEndLine {
			endCol = min(b.highlightEndCol, len(line))
		}

		// Track last non-empty position as we go
		lastContentX := -1

		// Single pass: check content and track last non-empty position
		for x := startCol; x < endCol; x++ {
			cell := line.At(x)
			if cell == nil {
				continue
			}

			// Update last content position if non-empty
			if cell.Content != "" && cell.Content != " " {
				lastContentX = x
			}
		}

		// Only apply highlight up to last content position
		highlightEnd := endCol
		if lastContentX >= 0 {
			highlightEnd = lastContentX + 1
		} else if lastContentX == -1 {
			highlightEnd = startCol // No content on this line
		}

		// Apply highlight style only to cells with content
		for x := startCol; x < highlightEnd; x++ {
			if !image.Pt(x, y).In(contentArea) {
				continue
			}
			cell := line.At(x)
			cell.Style = b.highlightStyle(cell.Style)
		}
	}
}

// ToStyler converts a [lipgloss.Style] to a [CellStyler].
func ToStyler(lgStyle lipgloss.Style) CellStyler {
	return func(uv.Style) uv.Style {
		return ToStyle(lgStyle)
	}
}

// ToStyle converts an inline [lipgloss.Style] to a [uv.Style].
func ToStyle(lgStyle lipgloss.Style) uv.Style {
	var uvStyle uv.Style

	// Colors are already color.Color
	uvStyle.Fg = lgStyle.GetForeground()
	uvStyle.Bg = lgStyle.GetBackground()

	// Build attributes using bitwise OR
	var attrs uint8

	if lgStyle.GetBold() {
		attrs |= uv.AttrBold
	}

	if lgStyle.GetItalic() {
		attrs |= uv.AttrItalic
	}

	if lgStyle.GetUnderline() {
		uvStyle.Underline = uv.UnderlineSingle
	}

	if lgStyle.GetStrikethrough() {
		attrs |= uv.AttrStrikethrough
	}

	if lgStyle.GetFaint() {
		attrs |= uv.AttrFaint
	}

	if lgStyle.GetBlink() {
		attrs |= uv.AttrBlink
	}

	if lgStyle.GetReverse() {
		attrs |= uv.AttrReverse
	}

	uvStyle.Attrs = attrs

	return uvStyle
}
