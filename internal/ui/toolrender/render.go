package toolrender

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/ansiext"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// responseContextHeight limits the number of lines displayed in tool output.
const responseContextHeight = 10

// RenderContext provides the context needed for rendering a tool call.
type RenderContext struct {
	Call      message.ToolCall
	Result    message.ToolResult
	Cancelled bool
	IsNested  bool
	Width     int
	Styles    *styles.Styles
}

// TextWidth returns the available width for content accounting for borders.
func (rc *RenderContext) TextWidth() int {
	if rc.IsNested {
		return rc.Width - 6
	}
	return rc.Width - 5
}

// Fit truncates content to fit within the specified width with ellipsis.
func (rc *RenderContext) Fit(content string, width int) string {
	lineStyle := rc.Styles.Muted
	dots := lineStyle.Render("…")
	return ansi.Truncate(content, width, dots)
}

// Render renders a tool call using the appropriate renderer based on tool name.
func Render(ctx *RenderContext) string {
	switch ctx.Call.Name {
	case tools.ViewToolName:
		return renderView(ctx)
	case tools.EditToolName:
		return renderEdit(ctx)
	case tools.MultiEditToolName:
		return renderMultiEdit(ctx)
	case tools.WriteToolName:
		return renderWrite(ctx)
	case tools.BashToolName:
		return renderBash(ctx)
	case tools.JobOutputToolName:
		return renderJobOutput(ctx)
	case tools.JobKillToolName:
		return renderJobKill(ctx)
	case tools.FetchToolName:
		return renderSimpleFetch(ctx)
	case tools.AgenticFetchToolName:
		return renderAgenticFetch(ctx)
	case tools.WebFetchToolName:
		return renderWebFetch(ctx)
	case tools.DownloadToolName:
		return renderDownload(ctx)
	case tools.GlobToolName:
		return renderGlob(ctx)
	case tools.GrepToolName:
		return renderGrep(ctx)
	case tools.LSToolName:
		return renderLS(ctx)
	case tools.SourcegraphToolName:
		return renderSourcegraph(ctx)
	case tools.DiagnosticsToolName:
		return renderDiagnostics(ctx)
	case agent.AgentToolName:
		return renderAgent(ctx)
	default:
		return renderGeneric(ctx)
	}
}

// Helper functions

func unmarshalParams(input string, target any) error {
	return json.Unmarshal([]byte(input), target)
}

type paramBuilder struct {
	args []string
}

func newParamBuilder() *paramBuilder {
	return &paramBuilder{args: make([]string, 0)}
}

func (pb *paramBuilder) addMain(value string) *paramBuilder {
	if value != "" {
		pb.args = append(pb.args, value)
	}
	return pb
}

func (pb *paramBuilder) addKeyValue(key, value string) *paramBuilder {
	if value != "" {
		pb.args = append(pb.args, key, value)
	}
	return pb
}

func (pb *paramBuilder) addFlag(key string, value bool) *paramBuilder {
	if value {
		pb.args = append(pb.args, key, "true")
	}
	return pb
}

func (pb *paramBuilder) build() []string {
	return pb.args
}

func formatNonZero[T comparable](value T) string {
	var zero T
	if value == zero {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func makeHeader(ctx *RenderContext, toolName string, args []string) string {
	if ctx.IsNested {
		return makeNestedHeader(ctx, toolName, args)
	}
	s := ctx.Styles
	var icon string
	if ctx.Result.ToolCallID != "" {
		if ctx.Result.IsError {
			icon = s.Tool.IconError.Render()
		} else {
			icon = s.Tool.IconSuccess.Render()
		}
	} else if ctx.Cancelled {
		icon = s.Tool.IconCancelled.Render()
	} else {
		icon = s.Tool.IconPending.Render()
	}
	tool := s.Tool.NameNormal.Render(toolName)
	prefix := fmt.Sprintf("%s %s ", icon, tool)
	return prefix + renderParamList(ctx, false, ctx.TextWidth()-lipgloss.Width(prefix), args...)
}

func makeNestedHeader(ctx *RenderContext, toolName string, args []string) string {
	s := ctx.Styles
	var icon string
	if ctx.Result.ToolCallID != "" {
		if ctx.Result.IsError {
			icon = s.Tool.IconError.Render()
		} else {
			icon = s.Tool.IconSuccess.Render()
		}
	} else if ctx.Cancelled {
		icon = s.Tool.IconCancelled.Render()
	} else {
		icon = s.Tool.IconPending.Render()
	}
	tool := s.Tool.NameNested.Render(toolName)
	prefix := fmt.Sprintf("%s %s ", icon, tool)
	return prefix + renderParamList(ctx, true, ctx.TextWidth()-lipgloss.Width(prefix), args...)
}

func renderParamList(ctx *RenderContext, nested bool, paramsWidth int, params ...string) string {
	s := ctx.Styles
	if len(params) == 0 {
		return ""
	}
	mainParam := params[0]
	if paramsWidth >= 0 && lipgloss.Width(mainParam) > paramsWidth {
		mainParam = ansi.Truncate(mainParam, paramsWidth, "…")
	}

	if len(params) == 1 {
		return s.Tool.ParamMain.Render(mainParam)
	}
	otherParams := params[1:]
	if len(otherParams)%2 != 0 {
		otherParams = append(otherParams, "")
	}
	parts := make([]string, 0, len(otherParams)/2)
	for i := 0; i < len(otherParams); i += 2 {
		key := otherParams[i]
		value := otherParams[i+1]
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}

	partsRendered := strings.Join(parts, ", ")
	remainingWidth := paramsWidth - lipgloss.Width(partsRendered) - 3
	if remainingWidth < 30 {
		return s.Tool.ParamMain.Render(mainParam)
	}

	if len(parts) > 0 {
		mainParam = fmt.Sprintf("%s (%s)", mainParam, strings.Join(parts, ", "))
	}

	return s.Tool.ParamMain.Render(ansi.Truncate(mainParam, paramsWidth, "…"))
}

func earlyState(ctx *RenderContext, header string) (string, bool) {
	s := ctx.Styles
	message := ""
	switch {
	case ctx.Result.IsError:
		message = renderToolError(ctx)
	case ctx.Cancelled:
		message = s.Tool.StateCancelled.Render("Canceled.")
	case ctx.Result.ToolCallID == "":
		message = s.Tool.StateWaiting.Render("Waiting for tool response...")
	default:
		return "", false
	}

	message = s.Tool.BodyPadding.Render(message)
	return lipgloss.JoinVertical(lipgloss.Left, header, "", message), true
}

func renderToolError(ctx *RenderContext) string {
	s := ctx.Styles
	errTag := s.Tool.ErrorTag.Render("ERROR")
	msg := ctx.Result.Content
	if msg == "" {
		msg = "An error occurred"
	}
	truncated := ansi.Truncate(msg, ctx.TextWidth()-3-lipgloss.Width(errTag), "…")
	return errTag + " " + s.Tool.ErrorMessage.Render(truncated)
}

func joinHeaderBody(ctx *RenderContext, header, body string) string {
	s := ctx.Styles
	if body == "" {
		return header
	}
	body = s.Tool.BodyPadding.Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, header, "", body)
}

func renderWithParams(ctx *RenderContext, toolName string, args []string, contentRenderer func() string) string {
	header := makeHeader(ctx, toolName, args)
	if ctx.IsNested {
		return header
	}
	if res, done := earlyState(ctx, header); done {
		return res
	}
	body := contentRenderer()
	return joinHeaderBody(ctx, header, body)
}

func renderError(ctx *RenderContext, message string) string {
	s := ctx.Styles
	header := makeHeader(ctx, prettifyToolName(ctx.Call.Name), []string{})
	errorTag := s.Tool.ErrorTag.Render("ERROR")
	message = s.Tool.ErrorMessage.Render(ctx.Fit(message, ctx.TextWidth()-3-lipgloss.Width(errorTag)))
	return joinHeaderBody(ctx, header, errorTag+" "+message)
}

func renderPlainContent(ctx *RenderContext, content string) string {
	s := ctx.Styles
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\t", "    ")
	content = strings.TrimSpace(content)
	lines := strings.Split(content, "\n")

	width := ctx.TextWidth() - 2
	var out []string
	for i, ln := range lines {
		if i >= responseContextHeight {
			break
		}
		ln = ansiext.Escape(ln)
		ln = " " + ln
		if len(ln) > width {
			ln = ctx.Fit(ln, width)
		}
		out = append(out, s.Tool.ContentLine.Width(width).Render(ln))
	}

	if len(lines) > responseContextHeight {
		out = append(out, s.Tool.ContentTruncation.Width(width).Render(fmt.Sprintf("… (%d lines)", len(lines)-responseContextHeight)))
	}

	return strings.Join(out, "\n")
}

func renderMarkdownContent(ctx *RenderContext, content string) string {
	s := ctx.Styles
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\t", "    ")
	content = strings.TrimSpace(content)

	width := ctx.TextWidth() - 2
	width = min(width, 120)

	renderer := common.PlainMarkdownRenderer(width)
	rendered, err := renderer.Render(content)
	if err != nil {
		return renderPlainContent(ctx, content)
	}

	lines := strings.Split(rendered, "\n")

	var out []string
	for i, ln := range lines {
		if i >= responseContextHeight {
			break
		}
		out = append(out, ln)
	}

	style := s.Tool.ContentLine
	if len(lines) > responseContextHeight {
		out = append(out, s.Tool.ContentTruncation.
			Width(width-2).
			Render(fmt.Sprintf("… (%d lines)", len(lines)-responseContextHeight)))
	}

	return style.Render(strings.Join(out, "\n"))
}

func renderCodeContent(ctx *RenderContext, path, content string, offset int) string {
	s := ctx.Styles
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\t", "    ")
	truncated := truncateHeight(content, responseContextHeight)

	lines := strings.Split(truncated, "\n")
	for i, ln := range lines {
		lines[i] = ansiext.Escape(ln)
	}

	bg := s.Tool.ContentCodeBg
	highlighted, _ := common.SyntaxHighlight(ctx.Styles, strings.Join(lines, "\n"), path, bg)
	lines = strings.Split(highlighted, "\n")

	width := ctx.TextWidth() - 2
	gutterWidth := getDigits(offset+len(lines)) + 1

	var out []string
	for i, ln := range lines {
		lineNum := fmt.Sprintf("%*d", gutterWidth, offset+i+1)
		gutter := s.Subtle.Render(lineNum + " ")
		ln = " " + ln
		if lipgloss.Width(gutter+ln) > width {
			ln = ctx.Fit(ln, width-lipgloss.Width(gutter))
		}
		out = append(out, s.Tool.ContentCodeLine.Width(width).Render(gutter+ln))
	}

	contentLines := strings.Split(content, "\n")
	if len(contentLines) > responseContextHeight {
		out = append(out, s.Tool.ContentTruncation.Width(width).Render(fmt.Sprintf("… (%d lines)", len(contentLines)-responseContextHeight)))
	}

	return strings.Join(out, "\n")
}

func getDigits(n int) int {
	if n == 0 {
		return 1
	}
	if n < 0 {
		n = -n
	}

	digits := 0
	for n > 0 {
		n /= 10
		digits++
	}

	return digits
}

func truncateHeight(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	return strings.Join(lines[:maxLines], "\n")
}

func prettifyToolName(name string) string {
	switch name {
	case "agent":
		return "Agent"
	case "bash":
		return "Bash"
	case "job_output":
		return "Job: Output"
	case "job_kill":
		return "Job: Kill"
	case "download":
		return "Download"
	case "edit":
		return "Edit"
	case "multiedit":
		return "Multi-Edit"
	case "fetch":
		return "Fetch"
	case "agentic_fetch":
		return "Agentic Fetch"
	case "web_fetch":
		return "Fetching"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	case "ls":
		return "List"
	case "sourcegraph":
		return "Sourcegraph"
	case "view":
		return "View"
	case "write":
		return "Write"
	case "lsp_references":
		return "Find References"
	case "lsp_diagnostics":
		return "Diagnostics"
	default:
		parts := strings.Split(name, "_")
		for i := range parts {
			if len(parts[i]) > 0 {
				parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
			}
		}
		return strings.Join(parts, " ")
	}
}

// Tool-specific renderers

func renderGeneric(ctx *RenderContext) string {
	return renderWithParams(ctx, prettifyToolName(ctx.Call.Name), []string{ctx.Call.Input}, func() string {
		return renderPlainContent(ctx, ctx.Result.Content)
	})
}

func renderView(ctx *RenderContext) string {
	var params tools.ViewParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid view parameters")
	}

	file := fsext.PrettyPath(params.FilePath)
	args := newParamBuilder().
		addMain(file).
		addKeyValue("limit", formatNonZero(params.Limit)).
		addKeyValue("offset", formatNonZero(params.Offset)).
		build()

	return renderWithParams(ctx, "View", args, func() string {
		var meta tools.ViewResponseMetadata
		if err := unmarshalParams(ctx.Result.Metadata, &meta); err != nil {
			return renderPlainContent(ctx, ctx.Result.Content)
		}
		return renderCodeContent(ctx, meta.FilePath, meta.Content, params.Offset)
	})
}

func renderEdit(ctx *RenderContext) string {
	s := ctx.Styles
	var params tools.EditParams
	var args []string
	if err := unmarshalParams(ctx.Call.Input, &params); err == nil {
		file := fsext.PrettyPath(params.FilePath)
		args = newParamBuilder().addMain(file).build()
	}

	return renderWithParams(ctx, "Edit", args, func() string {
		var meta tools.EditResponseMetadata
		if err := unmarshalParams(ctx.Result.Metadata, &meta); err != nil {
			return renderPlainContent(ctx, ctx.Result.Content)
		}

		formatter := common.DiffFormatter(ctx.Styles).
			Before(fsext.PrettyPath(params.FilePath), meta.OldContent).
			After(fsext.PrettyPath(params.FilePath), meta.NewContent).
			Width(ctx.TextWidth() - 2)
		if ctx.TextWidth() > 120 {
			formatter = formatter.Split()
		}
		formatted := formatter.String()
		if lipgloss.Height(formatted) > responseContextHeight {
			contentLines := strings.Split(formatted, "\n")
			truncateMessage := s.Tool.DiffTruncation.
				Width(ctx.TextWidth() - 2).
				Render(fmt.Sprintf("… (%d lines)", len(contentLines)-responseContextHeight))
			formatted = strings.Join(contentLines[:responseContextHeight], "\n") + "\n" + truncateMessage
		}
		return formatted
	})
}

func renderMultiEdit(ctx *RenderContext) string {
	s := ctx.Styles
	var params tools.MultiEditParams
	var args []string
	if err := unmarshalParams(ctx.Call.Input, &params); err == nil {
		file := fsext.PrettyPath(params.FilePath)
		args = newParamBuilder().
			addMain(file).
			addKeyValue("edits", fmt.Sprintf("%d", len(params.Edits))).
			build()
	}

	return renderWithParams(ctx, "Multi-Edit", args, func() string {
		var meta tools.MultiEditResponseMetadata
		if err := unmarshalParams(ctx.Result.Metadata, &meta); err != nil {
			return renderPlainContent(ctx, ctx.Result.Content)
		}

		formatter := common.DiffFormatter(ctx.Styles).
			Before(fsext.PrettyPath(params.FilePath), meta.OldContent).
			After(fsext.PrettyPath(params.FilePath), meta.NewContent).
			Width(ctx.TextWidth() - 2)
		if ctx.TextWidth() > 120 {
			formatter = formatter.Split()
		}
		formatted := formatter.String()
		if lipgloss.Height(formatted) > responseContextHeight {
			contentLines := strings.Split(formatted, "\n")
			truncateMessage := s.Tool.DiffTruncation.
				Width(ctx.TextWidth() - 2).
				Render(fmt.Sprintf("… (%d lines)", len(contentLines)-responseContextHeight))
			formatted = strings.Join(contentLines[:responseContextHeight], "\n") + "\n" + truncateMessage
		}

		// Add note about failed edits if any.
		if len(meta.EditsFailed) > 0 {
			noteTag := s.Tool.NoteTag.Render("NOTE")
			noteMsg := s.Tool.NoteMessage.Render(
				fmt.Sprintf("%d of %d edits failed", len(meta.EditsFailed), len(params.Edits)))
			formatted = formatted + "\n\n" + noteTag + " " + noteMsg
		}

		return formatted
	})
}

func renderWrite(ctx *RenderContext) string {
	var params tools.WriteParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid write parameters")
	}

	file := fsext.PrettyPath(params.FilePath)
	args := newParamBuilder().addMain(file).build()

	return renderWithParams(ctx, "Write", args, func() string {
		return renderCodeContent(ctx, params.FilePath, params.Content, 0)
	})
}

func renderBash(ctx *RenderContext) string {
	var params tools.BashParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid bash parameters")
	}

	cmd := strings.ReplaceAll(params.Command, "\n", " ")
	cmd = strings.ReplaceAll(cmd, "\t", "    ")
	args := newParamBuilder().
		addMain(cmd).
		addFlag("background", params.RunInBackground).
		build()

	if ctx.Call.Finished {
		var meta tools.BashResponseMetadata
		_ = unmarshalParams(ctx.Result.Metadata, &meta)
		if meta.Background {
			description := cmp.Or(meta.Description, params.Command)
			width := ctx.TextWidth()
			if ctx.IsNested {
				width -= 4
			}
			header := makeJobHeader(ctx, "Start", fmt.Sprintf("PID %s", meta.ShellID), description, width)
			if ctx.IsNested {
				return header
			}
			if res, done := earlyState(ctx, header); done {
				return res
			}
			content := "Command: " + params.Command + "\n" + ctx.Result.Content
			body := renderPlainContent(ctx, content)
			return joinHeaderBody(ctx, header, body)
		}
	}

	return renderWithParams(ctx, "Bash", args, func() string {
		var meta tools.BashResponseMetadata
		if err := unmarshalParams(ctx.Result.Metadata, &meta); err != nil {
			return renderPlainContent(ctx, ctx.Result.Content)
		}
		if meta.Output == "" && ctx.Result.Content != tools.BashNoOutput {
			meta.Output = ctx.Result.Content
		}

		if meta.Output == "" {
			return ""
		}
		return renderPlainContent(ctx, meta.Output)
	})
}

func makeJobHeader(ctx *RenderContext, action, pid, description string, width int) string {
	s := ctx.Styles
	icon := s.Tool.JobIconPending.Render(styles.ToolPending)
	if ctx.Result.ToolCallID != "" {
		if ctx.Result.IsError {
			icon = s.Tool.JobIconError.Render(styles.ToolError)
		} else {
			icon = s.Tool.JobIconSuccess.Render(styles.ToolSuccess)
		}
	} else if ctx.Cancelled {
		icon = s.Muted.Render(styles.ToolPending)
	}

	toolName := s.Tool.JobToolName.Render("Bash")
	actionPart := s.Tool.JobAction.Render(action)
	pidPart := s.Tool.JobPID.Render(pid)

	prefix := fmt.Sprintf("%s %s %s %s ", icon, toolName, actionPart, pidPart)
	remainingWidth := width - lipgloss.Width(prefix)

	descDisplay := ansi.Truncate(description, remainingWidth, "…")
	descDisplay = s.Tool.JobDescription.Render(descDisplay)

	return prefix + descDisplay
}

func renderJobOutput(ctx *RenderContext) string {
	var params tools.JobOutputParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid job output parameters")
	}

	width := ctx.TextWidth()
	if ctx.IsNested {
		width -= 4
	}

	var meta tools.JobOutputResponseMetadata
	_ = unmarshalParams(ctx.Result.Metadata, &meta)
	description := cmp.Or(meta.Description, meta.Command)

	header := makeJobHeader(ctx, "Output", fmt.Sprintf("PID %s", params.ShellID), description, width)
	if ctx.IsNested {
		return header
	}
	if res, done := earlyState(ctx, header); done {
		return res
	}
	body := renderPlainContent(ctx, ctx.Result.Content)
	return joinHeaderBody(ctx, header, body)
}

func renderJobKill(ctx *RenderContext) string {
	var params tools.JobKillParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid job kill parameters")
	}

	width := ctx.TextWidth()
	if ctx.IsNested {
		width -= 4
	}

	var meta tools.JobKillResponseMetadata
	_ = unmarshalParams(ctx.Result.Metadata, &meta)
	description := cmp.Or(meta.Description, meta.Command)

	header := makeJobHeader(ctx, "Kill", fmt.Sprintf("PID %s", params.ShellID), description, width)
	if ctx.IsNested {
		return header
	}
	if res, done := earlyState(ctx, header); done {
		return res
	}
	body := renderPlainContent(ctx, ctx.Result.Content)
	return joinHeaderBody(ctx, header, body)
}

func renderSimpleFetch(ctx *RenderContext) string {
	var params tools.FetchParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid fetch parameters")
	}

	args := newParamBuilder().
		addMain(params.URL).
		addKeyValue("format", params.Format).
		addKeyValue("timeout", formatNonZero(params.Timeout)).
		build()

	return renderWithParams(ctx, "Fetch", args, func() string {
		path := "file." + params.Format
		return renderCodeContent(ctx, path, ctx.Result.Content, 0)
	})
}

func renderAgenticFetch(ctx *RenderContext) string {
	// TODO: Implement nested tool call rendering with tree.
	return renderGeneric(ctx)
}

func renderWebFetch(ctx *RenderContext) string {
	var params tools.WebFetchParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid web fetch parameters")
	}

	args := newParamBuilder().addMain(params.URL).build()

	return renderWithParams(ctx, "Fetching", args, func() string {
		return renderMarkdownContent(ctx, ctx.Result.Content)
	})
}

func renderDownload(ctx *RenderContext) string {
	var params tools.DownloadParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid download parameters")
	}

	args := newParamBuilder().
		addMain(params.URL).
		addKeyValue("file", fsext.PrettyPath(params.FilePath)).
		addKeyValue("timeout", formatNonZero(params.Timeout)).
		build()

	return renderWithParams(ctx, "Download", args, func() string {
		return renderPlainContent(ctx, ctx.Result.Content)
	})
}

func renderGlob(ctx *RenderContext) string {
	var params tools.GlobParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid glob parameters")
	}

	args := newParamBuilder().
		addMain(params.Pattern).
		addKeyValue("path", params.Path).
		build()

	return renderWithParams(ctx, "Glob", args, func() string {
		return renderPlainContent(ctx, ctx.Result.Content)
	})
}

func renderGrep(ctx *RenderContext) string {
	var params tools.GrepParams
	var args []string
	if err := unmarshalParams(ctx.Call.Input, &params); err == nil {
		args = newParamBuilder().
			addMain(params.Pattern).
			addKeyValue("path", params.Path).
			addKeyValue("include", params.Include).
			addFlag("literal", params.LiteralText).
			build()
	}

	return renderWithParams(ctx, "Grep", args, func() string {
		return renderPlainContent(ctx, ctx.Result.Content)
	})
}

func renderLS(ctx *RenderContext) string {
	var params tools.LSParams
	path := cmp.Or(params.Path, ".")
	args := newParamBuilder().addMain(path).build()

	if err := unmarshalParams(ctx.Call.Input, &params); err == nil && params.Path != "" {
		args = newParamBuilder().addMain(params.Path).build()
	}

	return renderWithParams(ctx, "List", args, func() string {
		return renderPlainContent(ctx, ctx.Result.Content)
	})
}

func renderSourcegraph(ctx *RenderContext) string {
	var params tools.SourcegraphParams
	if err := unmarshalParams(ctx.Call.Input, &params); err != nil {
		return renderError(ctx, "Invalid sourcegraph parameters")
	}

	args := newParamBuilder().
		addMain(params.Query).
		addKeyValue("count", formatNonZero(params.Count)).
		addKeyValue("context", formatNonZero(params.ContextWindow)).
		build()

	return renderWithParams(ctx, "Sourcegraph", args, func() string {
		return renderPlainContent(ctx, ctx.Result.Content)
	})
}

func renderDiagnostics(ctx *RenderContext) string {
	args := newParamBuilder().addMain("project").build()

	return renderWithParams(ctx, "Diagnostics", args, func() string {
		return renderPlainContent(ctx, ctx.Result.Content)
	})
}

func renderAgent(ctx *RenderContext) string {
	s := ctx.Styles
	var params agent.AgentParams
	unmarshalParams(ctx.Call.Input, &params)

	prompt := params.Prompt
	prompt = strings.ReplaceAll(prompt, "\n", " ")

	header := makeHeader(ctx, "Agent", []string{})
	if res, done := earlyState(ctx, header); ctx.Cancelled && done {
		return res
	}
	taskTag := s.Tool.AgentTaskTag.Render("Task")
	remainingWidth := ctx.TextWidth() - lipgloss.Width(header) - lipgloss.Width(taskTag) - 2
	remainingWidth = min(remainingWidth, 120-lipgloss.Width(taskTag)-2)
	prompt = s.Tool.AgentPrompt.Width(remainingWidth).Render(prompt)
	header = lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		lipgloss.JoinHorizontal(
			lipgloss.Left,
			taskTag,
			" ",
			prompt,
		),
	)
	childTools := tree.Root(header)

	// TODO: Render nested tool calls when available.

	parts := []string{
		childTools.Enumerator(roundedEnumeratorWithWidth(2, lipgloss.Width(taskTag)-5)).String(),
	}

	if ctx.Result.ToolCallID == "" {
		// Pending state - would show animation in TUI.
		parts = append(parts, "", s.Subtle.Render("Working..."))
	}

	header = lipgloss.JoinVertical(
		lipgloss.Left,
		parts...,
	)

	if ctx.Result.ToolCallID == "" {
		return header
	}

	body := renderMarkdownContent(ctx, ctx.Result.Content)
	return joinHeaderBody(ctx, header, body)
}

func roundedEnumeratorWithWidth(width int, offset int) func(tree.Children, int) string {
	return func(children tree.Children, i int) string {
		if children.Length()-1 == i {
			return strings.Repeat(" ", offset) + "└" + strings.Repeat("─", width-1) + " "
		}
		return strings.Repeat(" ", offset) + "├" + strings.Repeat("─", width-1) + " "
	}
}
