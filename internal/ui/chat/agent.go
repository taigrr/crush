package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/anim"
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

// Animate progresses the message animation if it should be spinning.
//
// Bumps the parent's F6 list-cache version on both the parent-tick and
// nested-tick branches. Nested tools are not list entries of their
// own — their IDs map to this parent's index in idInxMap
// (internal/ui/model/chat.go:240-246) and their renders are embedded
// inline in this parent's output — so the list only checks the
// parent's version. Without the bump, the list cache would serve the
// previously rendered frame indefinitely and the spinner would appear
// frozen.
func (a *AgentToolMessageItem) Animate(msg anim.StepMsg) tea.Cmd {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return nil
	}
	if msg.ID == a.ID() {
		a.Bump()
		return a.anim.Animate(msg)
	}
	for _, nestedTool := range a.nestedTools {
		if msg.ID != nestedTool.ID() {
			continue
		}
		if s, ok := nestedTool.(Animatable); ok {
			a.Bump()
			return s.Animate(msg)
		}
	}
	return nil
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

// Animate progresses the message animation if it should be spinning.
// See [AgentToolMessageItem.Animate] for the parent-bump rationale —
// without an override, the embedded base.Animate would (a) drop
// StepMsgs whose ID matches a nested child instead of the parent
// (anim.Animate's ID check at internal/ui/anim/anim.go:326-329
// silently returns nil), and (b) never invalidate the parent's
// list-cache entry on a parent tick.
func (a *AgenticFetchToolMessageItem) Animate(msg anim.StepMsg) tea.Cmd {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return nil
	}
	if msg.ID == a.ID() {
		a.Bump()
		return a.anim.Animate(msg)
	}
	for _, nestedTool := range a.nestedTools {
		if msg.ID != nestedTool.ID() {
			continue
		}
		if s, ok := nestedTool.(Animatable); ok {
			a.Bump()
			return s.Animate(msg)
		}
	}
	return nil
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

// Animate progresses the message animation if it should be spinning.
// See [AgentToolMessageItem.Animate] for the parent-bump rationale.
func (a *ReviewToolMessageItem) Animate(msg anim.StepMsg) tea.Cmd {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return nil
	}
	if msg.ID == a.ID() {
		a.Bump()
		return a.anim.Animate(msg)
	}
	for _, group := range a.groups {
		for _, nestedTool := range group {
			if msg.ID != nestedTool.ID() {
				continue
			}
			if s, ok := nestedTool.(Animatable); ok {
				a.Bump()
				return s.Animate(msg)
			}
		}
	}
	return nil
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
	if opts.Compact {
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

	header = lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		lipgloss.JoinHorizontal(lipgloss.Left, taskTag, " ", labelText),
	)

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
