package chat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

func TestMinimalReviewSummary(t *testing.T) {
	t.Parallel()

	base := themes.CharmtonePantera()
	sty := base.WithMinimal(true)
	item := finishedReview(sty)
	item.SetNestedToolsForGroup(0, []ToolMessageItem{
		reviewChild(sty, "g0", "grep", `{"pattern":"gapBefore"}`),
		reviewChild(sty, "g1", "grep", `{"pattern":"SetConfigField"}`),
		reviewChild(sty, "g2", "grep", `{"pattern":"SetLine"}`),
		reviewChild(sty, "g3", "grep", `{"pattern":"ThemeSwarmSquare"}`),
		reviewChild(sty, "g4", "grep", `{"pattern":"extra"}`),
		reviewChild(sty, "v0", "view", `{"file_path":"/tmp/list.go"}`),
		reviewChild(sty, "v1", "view", `{"file_path":"/tmp/status.go"}`),
		reviewChild(sty, "v2", "view", `{"file_path":"/tmp/layout.go"}`),
	})
	item.SetNestedToolsForGroup(1, []ToolMessageItem{
		reviewChild(sty, "h0", "grep", `{"pattern":"GapChooser"}`),
	})

	plain := stripANSI(item.Render(80))
	require.Contains(t, plain, "Review · 9 calls")
	require.Contains(t, plain, "Reviewer 1 · 8 calls")
	require.Contains(t, plain, "Searched gapBefore, SetConfigField, SetLine, ThemeSwarmSquare")
	lines := strings.Split(plain, "\n")
	var reviewer int
	for i, line := range lines {
		if strings.Contains(line, "Reviewer 1") {
			reviewer = i
			break
		}
	}
	require.Greater(t, reviewer, 1)
	require.Contains(t, lines[reviewer-1], "│")
	require.NotContains(t, lines[reviewer-1], "Reviewer")
	require.Contains(t, lines[reviewer+1], "│")
	require.NotContains(t, lines[reviewer+1], "Searched")
	require.Contains(t, plain, "Read list.go, status.go, layout.go")
	require.Contains(t, plain, "+1 more")
	require.Contains(t, plain, "└ Reviewer 2 · 1 call")
	require.Contains(t, plain, "Searched GapChooser")
	require.NotContains(t, plain, "├ Reviewer 2")
	var reviewer2 int
	for i, line := range lines {
		if strings.Contains(line, "Reviewer 2") {
			reviewer2 = i
			break
		}
	}
	require.Greater(t, reviewer2, reviewer)
	require.Equal(t, "    │", strings.TrimRight(lines[reviewer2-1], " "))
	require.Equal(t, "    │ │", strings.TrimRight(lines[reviewer2+1], " "))
}

func TestMinimalReviewExpandedKeepsRootRail(t *testing.T) {
	t.Parallel()

	base := themes.CharmtonePantera()
	sty := base.WithMinimal(true)
	item := finishedReview(sty)
	item.SetNestedToolsForGroup(0, []ToolMessageItem{
		reviewChild(sty, "a0", "grep", `{"pattern":"one"}`),
	})
	item.SetNestedToolsForGroup(1, []ToolMessageItem{
		reviewChild(sty, "b0", "grep", `{"pattern":"two"}`),
		reviewChild(sty, "b1", "view", `{"file_path":"/tmp/list.go"}`),
	})
	item.ToggleExpanded()

	plain := stripANSI(item.Render(80))
	require.Contains(t, plain, "├ Grep two")
	require.Contains(t, plain, "└ View list.go")
	require.NotContains(t, plain, "/tmp/list.go")
	require.NotContains(t, plain, "+1 more")
}

func TestReviewSummaryCallPlural(t *testing.T) {
	t.Parallel()

	base := themes.CharmtonePantera()
	sty := base.WithMinimal(true)
	item := finishedReview(sty)
	item.SetNestedToolsForGroup(0, []ToolMessageItem{
		reviewChild(sty, "only", "bash", `{"command":"go test"}`),
	})

	plain := stripANSI(item.Render(80))
	require.Contains(t, plain, "Review · 1 call")
	require.NotContains(t, plain, "calls")
	require.Contains(t, plain, "Reviewer 1 · 1 call")
	require.Contains(t, plain, "Ran go test")
}

func finishedReview(sty *styles.Styles) *ReviewToolMessageItem {
	parent := message.ToolCall{ID: "review", Name: "review", Input: `{"command":"git diff"}`, Finished: true}
	return NewReviewToolMessageItem(sty, parent, &message.ToolResult{Content: "done"}, false)
}

func reviewChild(sty *styles.Styles, id, name, input string) ToolMessageItem {
	tc := message.ToolCall{ID: id, Name: name, Input: input, Finished: true}
	return NewToolMessageItem(sty, "msg", tc, &message.ToolResult{Content: "ok"}, false)
}

func TestReviewSummaryFoldsSameVerb(t *testing.T) {
	t.Parallel()

	base := themes.CharmtonePantera()
	sty := base.WithMinimal(true)
	var group []ToolMessageItem
	for i := range 3 {
		group = append(group, reviewChild(sty, fmt.Sprintf("g%d", i), "grep", fmt.Sprintf(`{"pattern":"p%d"}`, i)))
	}
	lines := reviewSummaryLines(sty, group)
	require.Len(t, lines, 1)
	require.Contains(t, stripANSI(lines[0]), "Searched p0, p1, p2")
	require.False(t, strings.Contains(stripANSI(lines[0]), "+"))

	capped := []ToolMessageItem{
		reviewChild(sty, "a", "grep", `{"pattern":"a"}`),
		reviewChild(sty, "b", "grep", `{"pattern":"b"}`),
		reviewChild(sty, "c", "grep", `{"pattern":"c"}`),
		reviewChild(sty, "d", "grep", `{"pattern":"d"}`),
		reviewChild(sty, "e", "grep", `{"pattern":"e"}`),
		reviewChild(sty, "f", "view", `{"file_path":"/tmp/list.go"}`),
	}
	cappedLines := reviewSummaryLines(sty, capped)
	require.Len(t, cappedLines, 2)
	require.Contains(t, stripANSI(cappedLines[0]), "Searched a, b, c, d +1 more")
	require.Contains(t, stripANSI(cappedLines[1]), "Read list.go")

	noisy := reviewSummaryLines(sty, []ToolMessageItem{
		reviewChild(sty, "n", "grep", `{"pattern":"boxEnumerator|boxIndenter|tree\\.Root|Enumerator\\("}`),
	})
	require.Contains(t, stripANSI(noisy[0]), "Searched boxEnumerator")
	require.NotContains(t, stripANSI(noisy[0]), "boxIndenter")
}
