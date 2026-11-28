package chat

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles"
)

// -----------------------------------------------------------------------------
// Agent Tool
// -----------------------------------------------------------------------------

// NestedToolContainer is an interface for tool items that can contain nested tool calls.
type NestedToolContainer interface {
	NestedTools() []ToolMessageItem
	SetNestedTools(tools []ToolMessageItem)
	AddNestedTool(tool ToolMessageItem)
}

// GroupedNestedToolContainer is implemented by containers that bucket
// their nested tool calls into independent groups — e.g. the review
// tool fans out to N adversarial reviewers, each rendered as its own
// sub-tree. NestedTools() (from [NestedToolContainer]) still returns the
// flattened set across all groups for ID registration and animation.
type GroupedNestedToolContainer interface {
	NestedToolContainer
	// NestedToolsForGroup returns the nested tools for reviewer group g.
	NestedToolsForGroup(g int) []ToolMessageItem
	// SetNestedToolsForGroup replaces the nested tools for group g,
	// growing the group set as needed.
	SetNestedToolsForGroup(g int, tools []ToolMessageItem)
	// GroupCount returns the number of groups currently tracked.
	GroupCount() int
}

// AgentToolMessageItem is a message item that represents an agent tool call.
type AgentToolMessageItem struct {
	*baseToolMessageItem

	nestedTools []ToolMessageItem
}

var (
	_ ToolMessageItem     = (*AgentToolMessageItem)(nil)
	_ NestedToolContainer = (*AgentToolMessageItem)(nil)
)

// NewAgentToolMessageItem creates a new [AgentToolMessageItem].
func NewAgentToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *AgentToolMessageItem {
	t := &AgentToolMessageItem{}
	t.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &AgentToolRenderContext{agent: t}, canceled)
	// For the agent tool we keep spinning until the tool call is finished.
	t.spinningFunc = func(state SpinningState) bool {
		return !state.HasResult() && !state.IsCanceled()
	}
	return t
}

// Advance implements [Animatable].
//
// Advances the parent's own spinner and every spinning nested tool in
// one frame, bumping the parent's F6 list-cache version. Nested tools
// are not list entries of their own — their IDs map to this parent's
// index in idInxMap and their renders are embedded inline in this
// parent's output — so the list only checks the parent's version.
// Without the bump, the list cache would serve the previously rendered
// frame indefinitely and the spinner would appear frozen.
func (a *AgentToolMessageItem) Advance() bool {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return false
	}
	changed := a.anim.Advance()
	changed = advanceNested(a.nestedTools) || changed
	if changed {
		a.Bump()
	}
	return changed
}

// advanceNested advances every spinning animatable tool in tools and
// reports whether any of them changed.
func advanceNested(tools []ToolMessageItem) bool {
	changed := false
	for _, nestedTool := range tools {
		if s, ok := nestedTool.(Animatable); ok && s.Spinning() && s.Advance() {
			changed = true
		}
	}
	return changed
}

// SetStyles points the agent tool and its nested tools at sty.
func (a *AgentToolMessageItem) SetStyles(sty *styles.Styles) {
	if a == nil {
		return
	}
	a.baseToolMessageItem.SetStyles(sty)
	for _, nested := range a.nestedTools {
		nested.SetStyles(sty)
	}
}

// NestedTools returns the nested tools.
func (a *AgentToolMessageItem) NestedTools() []ToolMessageItem {
	return a.nestedTools
}

// SetNestedTools sets the nested tools.
//
// SetNestedTools always bumps the version. The previous design
// deduped when the slice's length and element pointers were
// unchanged, but the live update path in internal/ui/model/ui.go
// mutates existing children in place (SetToolCall / SetResult on the
// same pointers) and then calls SetNestedTools with the same slice.
// Pointer-equality dedupe in that case skips the parent Bump even
// though the parent's rendered output (which embeds the children
// inline) has changed, leaving a stale parent entry in the list
// cache. Always bumping is cheap (one uint64 increment) and called
// at most once per agent event; in the rare case the slice is
// truly unchanged the worst case is one extra parent re-render
// while every child cache hit stays warm.
func (a *AgentToolMessageItem) SetNestedTools(tools []ToolMessageItem) {
	a.nestedTools = tools
	a.clearCache()
	a.Bump()
}

// AddNestedTool adds a nested tool.
func (a *AgentToolMessageItem) AddNestedTool(tool ToolMessageItem) {
	// Mark nested tools as simple (compact) rendering.
	if s, ok := tool.(Compactable); ok {
		s.SetCompact(true)
	}
	a.nestedTools = append(a.nestedTools, tool)
	a.clearCache()
	a.Bump()
}

// AgentToolRenderContext renders agent tool messages.
type AgentToolRenderContext struct {
	agent *AgentToolMessageItem
}

// RenderTool implements the [ToolRenderer] interface.
func (r *AgentToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := width
	if !opts.ToolCall.Finished && !opts.IsCanceled() && len(r.agent.nestedTools) == 0 {
		return pendingTool(sty, "Agent", opts.Anim, opts.Compact)
	}

	var params agent.AgentParams
	_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	prompt := params.Prompt
	prompt = strings.ReplaceAll(prompt, "\n", " ")

	header := toolHeader(sty, opts.Status, "Agent", cappedWidth, opts.Compact)
	if opts.Compact {
		return header
	}

	// Build the task tag and prompt.
	taskTag := sty.Tool.AgentTaskTag.Render("Task")
	taskTagWidth := lipgloss.Width(taskTag)

	// Calculate remaining width for prompt.
	remainingWidth := min(cappedWidth-taskTagWidth-3, maxTextWidth-taskTagWidth-3) // -3 for spacing

	promptText := sty.Tool.AgentPrompt.Width(remainingWidth).Render(prompt)

	header = lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		lipgloss.JoinHorizontal(
			lipgloss.Left,
			taskTag,
			" ",
			promptText,
		),
	)

	// Build tree with nested tool calls.
	childTools := tree.Root(header)

	for _, nestedTool := range r.agent.nestedTools {
		childView := nestedTool.Render(remainingWidth)
		childTools.Child(childView)
	}

	// Build parts.
	var parts []string
	parts = append(parts, childTools.
		Enumerator(roundedEnumerator(2, taskTagWidth-5)).
		Indenter(roundedIndenter(2, taskTagWidth-5)).
		String())

	// Show animation if still running.
	if !opts.HasResult() && !opts.IsCanceled() {
		parts = append(parts, "", opts.Anim.Render())
	}

	result := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Add body content when completed.
	if opts.HasResult() && opts.Result.Content != "" {
		body := toolOutputMarkdownContent(sty, opts.Result.Content, cappedWidth-toolBodyLeftPaddingTotal, opts.ExpandedContent)
		return joinToolParts(result, body)
	}

	return result
}

// -----------------------------------------------------------------------------
// Agentic Fetch Tool
// -----------------------------------------------------------------------------

// AgenticFetchToolMessageItem is a message item that represents an agentic fetch tool call.
type AgenticFetchToolMessageItem struct {
	*baseToolMessageItem

	nestedTools []ToolMessageItem
}

var (
	_ ToolMessageItem     = (*AgenticFetchToolMessageItem)(nil)
	_ NestedToolContainer = (*AgenticFetchToolMessageItem)(nil)
)

// NewAgenticFetchToolMessageItem creates a new [AgenticFetchToolMessageItem].
func NewAgenticFetchToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *AgenticFetchToolMessageItem {
	t := &AgenticFetchToolMessageItem{}
	t.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &AgenticFetchToolRenderContext{fetch: t}, canceled)
	// For the agentic fetch tool we keep spinning until the tool call is finished.
	t.spinningFunc = func(state SpinningState) bool {
		return !state.HasResult() && !state.IsCanceled()
	}
	return t
}

// Advance implements [Animatable]. See [AgentToolMessageItem.Advance]
// for the parent-bump rationale; without an override the embedded base
// Advance would never advance the nested children.
func (a *AgenticFetchToolMessageItem) Advance() bool {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return false
	}
	changed := a.anim.Advance()
	changed = advanceNested(a.nestedTools) || changed
	if changed {
		a.Bump()
	}
	return changed
}

// SetStyles points the fetch tool and its nested tools at sty.
func (a *AgenticFetchToolMessageItem) SetStyles(sty *styles.Styles) {
	if a == nil {
		return
	}
	a.baseToolMessageItem.SetStyles(sty)
	for _, nested := range a.nestedTools {
		nested.SetStyles(sty)
	}
}

// NestedTools returns the nested tools.
func (a *AgenticFetchToolMessageItem) NestedTools() []ToolMessageItem {
	return a.nestedTools
}

// SetNestedTools sets the nested tools. Always bumps the version;
// see [AgentToolMessageItem.SetNestedTools] for the rationale.
func (a *AgenticFetchToolMessageItem) SetNestedTools(tools []ToolMessageItem) {
	a.nestedTools = tools
	a.clearCache()
	a.Bump()
}

// AddNestedTool adds a nested tool.
func (a *AgenticFetchToolMessageItem) AddNestedTool(tool ToolMessageItem) {
	// Mark nested tools as simple (compact) rendering.
	if s, ok := tool.(Compactable); ok {
		s.SetCompact(true)
	}
	a.nestedTools = append(a.nestedTools, tool)
	a.clearCache()
	a.Bump()
}

// AgenticFetchToolRenderContext renders agentic fetch tool messages.
type AgenticFetchToolRenderContext struct {
	fetch *AgenticFetchToolMessageItem
}

// agenticFetchParams matches tools.AgenticFetchParams.
type agenticFetchParams struct {
	URL    string `json:"url,omitempty"`
	Prompt string `json:"prompt"`
}

// RenderTool implements the [ToolRenderer] interface.
func (r *AgenticFetchToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := width
	if !opts.ToolCall.Finished && !opts.IsCanceled() && len(r.fetch.nestedTools) == 0 {
		return pendingTool(sty, "Agentic Fetch", opts.Anim, opts.Compact)
	}

	var params agenticFetchParams
	_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	prompt := params.Prompt
	prompt = strings.ReplaceAll(prompt, "\n", " ")

	// Build header with optional URL param.
	var toolParams []string
	if params.URL != "" {
		toolParams = append(toolParams, params.URL)
	}

	header := toolHeader(sty, opts.Status, "Agentic Fetch", cappedWidth, opts.Compact, toolParams...)
	if opts.Compact {
		return header
	}

	// Build the prompt tag.
	promptTag := sty.Tool.AgenticFetchPromptTag.Render("Prompt")
	promptTagWidth := lipgloss.Width(promptTag)

	// Calculate remaining width for prompt text.
	remainingWidth := min(cappedWidth-promptTagWidth-3, maxTextWidth-promptTagWidth-3) // -3 for spacing

	promptText := sty.Tool.AgentPrompt.Width(remainingWidth).Render(prompt)

	header = lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		lipgloss.JoinHorizontal(
			lipgloss.Left,
			promptTag,
			" ",
			promptText,
		),
	)

	// Build tree with nested tool calls.
	childTools := tree.Root(header)

	for _, nestedTool := range r.fetch.nestedTools {
		childView := nestedTool.Render(remainingWidth)
		childTools.Child(childView)
	}

	// Build parts.
	var parts []string
	parts = append(parts, childTools.
		Enumerator(roundedEnumerator(2, promptTagWidth-5)).
		Indenter(roundedIndenter(2, promptTagWidth-5)).
		String())

	// Show animation if still running.
	if !opts.HasResult() && !opts.IsCanceled() {
		parts = append(parts, "", opts.Anim.Render())
	}

	result := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Add body content when completed.
	if opts.HasResult() && opts.Result.Content != "" {
		body := toolOutputMarkdownContent(sty, opts.Result.Content, cappedWidth-toolBodyLeftPaddingTotal, opts.ExpandedContent)
		return joinToolParts(result, body)
	}

	return result
}

// -----------------------------------------------------------------------------
// Review Tool
// -----------------------------------------------------------------------------

// ReviewToolMessageItem is a message item that represents a review tool
// call. It fans out to N adversarial reviewer sub-agents in parallel;
// each reviewer's nested tool calls are bucketed into its own group and
// rendered as a separate "Reviewer N" sub-tree.
type ReviewToolMessageItem struct {
	*baseToolMessageItem

	// groups[i] holds the nested tool calls made by reviewer i.
	groups [][]ToolMessageItem
}

var (
	_ ToolMessageItem            = (*ReviewToolMessageItem)(nil)
	_ NestedToolContainer        = (*ReviewToolMessageItem)(nil)
	_ GroupedNestedToolContainer = (*ReviewToolMessageItem)(nil)
)

// NewReviewToolMessageItem creates a new [ReviewToolMessageItem].
func NewReviewToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *ReviewToolMessageItem {
	t := &ReviewToolMessageItem{}
	t.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &ReviewToolRenderContext{review: t}, canceled)
	// Keep spinning until the tool call is finished.
	t.spinningFunc = func(state SpinningState) bool {
		return !state.HasResult() && !state.IsCanceled()
	}
	return t
}

// Advance implements [Animatable]. See [AgentToolMessageItem.Advance]
// for the parent-bump rationale.
func (a *ReviewToolMessageItem) Advance() bool {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return false
	}
	changed := a.anim.Advance()
	for _, group := range a.groups {
		changed = advanceNested(group) || changed
	}
	if changed {
		a.Bump()
	}
	return changed
}

// SetStyles points the review tool and every reviewer group's tools at sty.
func (a *ReviewToolMessageItem) SetStyles(sty *styles.Styles) {
	if a == nil {
		return
	}
	a.baseToolMessageItem.SetStyles(sty)
	for _, nested := range a.NestedTools() {
		nested.SetStyles(sty)
	}
}

// NestedTools returns the flattened set of nested tools across all
// reviewer groups (used for ID registration and animation dispatch).
func (a *ReviewToolMessageItem) NestedTools() []ToolMessageItem {
	var all []ToolMessageItem
	for _, group := range a.groups {
		all = append(all, group...)
	}
	return all
}

// SetNestedTools replaces group 0. Kept for the [NestedToolContainer]
// contract / non-grouped callers; grouped callers use
// SetNestedToolsForGroup.
func (a *ReviewToolMessageItem) SetNestedTools(tools []ToolMessageItem) {
	a.SetNestedToolsForGroup(0, tools)
}

// AddNestedTool appends a nested tool to group 0.
func (a *ReviewToolMessageItem) AddNestedTool(tool ToolMessageItem) {
	if s, ok := tool.(Compactable); ok {
		s.SetCompact(true)
	}
	if len(a.groups) == 0 {
		a.groups = append(a.groups, nil)
	}
	a.groups[0] = append(a.groups[0], tool)
	a.clearCache()
	a.Bump()
}

// NestedToolsForGroup returns the nested tools for reviewer group g.
func (a *ReviewToolMessageItem) NestedToolsForGroup(g int) []ToolMessageItem {
	if g < 0 || g >= len(a.groups) {
		return nil
	}
	return a.groups[g]
}

// SetNestedToolsForGroup replaces the nested tools for group g, growing
// the group set as needed. Always bumps the version; see
// [AgentToolMessageItem.SetNestedTools] for the rationale.
func (a *ReviewToolMessageItem) SetNestedToolsForGroup(g int, tools []ToolMessageItem) {
	if g < 0 {
		return
	}
	for len(a.groups) <= g {
		a.groups = append(a.groups, nil)
	}
	a.groups[g] = tools
	a.clearCache()
	a.Bump()
}

// GroupCount returns the number of reviewer groups currently tracked.
func (a *ReviewToolMessageItem) GroupCount() int {
	return len(a.groups)
}

type reviewAction struct {
	verb string
	arg  string
}

func reviewActionFor(tool ToolMessageItem) reviewAction {
	call := tool.ToolCall()
	params := decodeToolInput(call.Input)
	switch call.Name {
	case "grep", "glob", "sourcegraph", "web_search", "search_history", "lsp_references":
		return reviewAction{"Searched", reviewArg(params, true)}
	case "view", "multi_view", "fetch", "web_fetch", "agentic_fetch", "read_mcp_resource":
		return reviewAction{"Read", reviewArg(params, false)}
	case "ls":
		return reviewAction{"Listed", reviewArg(params, false)}
	case "bash":
		return reviewAction{"Ran", reviewArg(params, false)}
	default:
		return reviewAction{prettifyToolName(call.Name), reviewArg(params, false)}
	}
}

func reviewArg(params map[string]any, search bool) string {
	keys := []string{"file_path", "path", "url", "command", "prompt", "name"}
	if search {
		keys = []string{"pattern", "query", "file_path", "path", "url"}
	}
	for _, key := range keys {
		v, ok := params[key].(string)
		if !ok || v == "" {
			continue
		}
		v = strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
		if key == "file_path" || key == "path" {
			v = path.Base(v)
		}
		if search {
			v = reviewSearchTerms(v)
		}
		return v
	}
	if vals := toolSummaryParams(params); len(vals) > 0 {
		return strings.TrimSpace(strings.ReplaceAll(vals[0], "\n", " "))
	}
	return ""
}

func reviewSearchTerms(pattern string) string {
	if i := strings.IndexAny(pattern, "|&"); i >= 0 {
		pattern = pattern[:i]
	}
	var b strings.Builder
	for _, r := range pattern {
		switch {
		case r == ' ' || r == '_' || r == '-' || r == '.':
			b.WriteRune(' ')
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func reviewToolLine(sty *styles.Styles, tool ToolMessageItem) string {
	action := reviewActionFor(tool)
	name := sty.Tool.NameNested.Render(prettifyToolName(tool.ToolCall().Name))
	if action.arg == "" {
		return name
	}
	return name + " " + sty.Tool.ParamMain.Render(action.arg)
}

const (
	reviewSummaryRows = 3
	reviewSummaryArgs = 4
)

func reviewSummaryLines(sty *styles.Styles, group []ToolMessageItem) []string {
	actions := make([]reviewAction, len(group))
	for i, tool := range group {
		actions[i] = reviewActionFor(tool)
	}
	var lines []string
	shown := 0
	for shown < len(actions) && len(lines) < reviewSummaryRows {
		verb := actions[shown].verb
		end := shown + 1
		for end < len(actions) && actions[end].verb == verb {
			end++
		}
		if len(lines) == reviewSummaryRows-1 && end < len(actions) {
			lines = append(lines, sty.Tool.ParamMain.Render(fmt.Sprintf("+%d more", len(actions)-shown)))
			return lines
		}
		lines = append(lines, reviewRunLine(sty, verb, actions[shown:end]))
		shown = end
	}
	return lines
}

func reviewRunLine(sty *styles.Styles, verb string, run []reviewAction) string {
	var args []string
	seen := map[string]struct{}{}
	extra := 0
	for _, action := range run {
		if action.arg == "" {
			extra++
			continue
		}
		if _, ok := seen[action.arg]; ok {
			extra++
			continue
		}
		if len(args) == reviewSummaryArgs {
			extra++
			continue
		}
		seen[action.arg] = struct{}{}
		args = append(args, action.arg)
	}
	line := sty.Tool.NameNested.Render(verb)
	if len(args) > 0 {
		line += " " + sty.Tool.ParamMain.Render(strings.Join(args, ", "))
	}
	if extra > 0 {
		line += sty.Tool.ParamMain.Render(fmt.Sprintf(" +%d more", extra))
	}
	return line
}

func decodeToolInput(input string) map[string]any {
	var params map[string]any
	_ = json.Unmarshal([]byte(input), &params)
	return params
}

// ReviewToolRenderContext renders review tool messages.
type ReviewToolRenderContext struct {
	review *ReviewToolMessageItem
}

// reviewParams matches the JSON shape of agent.ReviewParams.
type reviewParams struct {
	Command string `json:"command"`
	Goal    string `json:"goal,omitempty"`
	Focus   string `json:"focus,omitempty"`
}

// RenderTool implements the [ToolRenderer] interface.
func (r *ReviewToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := width
	if !opts.ToolCall.Finished && !opts.IsCanceled() && r.review.GroupCount() == 0 {
		return pendingTool(sty, "Review", opts.Anim, opts.Compact)
	}

	var params reviewParams
	_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	header := toolHeader(sty, opts.Status, "Review", cappedWidth, opts.Compact)
	if opts.Compact && !sty.Minimal {
		return header
	}

	// Show the command being reviewed, and the goal if present.
	taskTag := sty.Tool.AgentTaskTag.Render("Adversarial")
	taskTagWidth := lipgloss.Width(taskTag)
	remainingWidth := min(cappedWidth-taskTagWidth-3, maxTextWidth-taskTagWidth-3)

	label := params.Command
	if params.Goal != "" {
		label = params.Goal
	}
	label = strings.ReplaceAll(label, "\n", " ")
	labelText := sty.Tool.AgentPrompt.Width(remainingWidth).Render(label)

	if !sty.Minimal {
		header = lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			"",
			lipgloss.JoinHorizontal(lipgloss.Left, taskTag, " ", labelText),
		)
	}

	if sty.Minimal {
		return r.renderMinimal(sty, cappedWidth, opts, header)
	}

	// One sub-tree per reviewer group, so N reviewers render as N
	// independent branches.
	childTools := tree.Root(header)
	for i, group := range r.review.groups {
		reviewerLabel := sty.Tool.AgentPrompt.Render(fmt.Sprintf("Reviewer %d", i+1))
		reviewerNode := tree.Root(reviewerLabel)
		for _, nestedTool := range group {
			reviewerNode.Child(nestedTool.Render(remainingWidth - 2))
		}
		childTools.Child(reviewerNode)
	}

	var parts []string
	parts = append(parts, childTools.
		Enumerator(roundedEnumerator(2, taskTagWidth-5)).
		Indenter(roundedIndenter(2, taskTagWidth-5)).
		String())

	if !opts.HasResult() && !opts.IsCanceled() {
		parts = append(parts, "", opts.Anim.Render())
	}

	result := lipgloss.JoinVertical(lipgloss.Left, parts...)

	if opts.HasResult() && opts.Result.Content != "" {
		body := toolOutputMarkdownContent(sty, opts.Result.Content, cappedWidth-toolBodyLeftPaddingTotal, opts.ExpandedContent)
		return joinToolParts(result, body)
	}

	return result
}

func (r *ReviewToolRenderContext) renderMinimal(sty *styles.Styles, width int, opts *ToolRenderOpts, header string) string {
	total := 0
	for _, group := range r.review.groups {
		total += len(group)
	}
	title := strings.TrimRight(strings.Replace(header, "●", toolIcon(sty, opts.Status), 1), " ")
	if total > 0 {
		title += sty.Tool.ParamMain.Render(" · " + reviewCallCount(total))
	}

	var b strings.Builder
	b.WriteString(title)
	const pad = "  "
	for i, group := range r.review.groups {
		branch := "├"
		if i == len(r.review.groups)-1 {
			branch = "└"
		}
		label := fmt.Sprintf("Reviewer %d", i+1)
		if n := len(group); n > 0 {
			label += " · " + reviewCallCount(n)
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s│\n%s%s %s", pad, pad, branch, sty.Tool.ParamMain.Render(label))

		rows := reviewSummaryLines(sty, group)
		if opts.ExpandedContent {
			rows = make([]string, len(group))
			for j, tool := range group {
				rows[j] = reviewToolLine(sty, tool)
			}
		}
		if len(rows) > 0 {
			fmt.Fprintf(&b, "\n%s│ │ ", pad)
		}
		for j, row := range rows {
			toolBranch := "├"
			if j == len(rows)-1 {
				toolBranch = "└"
			}
			fmt.Fprintf(&b, "\n%s│ %s %s", pad, toolBranch, row)
		}
	}

	result := b.String()
	if !opts.HasResult() && !opts.IsCanceled() && opts.Anim != nil {
		result += "\n" + opts.Anim.Render()
	}
	if opts.HasResult() && opts.Result.Content != "" && opts.ExpandedContent {
		body := toolOutputMarkdownContent(sty, opts.Result.Content, width-toolBodyLeftPaddingTotal, true)
		return joinToolParts(result, body)
	}
	return result
}

func reviewCallCount(n int) string {
	if n == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", n)
}
