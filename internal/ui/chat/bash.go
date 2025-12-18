package chat

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// BashToolRenderer renders a bash tool call.
func BashToolRenderer(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
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

	toolParams := []string{
		cmd,
	}

	if params.RunInBackground {
		toolParams = append(toolParams, "background", "true")
	}

	header := toolHeader(sty, opts.Status(), "Bash", cappedWidth, toolParams...)
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
