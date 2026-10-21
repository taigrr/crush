package chat

import (
	"encoding/json"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/fsext"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles"
)

// ReferencesToolMessageItem is a message item that represents a references tool call.
type ReferencesToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*ReferencesToolMessageItem)(nil)

// NewReferencesToolMessageItem creates a new [ReferencesToolMessageItem].
func NewReferencesToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, &ReferencesToolRenderContext{}, canceled)
}

// ReferencesToolRenderContext renders references tool messages.
type ReferencesToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (r *ReferencesToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := width
	if opts.IsPending() {
		return pendingTool(sty, "Find References", opts.Anim, opts.Compact)
	}

	var params tools.ReferencesParams
	_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	toolParams := []string{params.Symbol}
	if params.Path != "" {
		toolParams = append(toolParams, "path", fsext.PrettyPath(params.Path))
	}

	return renderPlainTool(sty, opts, "Find References", cappedWidth, toolParams...)
}
