package common

import "sort"

// MultiSelect is a reusable vim-style multi-selection state machine shared
// by the left session sidebar and the session picker popup. It tracks a set
// of selected item IDs plus an optional "visual mode" in which cursor
// movement sweeps a contiguous, additive range into the selection.
//
// The IDs are opaque strings (session IDs), so the helper is list-agnostic:
// callers pass the ordered ID slice of the navigable region when extending a
// visual sweep. The anchor is stored by ID (not index) so an in-progress
// sweep survives a list reprojection between movements.
//
// Semantics mirror the reviewed sidebar behavior exactly:
//   - Toggle: flip a single ID (builds a non-contiguous selection).
//   - ToggleVisual: enter visual mode anchored at the cursor (selecting it),
//     or, if already in visual mode, exit and clear the whole selection.
//   - ExtendVisual: while in visual mode, add every ID between the anchor and
//     the cursor (inclusive) to the selection. Additive: moving back does not
//     deselect.
//   - Clear: exit visual mode and drop all selected IDs.
type MultiSelect struct {
	selected map[string]bool
	visual   bool
	anchorID string
}

// Selected reports whether id is in the selection set.
func (m *MultiSelect) Selected(id string) bool {
	return m.selected[id]
}

// Count returns how many IDs are selected.
func (m *MultiSelect) Count() int {
	return len(m.selected)
}

// Visual reports whether visual (sweep) mode is active.
func (m *MultiSelect) Visual() bool {
	return m.visual
}

// IDs returns the selected IDs in deterministic (sorted) order.
func (m *MultiSelect) IDs() []string {
	ids := make([]string, 0, len(m.selected))
	for id := range m.selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Toggle flips the membership of a single ID. It works independently of
// visual mode, allowing a non-contiguous selection.
func (m *MultiSelect) Toggle(id string) {
	if id == "" {
		return
	}
	if m.selected == nil {
		m.selected = map[string]bool{}
	}
	if m.selected[id] {
		delete(m.selected, id)
	} else {
		m.selected[id] = true
	}
}

// ToggleVisual enters visual mode anchored at cursorID (selecting it), or, if
// visual mode is already active, exits and clears the whole selection.
func (m *MultiSelect) ToggleVisual(cursorID string) {
	if m.visual {
		m.Clear()
		return
	}
	m.visual = true
	m.anchorID = cursorID
	if m.selected == nil {
		m.selected = map[string]bool{}
	}
	if cursorID != "" {
		m.selected[cursorID] = true
	}
}

// ExtendVisual, while in visual mode, adds every ID between the anchor and the
// cursor (inclusive) in order to the selection. If the anchor ID is no longer
// present in order (e.g. dropped by a refresh), it re-anchors at the cursor so
// the sweep stays coherent. No-op when not in visual mode.
func (m *MultiSelect) ExtendVisual(order []string, cursorID string) {
	if !m.visual {
		return
	}
	if m.selected == nil {
		m.selected = map[string]bool{}
	}
	anchorPos := indexOf(order, m.anchorID)
	cursorPos := indexOf(order, cursorID)
	if cursorPos < 0 {
		return
	}
	if anchorPos < 0 {
		// Anchor vanished: re-anchor at the cursor.
		m.anchorID = cursorID
		anchorPos = cursorPos
	}
	lo, hi := anchorPos, cursorPos
	if lo > hi {
		lo, hi = hi, lo
	}
	for i := lo; i <= hi; i++ {
		m.selected[order[i]] = true
	}
}

// Clear exits visual mode and drops all selected IDs.
func (m *MultiSelect) Clear() {
	m.visual = false
	m.anchorID = ""
	m.selected = map[string]bool{}
}

// SetSelection replaces the selection with exactly the given IDs and exits
// visual mode. Used to keep only the failures selected after a partial bulk
// action.
func (m *MultiSelect) SetSelection(ids []string) {
	m.selected = make(map[string]bool, len(ids))
	for _, id := range ids {
		m.selected[id] = true
	}
	m.visual = false
	m.anchorID = ""
}

// Retain drops any selected ID not present in known, returning the number
// pruned. Callers use this to keep the selection in sync with the currently
// visible/known items (e.g. after a list rebuild or background refresh), so
// stale IDs — including a re-anchored-away visual anchor — never leak into
// downstream consumers. If the current visual anchor is pruned, the anchor
// is cleared too.
func (m *MultiSelect) Retain(known map[string]bool) int {
	pruned := 0
	for id := range m.selected {
		if !known[id] {
			delete(m.selected, id)
			pruned++
		}
	}
	if m.anchorID != "" && !known[m.anchorID] {
		m.anchorID = ""
	}
	return pruned
}

func indexOf(order []string, id string) int {
	if id == "" {
		return -1
	}
	for i, v := range order {
		if v == id {
			return i
		}
	}
	return -1
}
