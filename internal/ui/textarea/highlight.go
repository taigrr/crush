package textarea

import (
	"slices"

	"charm.land/lipgloss/v2"
)

// Highlight is a styled range of the buffer, independent of the selection,
// delimited by two marks so it follows the text through edits. It is
// purely presentational and does not move the cursor. A highlight whose
// marks have been removed, or that has collapsed to zero width, is not
// drawn.
type Highlight struct {
	Start, End MarkID
	Style      lipgloss.Style
}

// SetHighlights replaces the set of styled ranges drawn over the text.
// Later highlights take precedence where ranges overlap; an active
// selection takes precedence over all highlights.
func (m *Model) SetHighlights(hs ...Highlight) {
	m.highlights = slices.Clone(hs)
}

// ClearHighlights removes every highlight.
func (m *Model) ClearHighlights() {
	m.highlights = nil
}

// Highlights returns the current highlights.
func (m Model) Highlights() []Highlight {
	return slices.Clone(m.highlights)
}

// highlightRange resolves a highlight's marks to a normalized position
// range, or false when it cannot be drawn.
func (m Model) highlightRange(h Highlight) (start, end Position, ok bool) {
	start, ok1 := m.Mark(h.Start)
	end, ok2 := m.Mark(h.End)
	if !ok1 || !ok2 || start == end {
		return Position{}, Position{}, false
	}
	if end.before(start) {
		start, end = end, start
	}
	return start, end, true
}

// styledSpan is a run of runes within one wrapped segment that renders
// with a non-default style. Offsets are relative to the segment.
type styledSpan struct {
	from, to int
	style    lipgloss.Style
}

// rangeSpanFor is [Model.selectionSpanFor] for an arbitrary range.
func (m Model) rangeSpanFor(start, end Position, row, base, length int) (from, to int, ok bool) {
	if row < start.Row || row > end.Row {
		return 0, 0, false
	}
	lineLen := len(m.value[row])
	rowFrom, rowTo := 0, lineLen
	if row == start.Row {
		rowFrom = clamp(start.Col, 0, lineLen)
	}
	if row == end.Row {
		rowTo = clamp(end.Col, 0, lineLen)
	}
	from = max(rowFrom, base)
	to = min(rowTo, base+length)
	if from >= to {
		return 0, 0, false
	}
	return from - base, to - base, true
}

// segmentSpans returns the non-overlapping, ordered styled spans for the
// wrapped segment [base, base+length) of logical row. Highlights are
// layered in order, then the selection is layered on top.
func (m Model) segmentSpans(row, base, length int, selection lipgloss.Style) []styledSpan {
	var spans []styledSpan
	for _, h := range m.highlights {
		start, end, ok := m.highlightRange(h)
		if !ok {
			continue
		}
		if from, to, ok := m.rangeSpanFor(start, end, row, base, length); ok {
			spans = layerSpan(spans, styledSpan{from: from, to: to, style: h.Style})
		}
	}
	if from, to, ok := m.selectionSpanFor(row, base, length); ok {
		spans = layerSpan(spans, styledSpan{from: from, to: to, style: selection})
	}
	return spans
}

// layerSpan inserts s over spans, trimming or splitting any existing span
// it overlaps so the result stays disjoint and ordered.
func layerSpan(spans []styledSpan, s styledSpan) []styledSpan {
	out := make([]styledSpan, 0, len(spans)+2)
	for _, e := range spans {
		switch {
		case e.to <= s.from || e.from >= s.to:
			out = append(out, e)
		case e.from < s.from && e.to > s.to:
			out = append(out, styledSpan{e.from, s.from, e.style}, styledSpan{s.to, e.to, e.style})
		case e.from < s.from:
			out = append(out, styledSpan{e.from, s.from, e.style})
		case e.to > s.to:
			out = append(out, styledSpan{s.to, e.to, e.style})
		}
	}
	out = append(out, s)
	slices.SortFunc(out, func(a, b styledSpan) int { return a.from - b.from })
	return out
}

// spanAt returns the span covering segment offset i, if any.
func spanAt(spans []styledSpan, i int) (styledSpan, bool) {
	for _, s := range spans {
		if i >= s.from && i < s.to {
			return s, true
		}
	}
	return styledSpan{}, false
}

// renderSegment renders one wrapped segment: base-styled text with the
// given spans layered over it, and the virtual cursor drawn at cursorCol
// (segment-relative) when cursorCol >= 0. A cursor inside a span keeps the
// span's style behind it so the cell is not double-styled.
func (m *Model) renderSegment(segment []rune, base lipgloss.Style, spans []styledSpan, cursorCol int) string {
	if len(spans) == 0 && cursorCol < 0 {
		return base.Render(string(segment))
	}
	var b []byte
	flush := func(from, to int, st lipgloss.Style) {
		if from < to {
			b = append(b, st.Render(string(segment[from:to]))...)
		}
	}
	i := 0
	for i < len(segment) {
		st := base
		to := len(segment)
		if s, ok := spanAt(spans, i); ok {
			st, to = s.style, s.to
		} else {
			for _, s := range spans {
				if s.from > i && s.from < to {
					to = s.from
				}
			}
		}
		if cursorCol >= i && cursorCol < to {
			flush(i, cursorCol, st)
			m.virtualCursor.SetChar(string(segment[cursorCol]))
			b = append(b, st.Render(m.virtualCursor.View())...)
			i = cursorCol + 1
			flush(i, to, st)
		} else {
			flush(i, to, st)
		}
		i = to
	}
	return string(b)
}
