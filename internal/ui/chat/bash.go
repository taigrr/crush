package chat

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// BashToolMessageItem is a message item that represents a bash tool call.
type BashToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*BashToolMessageItem)(nil)

// NewBashToolMessageItem creates a new [BashToolMessageItem].
func NewBashToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(
		sty,
		toolCall,
		result,
		&BashToolRenderContext{},
		canceled,
	)
}

// BashToolRenderContext holds context for rendering bash tool messages.
//
// It implements the [ToolRenderer] interface.
type BashToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (b *BashToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	const toolName = "Bash"
	if !opts.ToolCall.Finished && !opts.Canceled {
		return pendingTool(sty, toolName, opts.Anim)
	}

	var params tools.BashParams
	var cmd string
	err := json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	if err != nil {
		cmd = "failed to parse command"
	} else {
		cmd = strings.ReplaceAll(params.Command, "\n", " ")
		cmd = strings.ReplaceAll(cmd, "\t", "    ")
	}

	// TODO: if the tool is being run in the background use the background job renderer

	toolParams := []string{
		cmd,
	}

	if params.RunInBackground {
		toolParams = append(toolParams, "background", "true")
	}

	header := toolHeader(sty, opts.Status(), "Bash", cappedWidth, toolParams...)

	if opts.Nested {
		return header
	}

	earlyStateContent, ok := toolEarlyStateContent(sty, opts, cappedWidth)

	// If this is OK that means that the tool is not done yet or it was canceled
	if ok {
		return strings.Join([]string{header, "", earlyStateContent}, "\n")
	}

	if opts.Result == nil {
		// We should not get here!
		return header
	}

	var meta tools.BashResponseMetadata
	err = json.Unmarshal([]byte(opts.Result.Metadata), &meta)

	var output string
	if err != nil {
		output = "failed to parse output"
	}
	output = meta.Output
	if output == "" && opts.Result.Content != tools.BashNoOutput {
		output = opts.Result.Content
	}

	if output == "" {
		return header
	}

	bodyWidth := cappedWidth - toolBodyLeftPaddingTotal

	output = sty.Tool.Body.Render(toolOutputPlainContent(sty, output, bodyWidth, opts.Expanded))

	return strings.Join([]string{header, "", output}, "\n")
}
