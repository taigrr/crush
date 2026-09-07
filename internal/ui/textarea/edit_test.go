package textarea

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newEditModel(value string) Model {
	m := New()
	m.SetWidth(40)
	m.SetHeight(5)
	m.ShowLineNumbers = false
	m.Focus()
	m.SetValue(value)
	return m
}

func TestReplaceRangeSameRow(t *testing.T) {
	t.Parallel()
	m := newEditModel("alpha omega")
	m.SetCursor(Position{Row: 0, Col: 11})
	end := m.ReplaceRange(Position{0, 5}, Position{0, 5}, " beta")
	require.Equal(t, "alpha beta omega", m.Value())
	require.Equal(t, Position{0, 10}, end)
	require.Equal(t, Position{0, 16}, m.CursorPosition(), "cursor after the edit shifts with it")

	m.SetCursor(Position{0, 2})
	m.ReplaceRange(Position{0, 5}, Position{0, 10}, " gamma delta")
	require.Equal(t, "alpha gamma delta omega", m.Value())
	require.Equal(t, Position{0, 2}, m.CursorPosition(), "cursor before the edit is untouched")

	m.SetCursor(Position{0, 8})
	end = m.ReplaceRange(Position{0, 5}, Position{0, 17}, "")
	require.Equal(t, "alpha omega", m.Value())
	require.Equal(t, Position{0, 5}, end)
	require.Equal(t, Position{0, 5}, m.CursorPosition(), "cursor inside a removed range moves to the edit point")

	m.SetCursor(Position{0, 5})
	m.ReplaceRange(Position{0, 5}, Position{0, 5}, " x")
	require.Equal(t, Position{0, 7}, m.CursorPosition(), "cursor at a pure insertion point follows the text")

	m.SetCursor(Position{0, 5})
	m.ReplaceRange(Position{0, 5}, Position{0, 7}, " yy")
	require.Equal(t, Position{0, 5}, m.CursorPosition(), "cursor at the start of a replaced range stays in front")
}

func TestReplaceRangeAcrossRows(t *testing.T) {
	t.Parallel()
	m := newEditModel("one\ntwo\nthree")
	m.SetCursor(Position{2, 3})
	end := m.ReplaceRange(Position{0, 2}, Position{1, 1}, "X\nY\nZ")
	require.Equal(t, "onX\nY\nZwo\nthree", m.Value())
	require.Equal(t, Position{2, 1}, end)
	require.Equal(t, Position{3, 3}, m.CursorPosition(), "later rows shift by the row delta")

	m.ReplaceRange(Position{0, 0}, Position{2, 1}, "")
	require.Equal(t, "wo\nthree", m.Value())
	require.Equal(t, Position{1, 3}, m.CursorPosition())
	require.Equal(t, "o\nth", m.TextIn(Position{0, 1}, Position{1, 2}))
}

func TestReplaceRangePreservesSelectionAndReversedArgs(t *testing.T) {
	t.Parallel()
	m := newEditModel("hello world")
	m.selectFrom(Position{0, 6}, Position{0, 11})
	m.ReplaceRange(Position{0, 5}, Position{0, 0}, "HELLO")
	require.Equal(t, "HELLO world", m.Value())
	start, end, ok := m.Selection()
	require.True(t, ok)
	require.Equal(t, Position{0, 6}, start)
	require.Equal(t, Position{0, 11}, end)
}

func TestHighlightsRenderWithoutChangingText(t *testing.T) {
	t.Parallel()
	m := newEditModel("hello brave world")
	m.SetVirtualCursor(false)
	plain := m.View()
	hl := lipgloss.NewStyle().Italic(true)
	m.SetHighlights(Highlight{Start: m.AddMark(Position{0, 5}, GravityLeft), End: m.AddMark(Position{0, 11}, GravityLeft), Style: hl})
	styled := m.View()
	require.Equal(t, ansi.Strip(plain), ansi.Strip(styled))
	require.NotEqual(t, plain, styled)
	require.Contains(t, styled, "\x1b[3m")
	require.Len(t, m.Highlights(), 1)
	m.ClearHighlights()
	require.Equal(t, plain, m.View())
}

func TestHighlightsAcrossWrappedAndMultipleRows(t *testing.T) {
	t.Parallel()
	m := newEditModel("aaaa bbbb cccc dddd eeee\nffff gggg")
	m.SetWidth(12)
	m.SetVirtualCursor(false)
	hl := lipgloss.NewStyle().Italic(true)
	m.SetHighlights(Highlight{Start: m.AddMark(Position{0, 5}, GravityLeft), End: m.AddMark(Position{1, 4}, GravityLeft), Style: hl})
	styled := m.View()
	require.Equal(t, "aaaa bbbb cccc dddd eeee\nffff gggg", m.Value())
	require.Equal(t, ansi.Strip(m.View()), ansi.Strip(styled))
	require.GreaterOrEqual(t, countItalicRuns(styled), 3, "a range spanning wrapped segments styles each one")
}

func TestLayerSpanSplitsAndOrders(t *testing.T) {
	t.Parallel()
	a := lipgloss.NewStyle().Bold(true)
	b := lipgloss.NewStyle().Italic(true)
	spans := layerSpan(nil, styledSpan{0, 10, a})
	spans = layerSpan(spans, styledSpan{3, 5, b})
	require.Len(t, spans, 3)
	require.Equal(t, []int{0, 3, 5}, []int{spans[0].from, spans[1].from, spans[2].from})
	require.Equal(t, []int{3, 5, 10}, []int{spans[0].to, spans[1].to, spans[2].to})
	spans = layerSpan(spans, styledSpan{0, 20, b})
	require.Len(t, spans, 1)
}

func countItalicRuns(s string) int {
	n := 0
	for i := 0; i+3 < len(s); i++ {
		if s[i:i+4] == "\x1b[3m" {
			n++
		}
	}
	return n
}
