package textarea

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func markPos(t *testing.T, m Model, id MarkID) Position {
	t.Helper()
	p, ok := m.Mark(id)
	require.True(t, ok)
	return p
}

func TestMarksTrackTypingAroundThem(t *testing.T) {
	t.Parallel()
	m := newEditModel("hello world")
	start := m.AddMark(Position{0, 5}, GravityRight)
	end := m.AddMark(Position{0, 11}, GravityLeft)

	// Type before the marked span: both shift.
	m.SetCursor(Position{0, 0})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	require.Equal(t, "Xhello world", m.Value())
	require.Equal(t, Position{0, 6}, markPos(t, m, start))
	require.Equal(t, Position{0, 12}, markPos(t, m, end))

	// Type exactly at the start mark: right gravity pushes the span past
	// the typed text.
	m.SetCursor(Position{0, 6})
	m, _ = m.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	require.Equal(t, "Xhello! world", m.Value())
	require.Equal(t, Position{0, 7}, markPos(t, m, start))
	require.Equal(t, Position{0, 13}, markPos(t, m, end))

	// Type inside the span: it grows.
	m.SetCursor(Position{0, 9})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	require.Equal(t, "Xhello! wZorld", m.Value())
	require.Equal(t, Position{0, 7}, markPos(t, m, start))
	require.Equal(t, Position{0, 14}, markPos(t, m, end))

	// Backspace before the span: shifts back.
	m.SetCursor(Position{0, 1})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "hello! wZorld", m.Value())
	require.Equal(t, Position{0, 6}, markPos(t, m, start))
	require.Equal(t, Position{0, 13}, markPos(t, m, end))

	// Type at the end mark: left gravity keeps the typed text outside.
	m.SetCursor(Position{0, 13})
	m, _ = m.Update(tea.KeyPressMsg{Code: '.', Text: "."})
	require.Equal(t, Position{0, 6}, markPos(t, m, start))
	require.Equal(t, Position{0, 13}, markPos(t, m, end))
}

func TestMarksTrackLineInsertedAbove(t *testing.T) {
	t.Parallel()
	m := newEditModel("ab spoken")
	start := m.AddMark(Position{0, 2}, GravityLeft)
	end := m.AddMark(Position{0, 9}, GravityLeft)
	m.SetCursor(Position{0, 0})
	m.InsertString("first\n")
	require.Equal(t, "first\nab spoken", m.Value())
	require.Equal(t, Position{1, 2}, markPos(t, m, start))
	require.Equal(t, Position{1, 9}, markPos(t, m, end))
	require.Equal(t, " spoken", m.TextIn(markPos(t, m, start), markPos(t, m, end)))
}

func TestMarksCollapseWhenSpanDeleted(t *testing.T) {
	t.Parallel()
	m := newEditModel("keep gone keep")
	start := m.AddMark(Position{0, 4}, GravityLeft)
	end := m.AddMark(Position{0, 9}, GravityLeft)
	m.ReplaceRange(Position{0, 4}, Position{0, 9}, "")
	require.Equal(t, "keep keep", m.Value())
	require.Equal(t, markPos(t, m, start), markPos(t, m, end), "span collapses to zero width")

	m.SetValue("completely different")
	s, e := markPos(t, m, start), markPos(t, m, end)
	require.Equal(t, s, e)
}

func TestReplaceRangeBetweenMarks(t *testing.T) {
	t.Parallel()
	m := newEditModel("alpha bet omega")
	start := m.AddMark(Position{0, 5}, GravityLeft)
	end := m.AddMark(Position{0, 9}, GravityLeft)
	m.SetCursor(Position{0, 15})
	insEnd := m.ReplaceRange(markPos(t, m, start), markPos(t, m, end), " beta gamma")
	require.Equal(t, "alpha beta gamma omega", m.Value())
	require.Equal(t, Position{0, 5}, markPos(t, m, start))
	require.Equal(t, insEnd, markPos(t, m, end), "end mark follows the replacement")
	require.Equal(t, Position{0, 22}, m.CursorPosition())
}

func TestResetDropsMarksAndHighlights(t *testing.T) {
	t.Parallel()
	m := newEditModel("x")
	id := m.AddMark(Position{0, 1}, GravityLeft)
	m.SetHighlights(Highlight{Start: id, End: id})
	m.Reset()
	_, ok := m.Mark(id)
	require.False(t, ok)
	require.Empty(t, m.Highlights())
}
