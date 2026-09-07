package textarea

import "slices"

// MarkID identifies a [Model] mark.
type MarkID int

// Gravity decides which side of a mark text inserted exactly at the mark
// ends up on.
type Gravity int

const (
	// GravityLeft keeps the mark in front of text inserted at it, so the
	// insertion lands after the mark. Use it for the end of a span.
	GravityLeft Gravity = iota
	// GravityRight pushes the mark past text inserted at it, so the
	// insertion lands before the mark. Use it for the start of a span.
	GravityRight
)

// A mark is a buffer position that the textarea keeps pointing at the same
// logical character as the text around it is edited: text inserted or
// removed before a mark shifts it, text inserted at a mark goes to the
// side chosen by its [Gravity], and removing the text a mark sits in
// collapses the mark onto the edit point. Marks make it possible to track
// a span of text — for example dictation still being transcribed — across
// the user's own typing without searching the buffer for it.
//
// Tracking is derived, not instrumented: every public mutation entry
// point snapshots the buffer beforehand and [Model.trackEdit] maps marks
// through the single replaced region found by a common prefix/suffix
// diff. Every editing command the textarea offers replaces one contiguous
// region, so this is exact for them; a caller's [Model.SetValue] with an
// unrelated buffer degrades gracefully to collapsing marks inside the
// changed region.
type mark struct {
	id      MarkID
	pos     int // flat rune offset; rows are joined by '\n'.
	gravity Gravity
}

// AddMark places a mark at pos and returns its id.
func (m *Model) AddMark(pos Position, g Gravity) MarkID {
	m.markSeq++
	id := m.markSeq
	m.marks = append(m.marks, mark{id: id, pos: m.flatOffset(m.clampPos(pos)), gravity: g})
	return id
}

// Mark returns the current position of a mark, or false if it was removed.
func (m Model) Mark(id MarkID) (Position, bool) {
	for _, mk := range m.marks {
		if mk.id == id {
			return m.positionAt(mk.pos), true
		}
	}
	return Position{}, false
}

// SetMark moves an existing mark to pos.
func (m *Model) SetMark(id MarkID, pos Position) {
	for i := range m.marks {
		if m.marks[i].id == id {
			m.marks[i].pos = m.flatOffset(m.clampPos(pos))
			return
		}
	}
}

// RemoveMark deletes a mark. Highlights referencing it stop rendering.
func (m *Model) RemoveMark(id MarkID) {
	m.marks = slices.DeleteFunc(m.marks, func(mk mark) bool { return mk.id == id })
}

// ClearMarks removes every mark.
func (m *Model) ClearMarks() {
	m.marks = nil
}

// flatOffset converts a buffer position to a flat rune offset.
func (m Model) flatOffset(p Position) int {
	n := 0
	for row := 0; row < p.Row && row < len(m.value); row++ {
		n += len(m.value[row]) + 1
	}
	return n + p.Col
}

// positionAt converts a flat rune offset back to a buffer position,
// clamped to the buffer.
func (m Model) positionAt(off int) Position {
	if off < 0 {
		off = 0
	}
	for row, line := range m.value {
		if off <= len(line) {
			return Position{Row: row, Col: off}
		}
		off -= len(line) + 1
	}
	last := len(m.value) - 1
	if last < 0 {
		return Position{}
	}
	return Position{Row: last, Col: len(m.value[last])}
}

// flatten joins the buffer rows with '\n'. It returns nil when there are
// no marks, since the snapshot is only needed to adjust them.
func (m Model) flatten() []rune {
	if len(m.marks) == 0 {
		return nil
	}
	n := 0
	for _, line := range m.value {
		n += len(line) + 1
	}
	out := make([]rune, 0, n)
	for i, line := range m.value {
		out = append(out, line...)
		if i < len(m.value)-1 {
			out = append(out, '\n')
		}
	}
	return out
}

// trackEdit adjusts marks for the change from the before snapshot to the
// current buffer, locating the replaced region by common prefix/suffix.
// It is a no-op when there are no marks. Where the caller knows the exact
// region (see [Model.ReplaceRange]) it should use shiftMarks directly,
// since a diff cannot distinguish equivalent edits such as deleting
// " gone" versus "gone ".
func (m *Model) trackEdit(before []rune) {
	if len(m.marks) == 0 || before == nil {
		return
	}
	after := m.flatten()
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	m.shiftMarks(prefix, len(before)-suffix, len(after)-suffix)
}

// shiftMarks maps marks through the replacement of flat range
// [start, oldEnd) by [start, newEnd). Marks strictly inside the replaced
// range collapse to start; a mark on the range's start or end boundary
// keeps delimiting it. For a pure insertion (start == oldEnd) a mark at
// the point follows its gravity.
func (m *Model) shiftMarks(start, oldEnd, newEnd int) {
	if start == oldEnd && oldEnd == newEnd {
		return
	}
	insertion := start == oldEnd
	for i := range m.marks {
		mk := &m.marks[i]
		switch {
		case mk.pos < start:
		case mk.pos == start && insertion:
			if mk.gravity == GravityRight {
				mk.pos = newEnd
			}
		case mk.pos == start:
		case mk.pos >= oldEnd:
			mk.pos += newEnd - oldEnd
		default:
			mk.pos = start
		}
	}
}
