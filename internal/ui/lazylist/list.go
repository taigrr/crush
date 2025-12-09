package lazylist

import (
	"log/slog"
	"strings"
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
	if idx < 0 || idx >= len(l.items) {
		return renderedItem{}
	}

	if item, ok := l.renderedItems[idx]; ok {
		return item
	}

	item := l.items[idx]
	rendered := item.Render(l.width)
	height := countLines(rendered)
	// slog.Info("Rendered item", "idx", idx, "height", height)

	ri := renderedItem{
		content: rendered,
		height:  height,
	}

	l.renderedItems[idx] = ri

	return ri
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
			if totalLines >= l.height {
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
			l.offsetLine = item.height - 1
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

	slog.Info("Render", "offsetIdx", l.offsetIdx, "offsetLine", l.offsetLine, "width", l.width, "height", l.height)

	var lines []string
	currentIdx := l.offsetIdx
	currentOffset := l.offsetLine

	linesNeeded := l.height

	for linesNeeded > 0 && currentIdx < len(l.items) {
		item := l.getItem(currentIdx)
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
}

// Blur removes the focus state from the list.
func (l *List) Blur() {
	l.focused = false
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
	// TODO: Implement me
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
func (l *List) HandleMouseDown(x, y int) {
}

// HandleMouseUp handles mouse up events at the given line in the viewport.
func (l *List) HandleMouseUp(x, y int) {
}

// HandleMouseDrag handles mouse drag events at the given line in the viewport.
func (l *List) HandleMouseDrag(x, y int) {
}

// countLines counts the number of lines in a string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
