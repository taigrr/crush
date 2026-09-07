package textarea

// SetCursor moves the cursor to pos, clamped to the buffer, and scrolls the
// viewport so it is visible.
func (m *Model) SetCursor(pos Position) {
	m.moveCursorTo(pos)
	m.lastCharOffset = 0
	m.repositionView()
}

// CursorPosition returns the cursor's logical buffer position.
func (m Model) CursorPosition() Position {
	return Position{Row: m.row, Col: m.col}
}

// TextIn returns the text between start and end (order-insensitive),
// with logical lines joined by "\n".
func (m Model) TextIn(start, end Position) string {
	if end.before(start) {
		start, end = end, start
	}
	start, end = m.clampPos(start), m.clampPos(end)
	if start.Row == end.Row {
		return string(m.value[start.Row][start.Col:end.Col])
	}
	var out []rune
	for row := start.Row; row <= end.Row; row++ {
		line := m.value[row]
		switch row {
		case start.Row:
			out = append(out, line[start.Col:]...)
		case end.Row:
			out = append(out, line[:end.Col]...)
		default:
			out = append(out, line...)
		}
		if row < end.Row {
			out = append(out, '\n')
		}
	}
	return string(out)
}

// ReplaceRange replaces the text between start and end (order-insensitive)
// with s and returns the position just past the inserted text. Unlike
// [Model.SetValue] this is a local edit: the viewport, selection, and
// cursor are preserved, with the cursor shifted so it stays on the same
// logical character when it sits after the edited range. A cursor inside
// the replaced range, or at a pure insertion point, moves to the end of
// the inserted text (as if it had typed it); a cursor at the start of a
// non-empty replaced range stays in front of the new text.
//
// s is sanitized like user input; CharLimit and line limits apply.
func (m *Model) ReplaceRange(start, end Position, s string) Position {
	if end.before(start) {
		start, end = end, start
	}
	start, end = m.clampPos(start), m.clampPos(end)
	flatStart, flatEnd := m.flatOffset(start), m.flatOffset(end)
	cursor := Position{Row: m.row, Col: m.col}
	selAnchor, selHead, hadSel := m.selAnchor, m.selHead, m.hasSelection

	// Delete [start, end).
	head := m.value[start.Row][:start.Col]
	tail := m.value[end.Row][end.Col:]
	merged := make([]rune, 0, len(head)+len(tail))
	merged = append(append(merged, head...), tail...)
	removedRows := end.Row - start.Row
	m.value = append(m.value[:start.Row+1], m.value[end.Row+1:]...)
	m.value[start.Row] = merged

	// Insert at start using the regular input path so limits apply, then
	// read back where it ended.
	m.row, m.col = start.Row, start.Col
	m.insertRunesFromUserInput([]rune(s))
	insEnd := Position{Row: m.row, Col: m.col}
	addedRows := insEnd.Row - start.Row

	// Map a position through the edit: unchanged before start (and at
	// start when text was replaced rather than purely inserted), moved to
	// insEnd inside the range, shifted after it.
	remap := func(p Position) Position {
		switch {
		case p.before(start) || p == start && start != end:
			return p
		case p.before(end) || p == end:
			return insEnd
		default:
			if p.Row == end.Row {
				p.Col = p.Col - end.Col + insEnd.Col
			}
			p.Row += addedRows - removedRows
			return m.clampPos(p)
		}
	}

	m.moveCursorTo(remap(cursor))
	if hadSel {
		m.selAnchor, m.selHead = remap(selAnchor), remap(selHead)
	}
	m.lastCharOffset = 0
	m.recalculateHeight()
	m.shiftMarks(flatStart, flatEnd, m.flatOffset(insEnd))
	m.repositionView()
	return insEnd
}

func (m Model) clampPos(p Position) Position {
	if len(m.value) == 0 {
		return Position{}
	}
	p.Row = clamp(p.Row, 0, len(m.value)-1)
	p.Col = clamp(p.Col, 0, len(m.value[p.Row]))
	return p
}
