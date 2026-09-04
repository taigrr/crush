package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/diff"
	"github.com/taigrr/crush/internal/fsext"
	"github.com/taigrr/crush/internal/hooks"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/stringext"
	"github.com/taigrr/crush/internal/ui/anim"
	"github.com/taigrr/crush/internal/ui/common"
	fimage "github.com/taigrr/crush/internal/ui/image"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
	_ "golang.org/x/image/webp" // WebP decoding.
)

// responseContextHeight limits the number of lines displayed in tool output.
const responseContextHeight = 10

// toolBodyLeftPaddingTotal represents the padding that should be applied to each tool body
const toolBodyLeftPaddingTotal = 2

const (
	defaultImageCols = 60
	defaultImageRows = 20
)

// ToolStatus represents the current state of a tool call.
type ToolStatus int

const (
	ToolStatusAwaitingPermission ToolStatus = iota
	ToolStatusRunning
	ToolStatusSuccess
	ToolStatusError
	ToolStatusCanceled
)

// ToolMessageItem represents a tool call message in the chat UI.
type ToolMessageItem interface {
	MessageItem

	ToolCall() message.ToolCall
	SetToolCall(tc message.ToolCall)
	SetResult(res *message.ToolResult)
	MessageID() string
	SetMessageID(id string)
	SetStatus(status ToolStatus)
	Status() ToolStatus
	// IsRunning reports whether the call is executing: input fully
	// streamed, no result yet, not canceled.
	IsRunning() bool
	SetImageConfig(cfg *ImageConfig)
	TransmitImage() tea.Cmd
}

// Compactable is an interface for tool items that can render in a compacted mode.
// When compact mode is enabled, tools render as a compact single-line header.
type Compactable interface {
	SetCompact(compact bool)
}

// SpinningState contains the state passed to SpinningFunc for custom spinning logic.
type SpinningState struct {
	ToolCall message.ToolCall
	Result   *message.ToolResult
	Status   ToolStatus
}

// IsCanceled returns true if the tool status is canceled.
func (s *SpinningState) IsCanceled() bool {
	return s.Status == ToolStatusCanceled
}

// HasResult returns true if the result is not nil.
func (s *SpinningState) HasResult() bool {
	return s.Result != nil
}

// SpinningFunc is a function type for custom spinning logic.
// Returns true if the tool should show the spinning animation.
type SpinningFunc func(state SpinningState) bool

// DefaultToolRenderContext implements the default [ToolRenderer] interface.
type DefaultToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (d *DefaultToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	return "TODO: Implement Tool Renderer For: " + opts.ToolCall.Name
}

// ToolRenderOpts contains the data needed to render a tool call.
type ToolRenderOpts struct {
	ToolCall        message.ToolCall
	Result          *message.ToolResult
	Anim            *anim.Anim
	ExpandedContent bool
	Compact         bool
	IsSpinning      bool
	Status          ToolStatus
	ImageConfig     *ImageConfig
	// RunningSince is when the tool call's input finished streaming and
	// the tool started executing. Zero until then, and left as-is once a
	// result arrives so renderers can show elapsed-time affordances only
	// while the call is genuinely in flight.
	RunningSince time.Time
}

// IsRunning returns true when the tool call has fully arrived, is being
// executed, and has not yet produced a result or been canceled.
func (o *ToolRenderOpts) IsRunning() bool {
	return o.ToolCall.Finished && !o.HasResult() && o.Status == ToolStatusRunning
}

// ImageConfig holds configuration for inline image rendering.
type ImageConfig struct {
	Encoding   fimage.Encoding
	CellWidth  int
	CellHeight int
	Tmux       bool
	MaxCols    int
	MaxRows    int
}

// IsPending returns true if the tool call is still pending (not finished and
// not canceled).
func (o *ToolRenderOpts) IsPending() bool {
	return !o.ToolCall.Finished && !o.IsCanceled()
}

// IsCanceled returns true if the tool status is canceled.
func (o *ToolRenderOpts) IsCanceled() bool {
	return o.Status == ToolStatusCanceled
}

// HasResult returns true if the result is not nil.
func (o *ToolRenderOpts) HasResult() bool {
	return o.Result != nil
}

// HasEmptyResult returns true if the result is nil or has empty content.
func (o *ToolRenderOpts) HasEmptyResult() bool {
	return o.Result == nil || o.Result.Content == ""
}

// ToolRenderer represents an interface for rendering tool calls.
type ToolRenderer interface {
	RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string
}

// ToolRendererFunc is a function type that implements the [ToolRenderer] interface.
type ToolRendererFunc func(sty *styles.Styles, width int, opts *ToolRenderOpts) string

// RenderTool implements the ToolRenderer interface.
func (f ToolRendererFunc) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	return f(sty, width, opts)
}

// baseToolMessageItem represents a tool call message that can be displayed in the UI.
type baseToolMessageItem struct {
	*list.Versioned
	*highlightableMessageItem
	*cachedMessageItem
	*focusableMessageItem

	toolRenderer ToolRenderer
	toolCall     message.ToolCall
	result       *message.ToolResult
	messageID    string
	status       ToolStatus
	// isCompact indicates this tool should render in compact mode.
	isCompact bool
	// spinningFunc allows tools to override the default spinning logic.
	// If nil, uses the default: !toolCall.Finished && !canceled.
	spinningFunc SpinningFunc

	sty             *styles.Styles
	anim            *anim.Anim
	expandedContent bool

	// imageConfig holds configuration for inline image rendering.
	imageConfig *ImageConfig

	// runningSince records when the tool call finished streaming its
	// input without a result yet, i.e. when execution began from the
	// UI's point of view. Zero while the input is still streaming.
	runningSince time.Time
}

var _ Expandable = (*baseToolMessageItem)(nil)

// newBaseToolMessageItem is the internal constructor for base tool message items.
func newBaseToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	toolRenderer ToolRenderer,
	canceled bool,
) *baseToolMessageItem {
	status := ToolStatusRunning
	if canceled {
		status = ToolStatusCanceled
	}

	v := list.NewVersioned()
	t := &baseToolMessageItem{
		Versioned:                v,
		highlightableMessageItem: defaultHighlighter(sty, v),
		cachedMessageItem:        &cachedMessageItem{},
		focusableMessageItem:     newFocusableMessageItem(v),
		sty:                      sty,
		toolRenderer:             toolRenderer,
		toolCall:                 toolCall,
		result:                   result,
		status:                   status,
	}
	if toolCall.Finished && result == nil && !canceled {
		t.runningSince = time.Now()
	}
	t.anim = anim.New(anim.Settings{
		ID:          toolCall.ID,
		Size:        15,
		GradColorA:  sty.WorkingGradFromColor,
		GradColorB:  sty.WorkingGradToColor,
		LabelColor:  sty.WorkingLabelColor,
		CycleColors: true,
	})

	return t
}

// NewToolMessageItem creates a new [ToolMessageItem] based on the tool call name.
//
// It returns a specific tool message item type if implemented, otherwise it
// returns a generic tool message item. The messageID is the ID of the assistant
// message containing this tool call.
func NewToolMessageItem(
	sty *styles.Styles,
	messageID string,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	var item ToolMessageItem
	switch toolCall.Name {
	case tools.BashToolName:
		item = NewBashToolMessageItem(sty, toolCall, result, canceled)
	case tools.JobOutputToolName:
		item = NewJobOutputToolMessageItem(sty, toolCall, result, canceled)
	case tools.JobKillToolName:
		item = NewJobKillToolMessageItem(sty, toolCall, result, canceled)
	case tools.ViewToolName:
		item = NewViewToolMessageItem(sty, toolCall, result, canceled)
	case tools.WriteToolName:
		item = NewWriteToolMessageItem(sty, toolCall, result, canceled)
	case tools.EditToolName:
		item = NewEditToolMessageItem(sty, toolCall, result, canceled)
	case tools.MultiEditToolName:
		item = NewMultiEditToolMessageItem(sty, toolCall, result, canceled)
	case tools.GlobToolName:
		item = NewGlobToolMessageItem(sty, toolCall, result, canceled)
	case tools.GrepToolName:
		item = NewGrepToolMessageItem(sty, toolCall, result, canceled)
	case tools.LSToolName:
		item = NewLSToolMessageItem(sty, toolCall, result, canceled)
	case tools.DownloadToolName:
		item = NewDownloadToolMessageItem(sty, toolCall, result, canceled)
	case tools.FetchToolName:
		item = NewFetchToolMessageItem(sty, toolCall, result, canceled)
	case tools.SourcegraphToolName:
		item = NewSourcegraphToolMessageItem(sty, toolCall, result, canceled)
	case tools.DiagnosticsToolName:
		item = NewDiagnosticsToolMessageItem(sty, toolCall, result, canceled)
	case agent.AgentToolName:
		item = NewAgentToolMessageItem(sty, toolCall, result, canceled)
	case agent.ReviewToolName:
		item = NewReviewToolMessageItem(sty, toolCall, result, canceled)
	case tools.AgenticFetchToolName:
		item = NewAgenticFetchToolMessageItem(sty, toolCall, result, canceled)
	case tools.WebFetchToolName:
		item = NewWebFetchToolMessageItem(sty, toolCall, result, canceled)
	case tools.WebSearchToolName:
		item = NewWebSearchToolMessageItem(sty, toolCall, result, canceled)
	case tools.TodosToolName:
		item = NewTodosToolMessageItem(sty, toolCall, result, canceled)
	case tools.ReferencesToolName:
		item = NewReferencesToolMessageItem(sty, toolCall, result, canceled)
	case tools.LSPRestartToolName:
		item = NewLSPRestartToolMessageItem(sty, toolCall, result, canceled)
	default:
		if IsDockerMCPTool(toolCall.Name) {
			item = NewDockerMCPToolMessageItem(sty, toolCall, result, canceled)
		} else if strings.HasPrefix(toolCall.Name, "mcp_") {
			item = NewMCPToolMessageItem(sty, toolCall, result, canceled)
		} else {
			item = NewGenericToolMessageItem(sty, toolCall, result, canceled)
		}
	}
	item.SetMessageID(messageID)
	return item
}

// SetCompact implements the Compactable interface.
func (t *baseToolMessageItem) SetCompact(compact bool) {
	if t.isCompact == compact {
		return
	}
	t.isCompact = compact
	t.clearCache()
	t.Bump()
}

// ID returns the unique identifier for this tool message item.
func (t *baseToolMessageItem) ID() string {
	return t.toolCall.ID
}

// StartAnimation starts the assistant message animation if it should be spinning.
func (t *baseToolMessageItem) StartAnimation() tea.Cmd {
	if !t.isSpinning() {
		return nil
	}
	return t.anim.Start()
}

// Animate progresses the assistant message animation if it should be spinning.
//
// Bumps the F6 list-cache version so the next draw re-renders this
// item: a spinner tick mutates anim's internal frame counter, which
// changes the rendered output but is invisible to the per-item
// caches. Without the bump the list cache would serve the previously
// rendered frame indefinitely and the spinner would appear frozen.
// The ID gate keeps unrelated ticks (routed here by a future change
// to chat.Animate's dispatch) from churning the cache.
func (t *baseToolMessageItem) Animate(msg anim.StepMsg) tea.Cmd {
	if !t.isSpinning() {
		return nil
	}
	if msg.ID != t.toolCall.ID {
		return nil
	}
	t.Bump()
	return t.anim.Animate(msg)
}

// RawRender implements [MessageItem].
func (t *baseToolMessageItem) RawRender(width int) string {
	// Tools render at full width (like diffs): command output, code, and
	// search results benefit from the whole terminal rather than the
	// prose readability cap used for assistant/user text.
	toolItemWidth := width - MessageLeftPaddingTotal

	content, height, ok := t.getCachedRender(toolItemWidth)
	// if we are spinning or there is no cache rerender
	if !ok || t.isSpinning() {
		content = t.toolRenderer.RenderTool(t.sty, toolItemWidth, &ToolRenderOpts{
			ToolCall:        t.toolCall,
			Result:          t.result,
			Anim:            t.anim,
			ExpandedContent: t.expandedContent,
			Compact:         t.isCompact,
			IsSpinning:      t.isSpinning(),
			Status:          t.computeStatus(),
			ImageConfig:     t.imageConfig,
			RunningSince:    t.runningSince,
		})

		// Prepend hook indicator if hooks ran for this tool call.
		if t.result != nil {
			if hookLine := toolOutputHookIndicator(t.sty, t.result.Metadata, toolItemWidth); hookLine != "" {
				content = hookLine + "\n\n" + content
			}
		}

		height = lipgloss.Height(content)
		// cache the rendered content
		t.setCachedRender(content, toolItemWidth, height)
	}

	return t.renderHighlighted(content, toolItemWidth, height)
}

// Render renders the tool message item at the given width.
func (t *baseToolMessageItem) Render(width int) string {
	// Cache the prefixed output keyed by (width, prefix variant).
	// Bypass the cache while spinning (RawRender output is
	// frame-dependent) or while a highlight range is active.
	useCache := !t.isSpinning() && !t.isHighlighted()
	var key uint64
	switch {
	case t.isCompact:
		key = 2
	case t.focused:
		key = 1
	default:
		key = 0
	}
	if useCache {
		if cached, ok := t.getCachedPrefixedRender(width, key); ok {
			return cached
		}
	}
	var prefix string
	if t.isCompact {
		prefix = t.sty.Messages.ToolCallCompact.Render()
	} else if t.focused {
		prefix = t.sty.Messages.ToolCallFocused.Render()
	} else {
		prefix = t.sty.Messages.ToolCallBlurred.Render()
	}
	lines := strings.Split(t.RawRender(width), "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	out := strings.Join(lines, "\n")
	if useCache {
		t.setCachedPrefixedRender(out, width, key)
	}
	return out
}

// ToolCall returns the tool call associated with this message item.
func (t *baseToolMessageItem) ToolCall() message.ToolCall {
	return t.toolCall
}

// SetToolCall sets the tool call associated with this message item.
func (t *baseToolMessageItem) SetToolCall(tc message.ToolCall) {
	if tc.Finished && !t.toolCall.Finished && t.result == nil && t.runningSince.IsZero() {
		t.runningSince = time.Now()
	}
	t.toolCall = tc
	t.clearCache()
	t.Bump()
}

// IsRunning reports whether the tool call has fully arrived and is
// executing without a result yet. Canceled calls are not running.
func (t *baseToolMessageItem) IsRunning() bool {
	return t.toolCall.Finished && t.result == nil && t.status == ToolStatusRunning
}

// SetResult sets the tool result associated with this message item.
func (t *baseToolMessageItem) SetResult(res *message.ToolResult) {
	t.result = res
	t.clearCache()
	t.Bump()
}

// MessageID returns the ID of the message containing this tool call.
func (t *baseToolMessageItem) MessageID() string {
	return t.messageID
}

// SetMessageID sets the ID of the message containing this tool call.
// MessageID is metadata only and does not affect the rendered output,
// so we deliberately do not bump the version here.
func (t *baseToolMessageItem) SetMessageID(id string) {
	t.messageID = id
}

// SetStatus sets the tool status.
func (t *baseToolMessageItem) SetStatus(status ToolStatus) {
	if t.status == status {
		return
	}
	t.status = status
	t.clearCache()
	t.Bump()
}

// Status returns the current tool status.
func (t *baseToolMessageItem) Status() ToolStatus {
	return t.status
}

// SetImageConfig sets the image rendering configuration.
func (t *baseToolMessageItem) SetImageConfig(cfg *ImageConfig) {
	t.imageConfig = cfg
}

// TransmitImage transmits the image data to the terminal if the result contains
// an image and the terminal supports inline image rendering.
func (t *baseToolMessageItem) TransmitImage() tea.Cmd {
	if t.result == nil || t.imageConfig == nil {
		return nil
	}
	if t.result.Data == "" || !strings.HasPrefix(t.result.MIMEType, "image/") {
		return nil
	}
	if t.imageConfig.Encoding != fimage.EncodingKitty {
		return nil
	}

	data, err := base64.StdEncoding.DecodeString(t.result.Data)
	if err != nil {
		return nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}

	cols, rows := imageRenderDims(t.imageConfig)

	id := t.toolCall.ID
	cs := fimage.CellSize{
		Width:  t.imageConfig.CellWidth,
		Height: t.imageConfig.CellHeight,
	}

	return t.imageConfig.Encoding.Transmit(id, img, cs, cols, rows, t.imageConfig.Tmux)
}

// computeStatus computes the effective status considering the result.
func (t *baseToolMessageItem) computeStatus() ToolStatus {
	if t.result != nil {
		if t.result.IsError {
			return ToolStatusError
		}
		return ToolStatusSuccess
	}
	return t.status
}

// isSpinning returns true if the tool should show animation.
func (t *baseToolMessageItem) isSpinning() bool {
	if t.spinningFunc != nil {
		return t.spinningFunc(SpinningState{
			ToolCall: t.toolCall,
			Result:   t.result,
			Status:   t.status,
		})
	}
	return !t.toolCall.Finished && t.status != ToolStatusCanceled
}

// SetSpinningFunc sets a custom function to determine if the tool should spin.
func (t *baseToolMessageItem) SetSpinningFunc(fn SpinningFunc) {
	t.spinningFunc = fn
}

// ToggleExpanded toggles the expanded state of the thinking box.
func (t *baseToolMessageItem) ToggleExpanded() bool {
	t.expandedContent = !t.expandedContent
	t.clearCache()
	t.Bump()
	return t.expandedContent
}

// Finished implements list.Item. A tool call is freezable once the
// tool call itself is marked finished AND a result has been recorded
// (or it has been canceled). Tools that override the spinning logic
// via spinningFunc would short-circuit live ticks; we still gate
// freezing on isSpinning to keep the contract conservative.
func (t *baseToolMessageItem) Finished() bool {
	if t.isSpinning() {
		return false
	}
	if t.status == ToolStatusCanceled {
		return true
	}
	return t.toolCall.Finished && t.result != nil
}

// HandleMouseClick implements MouseClickable.
func (t *baseToolMessageItem) HandleMouseClick(btn ansi.MouseButton, x, y int) bool {
	return btn == ansi.MouseLeft
}

// HandleKeyEvent implements KeyEventHandler.
func (t *baseToolMessageItem) HandleKeyEvent(key tea.KeyMsg) (bool, tea.Cmd) {
	if k := key.String(); k == "c" || k == "y" {
		text := t.formatToolForCopy()
		return true, common.CopyToClipboard(text, "Tool content copied to clipboard")
	}
	return false, nil
}

// pendingTool renders a tool that is still in progress with an animation.
func pendingTool(sty *styles.Styles, name string, anim *anim.Anim, nested bool) string {
	icon := sty.Tool.IconPending.Render()
	nameStyle := sty.Tool.NameNormal
	if nested {
		nameStyle = sty.Tool.NameNested
	}
	toolName := nameStyle.Render(name)

	var animView string
	if anim != nil {
		animView = anim.Render()
	}

	return fmt.Sprintf("%s %s %s", icon, toolName, animView)
}

// toolEarlyStateContent handles error/cancelled/pending states before content rendering.
// Returns the rendered output and true if early state was handled.
func toolEarlyStateContent(sty *styles.Styles, opts *ToolRenderOpts, width int) (string, bool) {
	var msg string
	switch opts.Status {
	case ToolStatusError:
		msg = toolErrorContent(sty, opts.Result, width)
	case ToolStatusCanceled:
		msg = sty.Tool.StateCancelled.Render("Canceled.")
	case ToolStatusAwaitingPermission:
		msg = sty.Tool.StateWaiting.Render("Requesting permission...")
	case ToolStatusRunning:
		msg = sty.Tool.StateWaiting.Render("Waiting for tool response...")
	default:
		return "", false
	}
	return msg, true
}

// toolErrorContent formats an error message with an ERROR or WARN tag.
func toolErrorContent(sty *styles.Styles, result *message.ToolResult, width int) string {
	if result == nil {
		return ""
	}
	errContent := strings.ReplaceAll(result.Content, "\n", " ")
	if strings.Contains(errContent, "User denied permission") {
		deniedTag := sty.Tool.WarnTag.Render("WARN")
		deniedTagWidth := lipgloss.Width(deniedTag)
		errContent = ansi.Truncate(errContent, width-deniedTagWidth-3, "…")
		return fmt.Sprintf("%s %s", deniedTag, sty.Tool.WarnMessage.Render(errContent))
	}
	errTag := sty.Tool.ErrorTag.Render("ERROR")
	tagWidth := lipgloss.Width(errTag)
	errContent = ansi.Truncate(errContent, width-tagWidth-3, "…")
	return fmt.Sprintf("%s %s", errTag, sty.Tool.ErrorMessage.Render(errContent))
}

// toolIcon returns the status icon for a tool call.
// toolIcon returns the status icon for a tool call based on its status.
func toolIcon(sty *styles.Styles, status ToolStatus) string {
	switch status {
	case ToolStatusSuccess:
		return sty.Tool.IconSuccess.String()
	case ToolStatusError:
		return sty.Tool.IconError.String()
	case ToolStatusCanceled:
		return sty.Tool.IconCancelled.String()
	default:
		return sty.Tool.IconPending.String()
	}
}

// toolParamList formats parameters as "main (key=value, ...)" and wraps
// the result across multiple lines at the given width instead of
// truncating, so long commands and paths remain fully readable. Each
// wrapped line is styled independently. A width <= 0 disables wrapping.
func toolParamList(sty *styles.Styles, params []string, width int) string {
	if len(params) == 0 {
		return ""
	}

	mainParam := params[0]

	// Build key=value pairs from remaining params (consecutive key, value pairs).
	var kvPairs []string
	for i := 1; i+1 < len(params); i += 2 {
		if params[i+1] != "" {
			kvPairs = append(kvPairs, fmt.Sprintf("%s=%s", params[i], params[i+1]))
		}
	}

	output := mainParam
	if len(kvPairs) > 0 {
		output = fmt.Sprintf("%s (%s)", mainParam, strings.Join(kvPairs, ", "))
	}

	if width > 0 {
		// ansi.Wrap word-wraps and falls back to hard-wrapping words
		// (e.g. long paths with no spaces) that exceed the width.
		output = ansi.Wrap(output, width, "")
	}

	lines := strings.Split(output, "\n")
	for i := range lines {
		lines[i] = sty.Tool.ParamMain.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

// toolHeader builds the tool header line: "● ToolName params..."
func toolHeader(sty *styles.Styles, status ToolStatus, name string, width int, nested bool, params ...string) string {
	icon := toolIcon(sty, status)
	nameStyle := sty.Tool.NameNormal
	if nested {
		nameStyle = sty.Tool.NameNested
	}
	toolName := nameStyle.Render(name)
	prefix := fmt.Sprintf("%s %s ", icon, toolName)
	return joinToolHeaderParams(sty, prefix, width, params...)
}

// joinToolHeaderParams composes a header from a styled prefix and a param
// list, wrapping the params at the remaining width and indenting
// continuation lines so they align under the first param — the header
// reads as a single logical line that happens to span multiple rows.
func joinToolHeaderParams(sty *styles.Styles, prefix string, width int, params ...string) string {
	prefixWidth := lipgloss.Width(prefix)
	paramsStr := toolParamList(sty, params, width-prefixWidth)
	if paramsStr == "" {
		return prefix
	}
	lines := strings.Split(paramsStr, "\n")
	if len(lines) > 1 {
		indent := strings.Repeat(" ", prefixWidth)
		for i := 1; i < len(lines); i++ {
			lines[i] = indent + lines[i]
		}
	}
	return prefix + strings.Join(lines, "\n")
}

// toolOutputPlainContent renders plain text with optional expansion support.
func toolOutputPlainContent(sty *styles.Styles, content string, width int, expanded bool) string {
	content = stringext.NormalizeSpace(content)
	lines := strings.Split(content, "\n")

	maxLines := responseContextHeight
	if expanded {
		maxLines = len(lines) // Show all
	}

	var out []string
	for i, ln := range lines {
		if i >= maxLines {
			break
		}
		ln = " " + ln
		if lipgloss.Width(ln) > width {
			ln = ansi.Truncate(ln, width, "…")
		}
		out = append(out, sty.Tool.ContentLine.Width(width).Render(ln))
	}

	wasTruncated := len(lines) > responseContextHeight

	if !expanded && wasTruncated {
		out = append(out, sty.Tool.ContentTruncation.
			Width(width).
			Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-responseContextHeight)))
	}

	return strings.Join(out, "\n")
}

// toolOutputCodeContent renders code with syntax highlighting and line numbers.
func toolOutputCodeContent(sty *styles.Styles, path, content string, offset, width int, expanded bool) string {
	content = stringext.NormalizeSpace(content)

	lines := strings.Split(content, "\n")
	maxLines := responseContextHeight
	if expanded {
		maxLines = len(lines)
	}

	// Truncate if needed.
	displayLines := lines
	if len(lines) > maxLines {
		displayLines = lines[:maxLines]
	}

	bg := sty.Tool.ContentCodeBg
	highlighted, _ := common.SyntaxHighlight(sty, strings.Join(displayLines, "\n"), path, bg)
	highlightedLines := strings.Split(highlighted, "\n")

	// Calculate line number width.
	maxLineNumber := len(displayLines) + offset
	maxDigits := getDigits(maxLineNumber)
	numFmt := fmt.Sprintf("%%%dd", maxDigits)

	bodyWidth := width - toolBodyLeftPaddingTotal
	codeWidth := bodyWidth - maxDigits

	var out []string
	for i, ln := range highlightedLines {
		lineNum := sty.Tool.ContentLineNumber.Render(fmt.Sprintf(numFmt, i+1+offset))

		// Truncate accounting for padding that will be added.
		ln = ansi.Truncate(ln, codeWidth-sty.Tool.ContentCodeLine.GetHorizontalPadding(), "…")

		codeLine := sty.Tool.ContentCodeLine.
			Width(codeWidth).
			Render(ln)

		out = append(out, lipgloss.JoinHorizontal(lipgloss.Left, lineNum, codeLine))
	}

	// Add truncation message if needed.
	if len(lines) > maxLines && !expanded {
		out = append(
			out, sty.Tool.ContentCodeTruncation.
				Width(width).
				Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-maxLines)),
		)
	}

	return sty.Tool.Body.Render(strings.Join(out, "\n"))
}

// imageRenderDims returns the column/row budget for inline image rendering.
// Transmit and render must use identical dimensions, else the cache key never
// matches and the image falls back to a chip.
func imageRenderDims(cfg *ImageConfig) (cols, rows int) {
	cols, rows = cfg.MaxCols, cfg.MaxRows
	if cols <= 0 {
		cols = defaultImageCols
	}
	if rows <= 0 {
		rows = defaultImageRows
	}
	return cols, rows
}

// toolOutputImageContent renders image data inline when supported, otherwise
// as a text summary.
func toolOutputImageContent(sty *styles.Styles, toolCallID, data, mediaType string, imgCfg *ImageConfig) string {
	dataSize := len(data) * 3 / 4
	sizeStr := formatSize(dataSize)

	if imgCfg != nil && imgCfg.Encoding == fimage.EncodingKitty {
		cols, rows := imageRenderDims(imgCfg)

		if fimage.HasTransmitted(toolCallID, cols, rows) {
			imgRender := imgCfg.Encoding.Render(toolCallID, cols, rows)
			infoLine := fmt.Sprintf(
				"%s %s %s",
				sty.Tool.MediaType.Render(mediaType),
				sty.Tool.ResourceLoadedIndicator.Render(styles.ArrowRightIcon),
				sty.Tool.ResourceSize.Render(sizeStr),
			)
			return sty.Tool.Body.Render(imgRender + "\n" + infoLine)
		}
	}

	return sty.Tool.Body.Render(fmt.Sprintf(
		"%s %s %s %s",
		sty.Tool.ResourceLoadedText.Render("Loaded Image"),
		sty.Tool.ResourceLoadedIndicator.Render(styles.ArrowRightIcon),
		sty.Tool.MediaType.Render(mediaType),
		sty.Tool.ResourceSize.Render(sizeStr),
	))
}

// toolOutputSkillContent renders a skill loaded indicator.
func toolOutputSkillContent(sty *styles.Styles, name, description string) string {
	return sty.Tool.Body.Render(fmt.Sprintf(
		"%s %s %s %s",
		sty.Tool.ResourceLoadedText.Render("Loaded Skill"),
		sty.Tool.ResourceLoadedIndicator.Render(styles.ArrowRightIcon),
		sty.Tool.ResourceName.Render(name),
		sty.Tool.ResourceSize.Render(description),
	))
}

// toolOutputHookIndicator renders hook indicator lines from tool metadata.
// Returns empty string if no hook metadata is present. Hook names are
// sanitized (newlines replaced with ¶) and truncated to fit the available
// horizontal space.
func toolOutputHookIndicator(sty *styles.Styles, metadata string, width int) string {
	if metadata == "" {
		return ""
	}
	var meta struct {
		Hook *hooks.HookMetadata `json:"hook"`
	}
	if err := json.Unmarshal([]byte(metadata), &meta); err != nil || meta.Hook == nil {
		return ""
	}
	h := meta.Hook
	if len(h.Hooks) == 0 {
		return ""
	}

	// Sanitize names (replace newlines with ¶) and compute max widths
	// for the name, matcher, and detail columns so they align. The name
	// column is capped at maxHookNameWidth characters.
	const maxHookNameWidth = 30
	sanitizedNames := make([]string, len(h.Hooks))
	details := make([]string, len(h.Hooks))
	maxNameWidth := 0
	maxMatcherWidth := 0
	maxDetailWidth := 0
	for i, hi := range h.Hooks {
		sanitizedNames[i] = strings.ReplaceAll(hi.Name, "\n", "¶")
		w := lipgloss.Width(sty.Tool.HookName.Render(sanitizedNames[i]))
		if w > maxNameWidth {
			maxNameWidth = w
		}
		if hi.Matcher != "" {
			mw := lipgloss.Width(sty.Tool.HookMatcher.Render(hi.Matcher))
			if mw > maxMatcherWidth {
				maxMatcherWidth = mw
			}
		}
		details[i] = hookDetail(sty, hi)
		if dw := lipgloss.Width(details[i]); dw > maxDetailWidth {
			maxDetailWidth = dw
		}
	}

	if maxNameWidth > maxHookNameWidth {
		maxNameWidth = maxHookNameWidth
	}

	// Cap the name column so the widest line still fits in width. The
	// per-line layout is:
	//   "Hook " + name(padded) + [" " + matcher(padded)] + " → " + detail
	if width > 0 {
		fixed := lipgloss.Width(sty.Tool.HookLabel.Render("Hook")) + 1
		if maxMatcherWidth > 0 {
			fixed += 1 + maxMatcherWidth
		}
		fixed += 1 + lipgloss.Width(sty.Tool.HookArrow.Render(styles.ArrowRightIcon)) + 1
		fixed += maxDetailWidth
		if budget := width - fixed; budget < maxNameWidth {
			maxNameWidth = max(1, budget)
		}
	}

	var lines []string
	for i, hi := range h.Hooks {
		name := truncateHookName(sanitizedNames[i], maxNameWidth)
		lines = append(lines, renderHookLine(sty, hi, name, details[i], maxNameWidth, maxMatcherWidth))
	}
	return strings.Join(lines, "\n")
}

// truncateHookName truncates a hook name to fit within maxWidth cells,
// using left-truncation for absolute paths (e.g. `…/format.sh`) and
// right-truncation for everything else. Left-truncation is only applied
// when the name looks unambiguously like a path: absolute, single-line,
// and contains no spaces.
func truncateHookName(name string, maxWidth int) string {
	if ansi.StringWidth(name) <= maxWidth {
		return name
	}
	if isLikelyPath(name) {
		// ansi.TruncateLeft removes n graphemes from the start; pick n
		// so the result plus the "…" prefix fits in maxWidth.
		n := ansi.StringWidth(name) - maxWidth + 1
		return ansi.TruncateLeft(name, n, "…")
	}
	return ansi.Truncate(name, maxWidth, "…")
}

// isLikelyPath reports whether s looks unambiguously like a filesystem
// path, suitable for left-truncation. We accept absolute paths and
// relative paths that contain a separator and no shell-ish characters.
func isLikelyPath(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\n¶'\"|&;<>$`*?(){}[]\\") {
		return false
	}
	if filepath.IsAbs(s) {
		return true
	}
	return strings.Contains(s, "/")
}

// renderHookLine renders a single hook indicator line with aligned columns.
func renderHookLine(sty *styles.Styles, hi hooks.HookInfo, rawName, detail string, maxNameWidth, maxMatcherWidth int) string {
	name := sty.Tool.HookName.Render(rawName)
	namePad := strings.Repeat(" ", max(0, maxNameWidth-lipgloss.Width(name)))

	var matcherPart string
	if maxMatcherWidth > 0 {
		if hi.Matcher != "" {
			matcher := sty.Tool.HookMatcher.Render(hi.Matcher)
			matcherPad := strings.Repeat(" ", maxMatcherWidth-lipgloss.Width(matcher))
			matcherPart = " " + matcher + matcherPad
		} else {
			matcherPart = " " + strings.Repeat(" ", maxMatcherWidth)
		}
	}

	labelStyle := sty.Tool.HookLabel
	arrowStyle := sty.Tool.HookArrow
	if hi.Decision == "deny" {
		labelStyle = sty.Tool.HookDeniedLabel
		arrowStyle = sty.Tool.HookDeniedLabel
	}

	return fmt.Sprintf(
		"%s %s%s%s %s %s",
		labelStyle.Render("Hook"),
		name,
		namePad,
		matcherPart,
		arrowStyle.Render(styles.ArrowRightIcon),
		detail,
	)
}

// hookDetail returns the styled detail text for a single hook result.
func hookDetail(sty *styles.Styles, hi hooks.HookInfo) string {
	const (
		okMessage      = "OK"
		denialMessage  = "Denied"
		rewroteMessage = "Rewrote Output"
	)
	switch hi.Decision {
	case "deny":
		if hi.Reason != "" {
			return sty.Tool.HookDenied.Render(denialMessage) + " " + sty.Tool.HookDeniedReason.Render(hi.Reason)
		}
		return sty.Tool.HookDenied.Render(denialMessage)
	case "allow":
		result := sty.Tool.HookOK.Render(okMessage)
		if hi.InputRewrite {
			result += " " + sty.Tool.HookRewrote.Render(rewroteMessage)
		}
		return result
	default:
		result := sty.Tool.HookOK.Render(okMessage)
		if hi.InputRewrite {
			result += " " + sty.Tool.HookRewrote.Render(rewroteMessage)
		}
		return result
	}
}

// getDigits returns the number of digits in a number.
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

// formatSize formats byte size into human readable format.
func formatSize(bytes int) string {
	const (
		kb = 1024
		mb = kb * 1024
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// toolOutputDiffContent renders a diff between old and new content.
func toolOutputDiffContent(sty *styles.Styles, file, oldContent, newContent string, width int, expanded bool) string {
	bodyWidth := width - toolBodyLeftPaddingTotal

	formatter := common.DiffFormatter(sty).
		Before(file, oldContent).
		After(file, newContent).
		Width(bodyWidth)

	// Use split view for wide terminals.
	if width > maxTextWidth {
		formatter = formatter.Split()
	}

	formatted := formatter.String()
	lines := strings.Split(formatted, "\n")

	// Truncate if needed.
	maxLines := responseContextHeight
	if expanded {
		maxLines = len(lines)
	}

	if len(lines) > maxLines && !expanded {
		truncMsg := sty.Tool.DiffTruncation.
			Width(bodyWidth).
			Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-maxLines))
		formatted = strings.Join(lines[:maxLines], "\n") + "\n" + truncMsg
	}

	return sty.Tool.Body.Render(formatted)
}

// formatTimeout converts timeout seconds to a duration string (e.g., "30s").
// Returns empty string if timeout is 0.
func formatTimeout(timeout int) string {
	if timeout == 0 {
		return ""
	}
	return fmt.Sprintf("%ds", timeout)
}

// formatNonZero returns string representation of non-zero integers, empty string for zero.
func formatNonZero(value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

// toolOutputMultiEditDiffContent renders a diff with optional failed edits note.
func toolOutputMultiEditDiffContent(sty *styles.Styles, file string, meta tools.MultiEditResponseMetadata, totalEdits, width int, expanded bool) string {
	bodyWidth := width - toolBodyLeftPaddingTotal

	formatter := common.DiffFormatter(sty).
		Before(file, meta.OldContent).
		After(file, meta.NewContent).
		Width(bodyWidth)

	// Use split view for wide terminals.
	if width > maxTextWidth {
		formatter = formatter.Split()
	}

	formatted := formatter.String()
	lines := strings.Split(formatted, "\n")

	// Truncate if needed.
	maxLines := responseContextHeight
	if expanded {
		maxLines = len(lines)
	}

	if len(lines) > maxLines && !expanded {
		truncMsg := sty.Tool.DiffTruncation.
			Width(bodyWidth).
			Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-maxLines))
		formatted = truncMsg + "\n" + strings.Join(lines[:maxLines], "\n")
	}

	// Add failed edits note if any exist.
	if len(meta.EditsFailed) > 0 {
		noteTag := sty.Tool.NoteTag.Render("Note")
		noteMsg := fmt.Sprintf("%d of %d edits succeeded", meta.EditsApplied, totalEdits)
		note := fmt.Sprintf("%s %s", noteTag, sty.Tool.NoteMessage.Render(noteMsg))
		formatted = formatted + "\n\n" + note
	}

	return sty.Tool.Body.Render(formatted)
}

// roundedEnumerator creates a tree enumerator with rounded corners.
func roundedEnumerator(lPadding, width int) tree.Enumerator {
	if width == 0 {
		width = 2
	}
	if lPadding == 0 {
		lPadding = 1
	}
	return func(children tree.Children, index int) string {
		line := strings.Repeat("─", width)
		padding := strings.Repeat(" ", lPadding)
		if children.Length()-1 == index {
			return padding + "╰" + line
		}
		return padding + "├" + line
	}
}

// roundedIndenter creates a tree indenter whose width matches
// [roundedEnumerator] for the same (lPadding, width) arguments. The
// lipgloss tree renderer uses the indenter both for the continuation
// lines of multi-line children and as the prefix for nested subtrees.
// The default indenter is a fixed 3 columns wide, so pairing it with a
// wider custom enumerator misaligns wrapped tool output and floats the
// vertical connector bars. Keeping the two in lockstep fixes that.
func roundedIndenter(lPadding, width int) tree.Indenter {
	if width == 0 {
		width = 2
	}
	if lPadding == 0 {
		lPadding = 1
	}
	return func(children tree.Children, index int) string {
		padding := strings.Repeat(" ", lPadding)
		if children.Length()-1 == index {
			return padding + " " + strings.Repeat(" ", width)
		}
		return padding + "│" + strings.Repeat(" ", width)
	}
}

// toolOutputMarkdownContent renders markdown content with optional truncation.
func toolOutputMarkdownContent(sty *styles.Styles, content string, width int, expanded bool) string {
	content = stringext.NormalizeSpace(content)

	// Cap width for readability.
	if width > maxTextWidth {
		width = maxTextWidth
	}

	renderer := common.QuietMarkdownRenderer(sty, width)
	mu := common.LockMarkdownRenderer(renderer)
	mu.Lock()
	rendered, err := renderer.Render(content)
	mu.Unlock()
	if err != nil {
		return toolOutputPlainContent(sty, content, width, expanded)
	}

	lines := strings.Split(rendered, "\n")
	maxLines := responseContextHeight
	if expanded {
		maxLines = len(lines)
	}

	var out []string
	for i, ln := range lines {
		if i >= maxLines {
			break
		}
		out = append(out, ln)
	}

	if len(lines) > maxLines && !expanded {
		out = append(
			out, sty.Tool.ContentTruncation.
				Width(width).
				Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-maxLines)),
		)
	}

	return sty.Tool.Body.Render(strings.Join(out, "\n"))
}

// formatToolForCopy formats the tool call for clipboard copying.
func (t *baseToolMessageItem) formatToolForCopy() string {
	var parts []string

	toolName := prettifyToolName(t.toolCall.Name)
	parts = append(parts, fmt.Sprintf("## %s Tool Call", toolName))

	if t.toolCall.Input != "" {
		params := t.formatParametersForCopy()
		if params != "" {
			parts = append(parts, "### Parameters:")
			parts = append(parts, params)
		}
	}

	if t.result != nil && t.result.ToolCallID != "" {
		if t.result.IsError {
			parts = append(parts, "### Error:")
			parts = append(parts, t.result.Content)
		} else {
			parts = append(parts, "### Result:")
			content := t.formatResultForCopy()
			if content != "" {
				parts = append(parts, content)
			}
		}
	} else if t.status == ToolStatusCanceled {
		parts = append(parts, "### Status:")
		parts = append(parts, "Cancelled")
	} else {
		parts = append(parts, "### Status:")
		parts = append(parts, "Pending...")
	}

	return strings.Join(parts, "\n\n")
}

// copyFields accumulates "**Label:** value" lines for clipboard formatting.
type copyFields []string

// add appends a "**label:** value" line unconditionally.
func (f *copyFields) addf(label, format string, args ...any) {
	*f = append(*f, fmt.Sprintf("**"+label+":** "+format, args...))
}

// addIf appends a line only when cond is true.
func (f *copyFields) addIff(cond bool, label, format string, args ...any) {
	if cond {
		f.addf(label, format, args...)
	}
}

func (f copyFields) String() string {
	return strings.Join(f, "\n")
}

// unmarshalParams decodes the tool call input into T, reporting whether it
// succeeded.
func unmarshalParams[T any](input string) (T, bool) {
	var params T
	ok := json.Unmarshal([]byte(input), &params) == nil
	return params, ok
}

// formatParametersForCopy formats tool parameters for clipboard copying.
func (t *baseToolMessageItem) formatParametersForCopy() string {
	input := t.toolCall.Input
	switch t.toolCall.Name {
	case tools.BashToolName:
		if p, ok := unmarshalParams[tools.BashParams](input); ok {
			cmd := strings.ReplaceAll(p.Command, "\n", " ")
			cmd = strings.ReplaceAll(cmd, "\t", "    ")
			return fmt.Sprintf("**Command:** %s", cmd)
		}
	case tools.ViewToolName:
		if p, ok := unmarshalParams[tools.ViewParams](input); ok {
			var f copyFields
			f.addf("File", "%s", fsext.PrettyPath(p.FilePath))
			f.addIff(p.Limit > 0, "Limit", "%d", p.Limit)
			f.addIff(p.Offset > 0, "Offset", "%d", p.Offset)
			return f.String()
		}
	case tools.EditToolName:
		if p, ok := unmarshalParams[tools.EditParams](input); ok {
			return fmt.Sprintf("**File:** %s", fsext.PrettyPath(p.FilePath))
		}
	case tools.MultiEditToolName:
		if p, ok := unmarshalParams[tools.MultiEditParams](input); ok {
			var f copyFields
			f.addf("File", "%s", fsext.PrettyPath(p.FilePath))
			f.addf("Edits", "%d", len(p.Edits))
			return f.String()
		}
	case tools.WriteToolName:
		if p, ok := unmarshalParams[tools.WriteParams](input); ok {
			return fmt.Sprintf("**File:** %s", fsext.PrettyPath(p.FilePath))
		}
	case tools.FetchToolName:
		if p, ok := unmarshalParams[tools.FetchParams](input); ok {
			var f copyFields
			f.addf("URL", "%s", p.URL)
			f.addIff(p.Format != "", "Format", "%s", p.Format)
			f.addIff(p.Timeout > 0, "Timeout", "%ds", p.Timeout)
			return f.String()
		}
	case tools.AgenticFetchToolName:
		if p, ok := unmarshalParams[tools.AgenticFetchParams](input); ok {
			var f copyFields
			f.addIff(p.URL != "", "URL", "%s", p.URL)
			f.addIff(p.Prompt != "", "Prompt", "%s", p.Prompt)
			return f.String()
		}
	case tools.WebFetchToolName:
		if p, ok := unmarshalParams[tools.WebFetchParams](input); ok {
			return fmt.Sprintf("**URL:** %s", p.URL)
		}
	case tools.GrepToolName:
		if p, ok := unmarshalParams[tools.GrepParams](input); ok {
			var f copyFields
			f.addf("Pattern", "%s", p.Pattern)
			f.addIff(p.Path != "", "Path", "%s", p.Path)
			f.addIff(p.Include != "", "Include", "%s", p.Include)
			f.addIff(p.LiteralText, "Literal", "true")
			return f.String()
		}
	case tools.GlobToolName:
		if p, ok := unmarshalParams[tools.GlobParams](input); ok {
			var f copyFields
			f.addf("Pattern", "%s", p.Pattern)
			f.addIff(p.Path != "", "Path", "%s", p.Path)
			return f.String()
		}
	case tools.LSToolName:
		if p, ok := unmarshalParams[tools.LSParams](input); ok {
			path := p.Path
			if path == "" {
				path = "."
			}
			return fmt.Sprintf("**Path:** %s", fsext.PrettyPath(path))
		}
	case tools.DownloadToolName:
		if p, ok := unmarshalParams[tools.DownloadParams](input); ok {
			var f copyFields
			f.addf("URL", "%s", p.URL)
			f.addf("File Path", "%s", fsext.PrettyPath(p.FilePath))
			f.addIff(p.Timeout > 0, "Timeout", "%s", (time.Duration(p.Timeout) * time.Second).String())
			return f.String()
		}
	case tools.SourcegraphToolName:
		if p, ok := unmarshalParams[tools.SourcegraphParams](input); ok {
			var f copyFields
			f.addf("Query", "%s", p.Query)
			f.addIff(p.Count > 0, "Count", "%d", p.Count)
			f.addIff(p.ContextWindow > 0, "Context", "%d", p.ContextWindow)
			return f.String()
		}
	case tools.DiagnosticsToolName:
		return "**Project:** diagnostics"
	case agent.AgentToolName:
		if p, ok := unmarshalParams[agent.AgentParams](input); ok {
			return fmt.Sprintf("**Task:**\n%s", p.Prompt)
		}
	}

	if params, ok := unmarshalParams[map[string]any](input); ok {
		var f copyFields
		keys := slices.Sorted(maps.Keys(params))
		for _, key := range keys {
			displayKey := strings.ReplaceAll(key, "_", " ")
			if len(displayKey) > 0 {
				displayKey = strings.ToUpper(displayKey[:1]) + displayKey[1:]
			}
			f.addf(displayKey, "%v", params[key])
		}
		return f.String()
	}

	return ""
}

// formatResultForCopy formats tool results for clipboard copying.
func (t *baseToolMessageItem) formatResultForCopy() string {
	if t.result == nil {
		return ""
	}

	if t.result.Data != "" {
		if strings.HasPrefix(t.result.MIMEType, "image/") {
			return fmt.Sprintf("[Image: %s]", t.result.MIMEType)
		}
		return fmt.Sprintf("[Media: %s]", t.result.MIMEType)
	}

	switch t.toolCall.Name {
	case tools.BashToolName:
		return t.formatBashResultForCopy()
	case tools.ViewToolName:
		return t.formatViewResultForCopy()
	case tools.EditToolName:
		return t.formatEditResultForCopy()
	case tools.MultiEditToolName:
		return t.formatMultiEditResultForCopy()
	case tools.WriteToolName:
		return t.formatWriteResultForCopy()
	case tools.FetchToolName:
		return t.formatFetchResultForCopy()
	case tools.AgenticFetchToolName:
		return t.formatAgenticFetchResultForCopy()
	case tools.WebFetchToolName:
		return t.formatWebFetchResultForCopy()
	case agent.AgentToolName:
		return t.formatAgentResultForCopy()
	case tools.DownloadToolName, tools.GrepToolName, tools.GlobToolName, tools.LSToolName, tools.SourcegraphToolName, tools.DiagnosticsToolName, tools.TodosToolName:
		return fmt.Sprintf("```\n%s\n```", t.result.Content)
	default:
		return t.result.Content
	}
}

// formatBashResultForCopy formats bash tool results for clipboard.
func (t *baseToolMessageItem) formatBashResultForCopy() string {
	if t.result == nil {
		return ""
	}

	var meta tools.BashResponseMetadata
	if t.result.Metadata != "" {
		json.Unmarshal([]byte(t.result.Metadata), &meta)
	}

	output := meta.Output
	if output == "" && t.result.Content != tools.BashNoOutput {
		output = t.result.Content
	}

	if output == "" {
		return ""
	}

	return fmt.Sprintf("```bash\n%s\n```", output)
}

// formatViewResultForCopy formats view tool results for clipboard.
func (t *baseToolMessageItem) formatViewResultForCopy() string {
	if t.result == nil {
		return ""
	}

	var meta tools.ViewResponseMetadata
	if t.result.Metadata != "" {
		json.Unmarshal([]byte(t.result.Metadata), &meta)
	}

	if meta.Content == "" {
		return t.result.Content
	}

	lang := ""
	if meta.FilePath != "" {
		ext := strings.ToLower(filepath.Ext(meta.FilePath))
		switch ext {
		case ".go":
			lang = "go"
		case ".js", ".mjs":
			lang = "javascript"
		case ".ts":
			lang = "typescript"
		case ".py":
			lang = "python"
		case ".rs":
			lang = "rust"
		case ".java":
			lang = "java"
		case ".c":
			lang = "c"
		case ".cpp", ".cc", ".cxx":
			lang = "cpp"
		case ".sh", ".bash":
			lang = "bash"
		case ".json":
			lang = "json"
		case ".yaml", ".yml":
			lang = "yaml"
		case ".xml":
			lang = "xml"
		case ".html":
			lang = "html"
		case ".css":
			lang = "css"
		case ".md":
			lang = "markdown"
		}
	}

	var result strings.Builder
	if lang != "" {
		fmt.Fprintf(&result, "```%s\n", lang)
	} else {
		result.WriteString("```\n")
	}
	result.WriteString(meta.Content)
	result.WriteString("\n```")

	return result.String()
}

// formatEditResultForCopy formats edit tool results for clipboard.
func (t *baseToolMessageItem) formatEditResultForCopy() string {
	if t.result == nil || t.result.Metadata == "" {
		if t.result != nil {
			return t.result.Content
		}
		return ""
	}

	var meta tools.EditResponseMetadata
	if json.Unmarshal([]byte(t.result.Metadata), &meta) != nil {
		return t.result.Content
	}

	var params tools.EditParams
	json.Unmarshal([]byte(t.toolCall.Input), &params)

	var result strings.Builder

	if meta.OldContent != "" || meta.NewContent != "" {
		fileName := params.FilePath
		if fileName != "" {
			fileName = fsext.PrettyPath(fileName)
		}
		diffContent, additions, removals := diff.GenerateDiff(meta.OldContent, meta.NewContent, fileName)

		fmt.Fprintf(&result, "Changes: +%d -%d\n", additions, removals)
		result.WriteString("```diff\n")
		result.WriteString(diffContent)
		result.WriteString("\n```")
	}

	return result.String()
}

// formatMultiEditResultForCopy formats multi-edit tool results for clipboard.
func (t *baseToolMessageItem) formatMultiEditResultForCopy() string {
	if t.result == nil || t.result.Metadata == "" {
		if t.result != nil {
			return t.result.Content
		}
		return ""
	}

	var meta tools.MultiEditResponseMetadata
	if json.Unmarshal([]byte(t.result.Metadata), &meta) != nil {
		return t.result.Content
	}

	var params tools.MultiEditParams
	json.Unmarshal([]byte(t.toolCall.Input), &params)

	var result strings.Builder
	if meta.OldContent != "" || meta.NewContent != "" {
		fileName := params.FilePath
		if fileName != "" {
			fileName = fsext.PrettyPath(fileName)
		}
		diffContent, additions, removals := diff.GenerateDiff(meta.OldContent, meta.NewContent, fileName)

		fmt.Fprintf(&result, "Changes: +%d -%d\n", additions, removals)
		result.WriteString("```diff\n")
		result.WriteString(diffContent)
		result.WriteString("\n```")
	}

	return result.String()
}

// formatWriteResultForCopy formats write tool results for clipboard.
func (t *baseToolMessageItem) formatWriteResultForCopy() string {
	if t.result == nil {
		return ""
	}

	var params tools.WriteParams
	if json.Unmarshal([]byte(t.toolCall.Input), &params) != nil {
		return t.result.Content
	}

	lang := ""
	if params.FilePath != "" {
		ext := strings.ToLower(filepath.Ext(params.FilePath))
		switch ext {
		case ".go":
			lang = "go"
		case ".js", ".mjs":
			lang = "javascript"
		case ".ts":
			lang = "typescript"
		case ".py":
			lang = "python"
		case ".rs":
			lang = "rust"
		case ".java":
			lang = "java"
		case ".c":
			lang = "c"
		case ".cpp", ".cc", ".cxx":
			lang = "cpp"
		case ".sh", ".bash":
			lang = "bash"
		case ".json":
			lang = "json"
		case ".yaml", ".yml":
			lang = "yaml"
		case ".xml":
			lang = "xml"
		case ".html":
			lang = "html"
		case ".css":
			lang = "css"
		case ".md":
			lang = "markdown"
		}
	}

	var result strings.Builder
	fmt.Fprintf(&result, "File: %s\n", fsext.PrettyPath(params.FilePath))
	if lang != "" {
		fmt.Fprintf(&result, "```%s\n", lang)
	} else {
		result.WriteString("```\n")
	}
	result.WriteString(params.Content)
	result.WriteString("\n```")

	return result.String()
}

// formatFetchResultForCopy formats fetch tool results for clipboard.
func (t *baseToolMessageItem) formatFetchResultForCopy() string {
	if t.result == nil {
		return ""
	}

	var params tools.FetchParams
	if json.Unmarshal([]byte(t.toolCall.Input), &params) != nil {
		return t.result.Content
	}

	var result strings.Builder
	if params.URL != "" {
		fmt.Fprintf(&result, "URL: %s\n", params.URL)
	}
	if params.Format != "" {
		fmt.Fprintf(&result, "Format: %s\n", params.Format)
	}
	if params.Timeout > 0 {
		fmt.Fprintf(&result, "Timeout: %ds\n", params.Timeout)
	}
	result.WriteString("\n")

	result.WriteString(t.result.Content)

	return result.String()
}

// formatAgenticFetchResultForCopy formats agentic fetch tool results for clipboard.
func (t *baseToolMessageItem) formatAgenticFetchResultForCopy() string {
	if t.result == nil {
		return ""
	}

	var params tools.AgenticFetchParams
	if json.Unmarshal([]byte(t.toolCall.Input), &params) != nil {
		return t.result.Content
	}

	var result strings.Builder
	if params.URL != "" {
		fmt.Fprintf(&result, "URL: %s\n", params.URL)
	}
	if params.Prompt != "" {
		fmt.Fprintf(&result, "Prompt: %s\n\n", params.Prompt)
	}

	result.WriteString("```markdown\n")
	result.WriteString(t.result.Content)
	result.WriteString("\n```")

	return result.String()
}

// formatWebFetchResultForCopy formats web fetch tool results for clipboard.
func (t *baseToolMessageItem) formatWebFetchResultForCopy() string {
	if t.result == nil {
		return ""
	}

	var params tools.WebFetchParams
	if json.Unmarshal([]byte(t.toolCall.Input), &params) != nil {
		return t.result.Content
	}

	var result strings.Builder
	fmt.Fprintf(&result, "URL: %s\n\n", params.URL)
	result.WriteString("```markdown\n")
	result.WriteString(t.result.Content)
	result.WriteString("\n```")

	return result.String()
}

// formatAgentResultForCopy formats agent tool results for clipboard.
func (t *baseToolMessageItem) formatAgentResultForCopy() string {
	if t.result == nil {
		return ""
	}

	var result strings.Builder

	if t.result.Content != "" {
		fmt.Fprintf(&result, "```markdown\n%s\n```", t.result.Content)
	}

	return result.String()
}

// prettyToolNames maps built-in tool names to their human-readable display
// names. Tool names not present here (e.g. MCP tools) fall back to
// humanizedToolName.
var prettyToolNames = map[string]string{
	agent.AgentToolName:        "Agent",
	agent.ReviewToolName:       "Review",
	tools.BashToolName:         "Bash",
	tools.JobOutputToolName:    "Job: Output",
	tools.JobKillToolName:      "Job: Kill",
	tools.DownloadToolName:     "Download",
	tools.EditToolName:         "Edit",
	tools.MultiEditToolName:    "Multi-Edit",
	tools.FetchToolName:        "Fetch",
	tools.AgenticFetchToolName: "Agentic Fetch",
	tools.WebFetchToolName:     "Fetch",
	tools.WebSearchToolName:    "Search",
	tools.GlobToolName:         "Glob",
	tools.GrepToolName:         "Grep",
	tools.LSToolName:           "List",
	tools.SourcegraphToolName:  "Sourcegraph",
	tools.TodosToolName:        "To-Do",
	tools.ViewToolName:         "View",
	tools.WriteToolName:        "Write",
}

// prettifyToolName returns a human-readable name for tool names.
func prettifyToolName(name string) string {
	if pretty, ok := prettyToolNames[name]; ok {
		return pretty
	}
	return humanizedToolName(name)
}
