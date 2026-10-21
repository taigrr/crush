package chat

import (
	"encoding/json"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles"
)

// LSPRestartToolMessageItem is a message item that represents a lsprestart tool call.
type LSPRestartToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*LSPRestartToolMessageItem)(nil)

// NewLSPRestartToolMessageItem creates a new [LSPRestartToolMessageItem].
func NewLSPRestartToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, &LSPRestartToolRenderContext{}, canceled)
}

// LSPRestartToolRenderContext renders lsprestart tool messages.
type LSPRestartToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (r *LSPRestartToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := width
	if opts.IsPending() {
		return pendingTool(sty, "Restart LSP", opts.Anim, opts.Compact)
	}

	var params tools.LSPRestartParams
	_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	var toolParams []string
	if params.Name != "" {
		toolParams = append(toolParams, params.Name)
	}

	return renderPlainTool(sty, opts, "Restart LSP", cappedWidth, toolParams...)
}
