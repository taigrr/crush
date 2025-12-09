package lazylist

import (
	"image"
	"log/slog"
	"strings"

	"charm.land/lipgloss/v2"
)

// List represents a list of items that can be lazily rendered. A list is
// always rendered like a chat conversation where items are stacked vertically
// from top to bottom.
type List struct {
	// Viewport size
	width, height int

	// Items in the list
	items []Item

	// Gap between items (0 or less means no gap)
	gap int

	// Focus and selection state
	focused     bool
	selectedIdx int // The current selected index -1 means no selection

	// Mouse state
	mouseDown       bool
	mouseDownItem   int          // Item index where mouse was pressed
	mouseDownX      int          // X position in item content (character offset)
	mouseDownY      int          // Y position in item (line offset)
	mouseDragItem   int          // Current item index being dragged over
	mouseDragX      int          // Current X in item content
	mouseDragY      int          // Current Y in item
	lastHighlighted map[int]bool // Track which items were highlighted in last update

	// Rendered content and cache
	renderedItems map[int]renderedItem

	// offsetIdx is the index of the first visible item in the viewport.
	offsetIdx int
	// offsetLine is the number of lines of the item at offsetIdx that are
	// scrolled out of view (above the viewport).
	// It must always be >= 0.
	offsetLine int
}

// renderedItem holds the rendered content and height of an item.
type renderedItem struct {
	content string
	height  int
}

// NewList creates a new lazy-loaded list.
func NewList(items ...Item) *List {
	l := new(List)
	l.items = items
	l.renderedItems = make(map[int]renderedItem)
	l.selectedIdx = -1
	l.mouseDownItem = -1
	l.mouseDragItem = -1
	l.lastHighlighted = make(map[int]bool)
	return l
}

// SetSize sets the size of the list viewport.
func (l *List) SetSize(width, height int) {
	if width != l.width {
		l.renderedItems = make(map[int]renderedItem)
	}
	l.width = width
	l.height = height
	// l.normalizeOffsets()
}

// SetGap sets the gap between items.
func (l *List) SetGap(gap int) {
	l.gap = gap
}

// Width returns the width of the list viewport.
func (l *List) Width() int {
	return l.width
}

// Height returns the height of the list viewport.
func (l *List) Height() int {
	return l.height
}

// Len returns the number of items in the list.
func (l *List) Len() int {
	return len(l.items)
}

// getItem renders (if needed) and returns the item at the given index.
func (l *List) getItem(idx int) renderedItem {
	return l.renderItem(idx, false)
}

// renderItem renders (if needed) and returns the item at the given index. If
// process is true, it applies focus and highlight styling.
func (l *List) renderItem(idx int, process bool) renderedItem {
	if idx < 0 || idx >= len(l.items) {
		return renderedItem{}
	}

	var style lipgloss.Style
	focusable, isFocusable := l.items[idx].(FocusStylable)
	if isFocusable {
		style = focusable.BlurStyle()
		if l.focused && idx == l.selectedIdx {
			style = focusable.FocusStyle()
		}
	}

	ri, ok := l.renderedItems[idx]
	if !ok {
		item := l.items[idx]
		rendered := item.Render(l.width - style.GetHorizontalFrameSize())
		height := countLines(rendered)

		ri = renderedItem{
			content: rendered,
			height:  height,
		}

		l.renderedItems[idx] = ri
	}

	if !process {
		return ri
	}

	// We apply highlighting before focus styling so that focus styling
	// overrides highlight styles.
	// Apply highlight if item supports it
	if l.mouseDownItem >= 0 {
		if highlightable, ok := l.items[idx].(HighlightStylable); ok {
			startItemIdx, startLine, startCol, endItemIdx, endLine, endCol := l.getHighlightRange()
			if idx >= startItemIdx && idx <= endItemIdx {
				var sLine, sCol, eLine, eCol int
				if idx == startItemIdx && idx == endItemIdx {
					// Single item selection
					sLine = startLine
					sCol = startCol
					eLine = endLine
					eCol = endCol
				} else if idx == startItemIdx {
					// First item - from start position to end of item
					sLine = startLine
					sCol = startCol
					eLine = ri.height - 1
					eCol = 9999 // 9999 = end of line
				} else if idx == endItemIdx {
					// Last item - from start of item to end position
					sLine = 0
					sCol = 0
					eLine = endLine
					eCol = endCol
				} else {
					// Middle item - fully highlighted
					sLine = 0
					sCol = 0
					eLine = ri.height - 1
					eCol = 9999
				}

				// Apply offset for styling frame
				contentArea := image.Rect(0, 0, l.width, ri.height)

				hiStyle := highlightable.HighlightStyle()
				slog.Info("Highlighting item", "idx", idx,
					"sLine", sLine, "sCol", sCol,
					"eLine", eLine, "eCol", eCol,
				)
				rendered := Highlight(ri.content, contentArea, sLine, sCol, eLine, eCol, ToHighlighter(hiStyle))
				ri.content = rendered
			}
		}
	}

	if isFocusable {
		// Apply focus/blur styling if needed
		rendered := style.Render(ri.content)
		height := countLines(rendered)
		ri.content = rendered
		ri.height = height
	}

	return ri
}

// invalidateItem invalidates the cached rendered content of the item at the
// given index.
func (l *List) invalidateItem(idx int) {
	delete(l.renderedItems, idx)
}

// ScrollToIndex scrolls the list to the given item index.
func (l *List) ScrollToIndex(index int) {
	if index < 0 {
		index = 0
	}
	if index >= len(l.items) {
		index = len(l.items) - 1
	}
	l.offsetIdx = index
	l.offsetLine = 0
}

// ScrollBy scrolls the list by the given number of lines.
func (l *List) ScrollBy(lines int) {
	if len(l.items) == 0 || lines == 0 {
		return
	}

	if lines > 0 {
		// Scroll down
		// Calculate from the bottom how many lines needed to anchor the last
		// item to the bottom
		var totalLines int
		var lastItemIdx int // the last item that can be partially visible
		for i := len(l.items) - 1; i >= 0; i-- {
			item := l.getItem(i)
			totalLines += item.height
			if l.gap > 0 && i < len(l.items)-1 {
				totalLines += l.gap
			}
			if totalLines > l.height-1 {
				lastItemIdx = i
				break
			}
		}

		// Now scroll down by lines
		var item renderedItem
		l.offsetLine += lines
		for {
			item = l.getItem(l.offsetIdx)
			totalHeight := item.height
			if l.gap > 0 {
				totalHeight += l.gap
			}

			if l.offsetIdx >= lastItemIdx || l.offsetLine < totalHeight {
				// Valid offset
				break
			}

			// Move to next item
			l.offsetLine -= totalHeight
			l.offsetIdx++
		}

		if l.offsetLine >= item.height {
			l.offsetLine = item.height
		}
	} else if lines < 0 {
		// Scroll up
		// Calculate from offset how many items needed to fill the viewport
		// This is needed to know when to stop scrolling up
		var totalLines int
		var firstItemIdx int
		for i := l.offsetIdx; i >= 0; i-- {
			item := l.getItem(i)
			totalLines += item.height
			if l.gap > 0 && i < l.offsetIdx {
				totalLines += l.gap
			}
			if totalLines >= l.height {
				firstItemIdx = i
				break
			}
		}

		// Now scroll up by lines
		l.offsetLine += lines // lines is negative
		for l.offsetIdx > firstItemIdx && l.offsetLine < 0 {
			// Move to previous item
			l.offsetIdx--
			prevItem := l.getItem(l.offsetIdx)
			totalHeight := prevItem.height
			if l.gap > 0 {
				totalHeight += l.gap
			}
			l.offsetLine += totalHeight
		}

		if l.offsetLine < 0 {
			l.offsetLine = 0
		}
	}
}

// findVisibleItems finds the range of items that are visible in the viewport.
// This is used for checking if selected item is in view.
func (l *List) findVisibleItems() (startIdx, endIdx int) {
	if len(l.items) == 0 {
		return 0, 0
	}

	startIdx = l.offsetIdx
	currentIdx := startIdx
	visibleHeight := -l.offsetLine

	for currentIdx < len(l.items) {
		item := l.getItem(currentIdx)
		visibleHeight += item.height
		if l.gap > 0 {
			visibleHeight += l.gap
		}

		if visibleHeight >= l.height {
			break
		}
		currentIdx++
	}

	endIdx = currentIdx
	if endIdx >= len(l.items) {
		endIdx = len(l.items) - 1
	}

	return startIdx, endIdx
}

// Render renders the list and returns the visible lines.
func (l *List) Render() string {
	if len(l.items) == 0 {
		return ""
	}

	var lines []string
	currentIdx := l.offsetIdx
	currentOffset := l.offsetLine

	linesNeeded := l.height

	for linesNeeded > 0 && currentIdx < len(l.items) {
		item := l.renderItem(currentIdx, true)
		itemLines := strings.Split(item.content, "\n")
		itemHeight := len(itemLines)

		if currentOffset < itemHeight {
			// Add visible content lines
			lines = append(lines, itemLines[currentOffset:]...)

			// Add gap if this is not the absolute last visual element (conceptually gaps are between items)
			// But in the loop we can just add it and trim later
			if l.gap > 0 {
				for i := 0; i < l.gap; i++ {
					lines = append(lines, "")
				}
			}
		} else {
			// offsetLine starts in the gap
			gapOffset := currentOffset - itemHeight
			gapRemaining := l.gap - gapOffset
			if gapRemaining > 0 {
				for i := 0; i < gapRemaining; i++ {
					lines = append(lines, "")
				}
			}
		}

		linesNeeded = l.height - len(lines)
		currentIdx++
		currentOffset = 0 // Reset offset for subsequent items
	}

	if len(lines) > l.height {
		lines = lines[:l.height]
	}

	return strings.Join(lines, "\n")
}

// PrependItems prepends items to the list.
func (l *List) PrependItems(items ...Item) {
	l.items = append(items, l.items...)

	// Shift cache
	newCache := make(map[int]renderedItem)
	for idx, val := range l.renderedItems {
		newCache[idx+len(items)] = val
	}
	l.renderedItems = newCache

	// Keep view position relative to the content that was visible
	l.offsetIdx += len(items)

	// Update selection index if valid
	if l.selectedIdx != -1 {
		l.selectedIdx += len(items)
	}
}

// AppendItems appends items to the list.
func (l *List) AppendItems(items ...Item) {
	l.items = append(l.items, items...)
}

// Focus sets the focus state of the list.
func (l *List) Focus() {
	l.focused = true
	if l.selectedIdx < 0 || l.selectedIdx > len(l.items)-1 {
		return
	}

	// item := l.items[l.selectedIdx]
	// if focusable, ok := item.(Focusable); ok {
	// 	focusable.Focus()
	// 	l.items[l.selectedIdx] = focusable.(Item)
	// 	l.invalidateItem(l.selectedIdx)
	// }
}

// Blur removes the focus state from the list.
func (l *List) Blur() {
	l.focused = false
	if l.selectedIdx < 0 || l.selectedIdx > len(l.items)-1 {
		return
	}

	// item := l.items[l.selectedIdx]
	// if focusable, ok := item.(Focusable); ok {
	// 	focusable.Blur()
	// 	l.items[l.selectedIdx] = focusable.(Item)
	// 	l.invalidateItem(l.selectedIdx)
	// }
}

// ScrollToTop scrolls the list to the top.
func (l *List) ScrollToTop() {
	l.offsetIdx = 0
	l.offsetLine = 0
}

// ScrollToBottom scrolls the list to the bottom.
func (l *List) ScrollToBottom() {
	if len(l.items) == 0 {
		return
	}

	// Scroll to the last item
	var totalHeight int
	for i := len(l.items) - 1; i >= 0; i-- {
		item := l.getItem(i)
		totalHeight += item.height
		if l.gap > 0 && i < len(l.items)-1 {
			totalHeight += l.gap
		}
		if totalHeight >= l.height {
			l.offsetIdx = i
			l.offsetLine = totalHeight - l.height
			break
		}
	}
	if totalHeight < l.height {
		// All items fit in the viewport
		l.ScrollToTop()
	}
}

// ScrollToSelected scrolls the list to the selected item.
func (l *List) ScrollToSelected() {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return
	}

	startIdx, endIdx := l.findVisibleItems()
	if l.selectedIdx < startIdx {
		// Selected item is above the visible range
		l.offsetIdx = l.selectedIdx
		l.offsetLine = 0
	} else if l.selectedIdx > endIdx {
		// Selected item is below the visible range
		// Scroll so that the selected item is at the bottom
		var totalHeight int
		for i := l.selectedIdx; i >= 0; i-- {
			item := l.getItem(i)
			totalHeight += item.height
			if l.gap > 0 && i < l.selectedIdx {
				totalHeight += l.gap
			}
			if totalHeight >= l.height {
				l.offsetIdx = i
				l.offsetLine = totalHeight - l.height
				break
			}
		}
		if totalHeight < l.height {
			// All items fit in the viewport
			l.ScrollToTop()
		}
	}
}

// SelectedItemInView returns whether the selected item is currently in view.
func (l *List) SelectedItemInView() bool {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return false
	}
	startIdx, endIdx := l.findVisibleItems()
	return l.selectedIdx >= startIdx && l.selectedIdx <= endIdx
}

// SetSelected sets the selected item index in the list.
func (l *List) SetSelected(index int) {
	if index < 0 || index >= len(l.items) {
		l.selectedIdx = -1
	} else {
		l.selectedIdx = index
	}
}

// SelectPrev selects the previous item in the list.
func (l *List) SelectPrev() {
	if l.selectedIdx > 0 {
		l.selectedIdx--
	}
}

// SelectNext selects the next item in the list.
func (l *List) SelectNext() {
	if l.selectedIdx < len(l.items)-1 {
		l.selectedIdx++
	}
}

// SelectFirst selects the first item in the list.
func (l *List) SelectFirst() {
	if len(l.items) > 0 {
		l.selectedIdx = 0
	}
}

// SelectLast selects the last item in the list.
func (l *List) SelectLast() {
	if len(l.items) > 0 {
		l.selectedIdx = len(l.items) - 1
	}
}

// SelectFirstInView selects the first item currently in view.
func (l *List) SelectFirstInView() {
	startIdx, _ := l.findVisibleItems()
	l.selectedIdx = startIdx
}

// SelectLastInView selects the last item currently in view.
func (l *List) SelectLastInView() {
	_, endIdx := l.findVisibleItems()
	l.selectedIdx = endIdx
}

// HandleMouseDown handles mouse down events at the given line in the viewport.
// x and y are viewport-relative coordinates (0,0 = top-left of visible area).
// Returns true if the event was handled.
func (l *List) HandleMouseDown(x, y int) bool {
	if len(l.items) == 0 {
		return false
	}

	// Find which item was clicked
	itemIdx, itemY := l.findItemAtY(x, y)
	if itemIdx < 0 {
		return false
	}

	l.mouseDown = true
	l.mouseDownItem = itemIdx
	l.mouseDownX = x
	l.mouseDownY = itemY
	l.mouseDragItem = itemIdx
	l.mouseDragX = x
	l.mouseDragY = itemY

	// Select the clicked item
	l.SetSelected(itemIdx)

	return true
}

// HandleMouseUp handles mouse up events at the given line in the viewport.
// Returns true if the event was handled.
func (l *List) HandleMouseUp(x, y int) bool {
	if !l.mouseDown {
		return false
	}

	l.mouseDown = false

	return true
}

// HandleMouseDrag handles mouse drag events at the given line in the viewport.
// x and y are viewport-relative coordinates.
// Returns true if the event was handled.
func (l *List) HandleMouseDrag(x, y int) bool {
	if !l.mouseDown {
		return false
	}

	if len(l.items) == 0 {
		return false
	}

	// Find which item we're dragging over
	itemIdx, itemY := l.findItemAtY(x, y)
	if itemIdx < 0 {
		return false
	}

	l.mouseDragItem = itemIdx
	l.mouseDragX = x
	l.mouseDragY = itemY

	startItemIdx, startLine, startCol, endItemIdx, endLine, endCol := l.getHighlightRange()

	slog.Info("HandleMouseDrag", "mouseDownItem", l.mouseDownItem,
		"mouseDragItem", l.mouseDragItem,
		"startItemIdx", startItemIdx,
		"endItemIdx", endItemIdx,
		"startLine", startLine,
		"startCol", startCol,
		"endLine", endLine,
		"endCol", endCol,
	)

	// for i := startItemIdx; i <= endItemIdx; i++ {
	// 	item := l.getItem(i)
	// 	itemHi, ok := l.items[i].(Highlightable)
	// 	if ok {
	// 		if i == startItemIdx && i == endItemIdx {
	// 			// Single item selection
	// 			itemHi.SetHighlight(startLine, startCol, endLine, endCol)
	// 		} else if i == startItemIdx {
	// 			// First item - from start position to end of item
	// 			itemHi.SetHighlight(startLine, startCol, item.height-1, 9999) // 9999 = end of line
	// 		} else if i == endItemIdx {
	// 			// Last item - from start of item to end position
	// 			itemHi.SetHighlight(0, 0, endLine, endCol)
	// 		} else {
	// 			// Middle item - fully highlighted
	// 			itemHi.SetHighlight(0, 0, item.height-1, 9999)
	// 		}
	//
	// 		// Invalidate item to re-render
	// 		l.items[i] = itemHi.(Item)
	// 		l.invalidateItem(i)
	// 	}
	// }

	// Update highlight if item supports it
	// l.updateHighlight()

	return true
}

// ClearHighlight clears any active text highlighting.
func (l *List) ClearHighlight() {
	// for i, item := range l.renderedItems {
	// 	if !item.highlighted {
	// 		continue
	// 	}
	// 	if h, ok := l.items[i].(Highlightable); ok {
	// 		h.SetHighlight(-1, -1, -1, -1)
	// 		l.items[i] = h.(Item)
	// 		l.invalidateItem(i)
	// 	}
	// }
	l.mouseDownItem = -1
	l.mouseDragItem = -1
	l.lastHighlighted = make(map[int]bool)
}

// findItemAtY finds the item at the given viewport y coordinate.
// Returns the item index and the y offset within that item. It returns -1, -1
// if no item is found.
func (l *List) findItemAtY(_, y int) (itemIdx int, itemY int) {
	if y < 0 || y >= l.height {
		return -1, -1
	}

	// Walk through visible items to find which one contains this y
	currentIdx := l.offsetIdx
	currentLine := -l.offsetLine // Negative because offsetLine is how many lines are hidden

	for currentIdx < len(l.items) && currentLine < l.height {
		item := l.getItem(currentIdx)
		itemEndLine := currentLine + item.height

		// Check if y is within this item's visible range
		if y >= currentLine && y < itemEndLine {
			// Found the item, calculate itemY (offset within the item)
			itemY = y - currentLine
			return currentIdx, itemY
		}

		// Move to next item
		currentLine = itemEndLine
		if l.gap > 0 {
			currentLine += l.gap
		}
		currentIdx++
	}

	return -1, -1
}

// getHighlightRange returns the current highlight range.
func (l *List) getHighlightRange() (startItemIdx, startLine, startCol, endItemIdx, endLine, endCol int) {
	if l.mouseDownItem < 0 {
		return -1, -1, -1, -1, -1, -1
	}

	downItemIdx := l.mouseDownItem
	dragItemIdx := l.mouseDragItem

	// Determine selection direction
	draggingDown := dragItemIdx > downItemIdx ||
		(dragItemIdx == downItemIdx && l.mouseDragY > l.mouseDownY) ||
		(dragItemIdx == downItemIdx && l.mouseDragY == l.mouseDownY && l.mouseDragX >= l.mouseDownX)

	if draggingDown {
		// Normal forward selection
		startItemIdx = downItemIdx
		startLine = l.mouseDownY
		startCol = l.mouseDownX
		endItemIdx = dragItemIdx
		endLine = l.mouseDragY
		endCol = l.mouseDragX
	} else {
		// Backward selection (dragging up)
		startItemIdx = dragItemIdx
		startLine = l.mouseDragY
		startCol = l.mouseDragX
		endItemIdx = downItemIdx
		endLine = l.mouseDownY
		endCol = l.mouseDownX
	}

	slog.Info("Apply highlight",
		"startItemIdx", startItemIdx,
		"endItemIdx", endItemIdx,
		"startLine", startLine,
		"startCol", startCol,
		"endLine", endLine,
		"endCol", endCol,
	)

	return startItemIdx, startLine, startCol, endItemIdx, endLine, endCol
}

// updateHighlight updates the highlight range for highlightable items.
// Supports highlighting across multiple items and respects drag direction.
func (l *List) updateHighlight() {
	if l.mouseDownItem < 0 {
		return
	}

	// Get start and end item indices
	downItemIdx := l.mouseDownItem
	dragItemIdx := l.mouseDragItem

	// Determine selection direction
	draggingDown := dragItemIdx > downItemIdx ||
		(dragItemIdx == downItemIdx && l.mouseDragY > l.mouseDownY) ||
		(dragItemIdx == downItemIdx && l.mouseDragY == l.mouseDownY && l.mouseDragX >= l.mouseDownX)

	// Determine actual start and end based on direction
	var startItemIdx, endItemIdx int
	var startLine, startCol, endLine, endCol int

	if draggingDown {
		// Normal forward selection
		startItemIdx = downItemIdx
		endItemIdx = dragItemIdx
		startLine = l.mouseDownY
		startCol = l.mouseDownX
		endLine = l.mouseDragY
		endCol = l.mouseDragX
	} else {
		// Backward selection (dragging up)
		startItemIdx = dragItemIdx
		endItemIdx = downItemIdx
		startLine = l.mouseDragY
		startCol = l.mouseDragX
		endLine = l.mouseDownY
		endCol = l.mouseDownX
	}

	slog.Info("Update highlight", "startItemIdx", startItemIdx, "endItemIdx", endItemIdx,
		"startLine", startLine, "startCol", startCol,
		"endLine", endLine, "endCol", endCol,
		"draggingDown", draggingDown,
	)

	// Track newly highlighted items
	// newHighlighted := make(map[int]bool)

	// Clear highlights on items that are no longer in range
	// for i := range l.lastHighlighted {
	// 	if i < startItemIdx || i > endItemIdx {
	// 		if h, ok := l.items[i].(Highlightable); ok {
	// 			h.SetHighlight(-1, -1, -1, -1)
	// 			l.items[i] = h.(Item)
	// 			l.invalidateItem(i)
	// 		}
	// 	}
	// }

	// Highlight all items in range
	// for idx := startItemIdx; idx <= endItemIdx; idx++ {
	// 	item, ok := l.items[idx].(Highlightable)
	// 	if !ok {
	// 		continue
	// 	}
	//
	// 	renderedItem := l.getItem(idx)
	//
	// 	if idx == startItemIdx && idx == endItemIdx {
	// 		// Single item selection
	// 		item.SetHighlight(startLine, startCol, endLine, endCol)
	// 	} else if idx == startItemIdx {
	// 		// First item - from start position to end of item
	// 		item.SetHighlight(startLine, startCol, renderedItem.height-1, 9999) // 9999 = end of line
	// 	} else if idx == endItemIdx {
	// 		// Last item - from start of item to end position
	// 		item.SetHighlight(0, 0, endLine, endCol)
	// 	} else {
	// 		// Middle item - fully highlighted
	// 		item.SetHighlight(0, 0, renderedItem.height-1, 9999)
	// 	}
	//
	// 	l.items[idx] = item.(Item)
	//
	// 	l.invalidateItem(idx)
	// 	newHighlighted[idx] = true
	// }
	//
	// l.lastHighlighted = newHighlighted
}

// countLines counts the number of lines in a string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
