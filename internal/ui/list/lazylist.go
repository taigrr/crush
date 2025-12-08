package list

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/screen"
)

// LazyList is a virtual scrolling list that only renders visible items.
// It uses height estimates to avoid expensive renders during initial layout.
type LazyList struct {
	// Configuration
	width, height int

	// Data
	items []Item

	// Focus & Selection
	focused     bool
	selectedIdx int // Currently selected item index (-1 if none)

	// Item positioning - tracks measured and estimated positions
	itemHeights []itemHeight
	totalHeight int // Sum of all item heights (measured or estimated)

	// Viewport state
	offset int // Scroll offset in lines from top

	// Rendered items cache - only visible items are rendered
	renderedCache map[int]*renderedItemCache

	// Virtual scrolling configuration
	defaultEstimate int // Default height estimate for unmeasured items
	overscan        int // Number of items to render outside viewport for smooth scrolling

	// Dirty tracking
	needsLayout   bool
	dirtyItems    map[int]bool
	dirtyViewport bool // True if we need to re-render viewport

	// Mouse state
	mouseDown     bool
	mouseDownItem int
	mouseDownX    int
	mouseDownY    int
	mouseDragItem int
	mouseDragX    int
	mouseDragY    int
}

// itemHeight tracks the height of an item - either measured or estimated.
type itemHeight struct {
	height   int
	measured bool // true if height is actual measurement, false if estimate
}

// renderedItemCache stores a rendered item's buffer.
type renderedItemCache struct {
	buffer *uv.ScreenBuffer
	height int // Actual measured height after rendering
}

// NewLazyList creates a new lazy-rendering list.
func NewLazyList(items ...Item) *LazyList {
	l := &LazyList{
		items:           items,
		itemHeights:     make([]itemHeight, len(items)),
		renderedCache:   make(map[int]*renderedItemCache),
		dirtyItems:      make(map[int]bool),
		selectedIdx:     -1,
		mouseDownItem:   -1,
		mouseDragItem:   -1,
		defaultEstimate: 10, // Conservative estimate: 5 lines per item
		overscan:        5,  // Render 3 items above/below viewport
		needsLayout:     true,
		dirtyViewport:   true,
	}

	// Initialize all items with estimated heights
	for i := range l.items {
		l.itemHeights[i] = itemHeight{
			height:   l.defaultEstimate,
			measured: false,
		}
	}
	l.calculateTotalHeight()

	return l
}

// calculateTotalHeight sums all item heights (measured or estimated).
func (l *LazyList) calculateTotalHeight() {
	l.totalHeight = 0
	for _, h := range l.itemHeights {
		l.totalHeight += h.height
	}
}

// getItemPosition returns the Y position where an item starts.
func (l *LazyList) getItemPosition(idx int) int {
	pos := 0
	for i := 0; i < idx && i < len(l.itemHeights); i++ {
		pos += l.itemHeights[i].height
	}
	return pos
}

// findVisibleItems returns the range of items that are visible or near the viewport.
func (l *LazyList) findVisibleItems() (firstIdx, lastIdx int) {
	if len(l.items) == 0 {
		return 0, 0
	}

	viewportStart := l.offset
	viewportEnd := l.offset + l.height

	// Find first visible item
	firstIdx = -1
	pos := 0
	for i := 0; i < len(l.items); i++ {
		itemEnd := pos + l.itemHeights[i].height
		if itemEnd > viewportStart {
			firstIdx = i
			break
		}
		pos = itemEnd
	}

	// Apply overscan above
	firstIdx = max(0, firstIdx-l.overscan)

	// Find last visible item
	lastIdx = firstIdx
	pos = l.getItemPosition(firstIdx)
	for i := firstIdx; i < len(l.items); i++ {
		if pos >= viewportEnd {
			break
		}
		pos += l.itemHeights[i].height
		lastIdx = i
	}

	// Apply overscan below
	lastIdx = min(len(l.items)-1, lastIdx+l.overscan)

	return firstIdx, lastIdx
}

// renderItem renders a single item and caches it.
// Returns the actual measured height.
func (l *LazyList) renderItem(idx int) int {
	if idx < 0 || idx >= len(l.items) {
		return 0
	}

	item := l.items[idx]

	// Measure actual height
	actualHeight := item.Height(l.width)

	// Create buffer and render
	buf := uv.NewScreenBuffer(l.width, actualHeight)
	area := uv.Rect(0, 0, l.width, actualHeight)
	item.Draw(&buf, area)

	// Cache rendered item
	l.renderedCache[idx] = &renderedItemCache{
		buffer: &buf,
		height: actualHeight,
	}

	// Update height if it was estimated or changed
	if !l.itemHeights[idx].measured || l.itemHeights[idx].height != actualHeight {
		oldHeight := l.itemHeights[idx].height
		l.itemHeights[idx] = itemHeight{
			height:   actualHeight,
			measured: true,
		}

		// Adjust total height
		l.totalHeight += actualHeight - oldHeight
	}

	return actualHeight
}

// Draw implements uv.Drawable.
func (l *LazyList) Draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return
	}

	widthChanged := l.width != area.Dx()
	heightChanged := l.height != area.Dy()

	l.width = area.Dx()
	l.height = area.Dy()

	// Width changes invalidate all cached renders
	if widthChanged {
		l.renderedCache = make(map[int]*renderedItemCache)
		// Mark all heights as needing remeasurement
		for i := range l.itemHeights {
			l.itemHeights[i].measured = false
			l.itemHeights[i].height = l.defaultEstimate
		}
		l.calculateTotalHeight()
		l.needsLayout = true
		l.dirtyViewport = true
	}

	if heightChanged {
		l.clampOffset()
		l.dirtyViewport = true
	}

	if len(l.items) == 0 {
		screen.ClearArea(scr, area)
		return
	}

	// Find visible items based on current estimates
	firstIdx, lastIdx := l.findVisibleItems()

	// Track the first visible item's position to maintain stability
	// Only stabilize if we're not at the top boundary
	stabilizeIdx := -1
	stabilizeY := 0
	if l.offset > 0 {
		for i := firstIdx; i <= lastIdx; i++ {
			itemPos := l.getItemPosition(i)
			if itemPos >= l.offset {
				stabilizeIdx = i
				stabilizeY = itemPos
				break
			}
		}
	}

	// Track if any heights changed during rendering
	heightsChanged := false

	// Render visible items that aren't cached (measurement pass)
	for i := firstIdx; i <= lastIdx; i++ {
		if _, cached := l.renderedCache[i]; !cached {
			oldHeight := l.itemHeights[i].height
			l.renderItem(i)
			if l.itemHeights[i].height != oldHeight {
				heightsChanged = true
			}
		} else if l.dirtyItems[i] {
			// Re-render dirty items
			oldHeight := l.itemHeights[i].height
			l.renderItem(i)
			delete(l.dirtyItems, i)
			if l.itemHeights[i].height != oldHeight {
				heightsChanged = true
			}
		}
	}

	// If heights changed, adjust offset to keep stabilization point stable
	if heightsChanged && stabilizeIdx >= 0 {
		newStabilizeY := l.getItemPosition(stabilizeIdx)
		offsetDelta := newStabilizeY - stabilizeY

		// Adjust offset to maintain visual stability
		l.offset += offsetDelta
		l.clampOffset()

		// Re-find visible items with adjusted positions
		firstIdx, lastIdx = l.findVisibleItems()

		// Render any newly visible items after position adjustments
		for i := firstIdx; i <= lastIdx; i++ {
			if _, cached := l.renderedCache[i]; !cached {
				l.renderItem(i)
			}
		}
	}

	// Clear old cache entries outside visible range
	if len(l.renderedCache) > (lastIdx-firstIdx+1)*2 {
		l.pruneCache(firstIdx, lastIdx)
	}

	// Composite visible items into viewport with stable positions
	l.drawViewport(scr, area, firstIdx, lastIdx)

	l.dirtyViewport = false
	l.needsLayout = false
}

// drawViewport composites visible items into the screen.
func (l *LazyList) drawViewport(scr uv.Screen, area uv.Rectangle, firstIdx, lastIdx int) {
	screen.ClearArea(scr, area)

	itemStartY := l.getItemPosition(firstIdx)

	for i := firstIdx; i <= lastIdx; i++ {
		cached, ok := l.renderedCache[i]
		if !ok {
			continue
		}

		// Calculate where this item appears in viewport
		itemY := itemStartY - l.offset
		itemHeight := cached.height

		// Skip if entirely above viewport
		if itemY+itemHeight < 0 {
			itemStartY += itemHeight
			continue
		}

		// Stop if entirely below viewport
		if itemY >= l.height {
			break
		}

		// Calculate visible portion of item
		srcStartY := 0
		dstStartY := itemY

		if itemY < 0 {
			// Item starts above viewport
			srcStartY = -itemY
			dstStartY = 0
		}

		srcEndY := srcStartY + (l.height - dstStartY)
		if srcEndY > itemHeight {
			srcEndY = itemHeight
		}

		// Copy visible lines from item buffer to screen
		buf := cached.buffer.Buffer
		destY := area.Min.Y + dstStartY

		for srcY := srcStartY; srcY < srcEndY && destY < area.Max.Y; srcY++ {
			if srcY >= buf.Height() {
				break
			}

			line := buf.Line(srcY)
			destX := area.Min.X

			for x := 0; x < len(line) && x < area.Dx() && destX < area.Max.X; x++ {
				cell := line.At(x)
				scr.SetCell(destX, destY, cell)
				destX++
			}
			destY++
		}

		itemStartY += itemHeight
	}
}

// pruneCache removes cached items outside the visible range.
func (l *LazyList) pruneCache(firstIdx, lastIdx int) {
	keepStart := max(0, firstIdx-l.overscan*2)
	keepEnd := min(len(l.items)-1, lastIdx+l.overscan*2)

	for idx := range l.renderedCache {
		if idx < keepStart || idx > keepEnd {
			delete(l.renderedCache, idx)
		}
	}
}

// clampOffset ensures scroll offset stays within valid bounds.
func (l *LazyList) clampOffset() {
	maxOffset := l.totalHeight - l.height
	if maxOffset < 0 {
		maxOffset = 0
	}

	if l.offset > maxOffset {
		l.offset = maxOffset
	}
	if l.offset < 0 {
		l.offset = 0
	}
}

// SetItems replaces all items in the list.
func (l *LazyList) SetItems(items []Item) {
	l.items = items
	l.itemHeights = make([]itemHeight, len(items))
	l.renderedCache = make(map[int]*renderedItemCache)
	l.dirtyItems = make(map[int]bool)

	// Initialize with estimates
	for i := range l.items {
		l.itemHeights[i] = itemHeight{
			height:   l.defaultEstimate,
			measured: false,
		}
	}
	l.calculateTotalHeight()
	l.needsLayout = true
	l.dirtyViewport = true
}

// AppendItem adds an item to the end of the list.
func (l *LazyList) AppendItem(item Item) {
	l.items = append(l.items, item)
	l.itemHeights = append(l.itemHeights, itemHeight{
		height:   l.defaultEstimate,
		measured: false,
	})
	l.totalHeight += l.defaultEstimate
	l.dirtyViewport = true
}

// PrependItem adds an item to the beginning of the list.
func (l *LazyList) PrependItem(item Item) {
	l.items = append([]Item{item}, l.items...)
	l.itemHeights = append([]itemHeight{{
		height:   l.defaultEstimate,
		measured: false,
	}}, l.itemHeights...)

	// Shift cache indices
	newCache := make(map[int]*renderedItemCache)
	for idx, cached := range l.renderedCache {
		newCache[idx+1] = cached
	}
	l.renderedCache = newCache

	l.totalHeight += l.defaultEstimate
	l.offset += l.defaultEstimate // Maintain scroll position
	l.dirtyViewport = true
}

// UpdateItem replaces an item at the given index.
func (l *LazyList) UpdateItem(idx int, item Item) {
	if idx < 0 || idx >= len(l.items) {
		return
	}

	l.items[idx] = item
	delete(l.renderedCache, idx)
	l.dirtyItems[idx] = true
	// Keep height estimate - will remeasure on next render
	l.dirtyViewport = true
}

// ScrollBy scrolls by the given number of lines.
func (l *LazyList) ScrollBy(delta int) {
	l.offset += delta
	l.clampOffset()
	l.dirtyViewport = true
}

// ScrollToBottom scrolls to the end of the list.
func (l *LazyList) ScrollToBottom() {
	l.offset = l.totalHeight - l.height
	l.clampOffset()
	l.dirtyViewport = true
}

// ScrollToTop scrolls to the beginning of the list.
func (l *LazyList) ScrollToTop() {
	l.offset = 0
	l.dirtyViewport = true
}

// Len returns the number of items in the list.
func (l *LazyList) Len() int {
	return len(l.items)
}

// Focus sets the list as focused.
func (l *LazyList) Focus() {
	l.focused = true
	l.focusSelectedItem()
	l.dirtyViewport = true
}

// Blur removes focus from the list.
func (l *LazyList) Blur() {
	l.focused = false
	l.blurSelectedItem()
	l.dirtyViewport = true
}

// focusSelectedItem focuses the currently selected item if it's focusable.
func (l *LazyList) focusSelectedItem() {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return
	}

	item := l.items[l.selectedIdx]
	if f, ok := item.(Focusable); ok {
		f.Focus()
		delete(l.renderedCache, l.selectedIdx)
		l.dirtyItems[l.selectedIdx] = true
	}
}

// blurSelectedItem blurs the currently selected item if it's focusable.
func (l *LazyList) blurSelectedItem() {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return
	}

	item := l.items[l.selectedIdx]
	if f, ok := item.(Focusable); ok {
		f.Blur()
		delete(l.renderedCache, l.selectedIdx)
		l.dirtyItems[l.selectedIdx] = true
	}
}

// IsFocused returns whether the list is focused.
func (l *LazyList) IsFocused() bool {
	return l.focused
}

// Width returns the current viewport width.
func (l *LazyList) Width() int {
	return l.width
}

// Height returns the current viewport height.
func (l *LazyList) Height() int {
	return l.height
}

// SetSize sets the viewport size explicitly.
// This is useful when you want to pre-configure the list size before drawing.
func (l *LazyList) SetSize(width, height int) {
	widthChanged := l.width != width
	heightChanged := l.height != height

	l.width = width
	l.height = height

	// Width changes invalidate all cached renders
	if widthChanged && width > 0 {
		l.renderedCache = make(map[int]*renderedItemCache)
		// Mark all heights as needing remeasurement
		for i := range l.itemHeights {
			l.itemHeights[i].measured = false
			l.itemHeights[i].height = l.defaultEstimate
		}
		l.calculateTotalHeight()
		l.needsLayout = true
		l.dirtyViewport = true
	}

	if heightChanged && height > 0 {
		l.clampOffset()
		l.dirtyViewport = true
	}

	// After cache invalidation, scroll to selected item or bottom
	if widthChanged || heightChanged {
		if l.selectedIdx >= 0 && l.selectedIdx < len(l.items) {
			// Scroll to selected item
			l.ScrollToSelected()
		} else if len(l.items) > 0 {
			// No selection - scroll to bottom
			l.ScrollToBottom()
		}
	}
}

// Selection methods

// Selected returns the currently selected item index (-1 if none).
func (l *LazyList) Selected() int {
	return l.selectedIdx
}

// SetSelected sets the selected item by index.
func (l *LazyList) SetSelected(idx int) {
	if idx < -1 || idx >= len(l.items) {
		return
	}

	if l.selectedIdx != idx {
		prevIdx := l.selectedIdx
		l.selectedIdx = idx
		l.dirtyViewport = true

		// Update focus states if list is focused.
		if l.focused {
			// Blur previously selected item.
			if prevIdx >= 0 && prevIdx < len(l.items) {
				if f, ok := l.items[prevIdx].(Focusable); ok {
					f.Blur()
					delete(l.renderedCache, prevIdx)
					l.dirtyItems[prevIdx] = true
				}
			}

			// Focus newly selected item.
			if idx >= 0 && idx < len(l.items) {
				if f, ok := l.items[idx].(Focusable); ok {
					f.Focus()
					delete(l.renderedCache, idx)
					l.dirtyItems[idx] = true
				}
			}
		}
	}
}

// SelectPrev selects the previous item.
func (l *LazyList) SelectPrev() {
	if len(l.items) == 0 {
		return
	}

	if l.selectedIdx <= 0 {
		l.selectedIdx = 0
	} else {
		l.selectedIdx--
	}

	l.dirtyViewport = true
}

// SelectNext selects the next item.
func (l *LazyList) SelectNext() {
	if len(l.items) == 0 {
		return
	}

	if l.selectedIdx < 0 {
		l.selectedIdx = 0
	} else if l.selectedIdx < len(l.items)-1 {
		l.selectedIdx++
	}

	l.dirtyViewport = true
}

// SelectFirst selects the first item.
func (l *LazyList) SelectFirst() {
	if len(l.items) > 0 {
		l.selectedIdx = 0
		l.dirtyViewport = true
	}
}

// SelectLast selects the last item.
func (l *LazyList) SelectLast() {
	if len(l.items) > 0 {
		l.selectedIdx = len(l.items) - 1
		l.dirtyViewport = true
	}
}

// SelectFirstInView selects the first visible item in the viewport.
func (l *LazyList) SelectFirstInView() {
	if len(l.items) == 0 {
		return
	}

	firstIdx, _ := l.findVisibleItems()
	l.selectedIdx = firstIdx
	l.dirtyViewport = true
}

// SelectLastInView selects the last visible item in the viewport.
func (l *LazyList) SelectLastInView() {
	if len(l.items) == 0 {
		return
	}

	_, lastIdx := l.findVisibleItems()
	l.selectedIdx = lastIdx
	l.dirtyViewport = true
}

// SelectedItemInView returns whether the selected item is visible in the viewport.
func (l *LazyList) SelectedItemInView() bool {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return false
	}

	firstIdx, lastIdx := l.findVisibleItems()
	return l.selectedIdx >= firstIdx && l.selectedIdx <= lastIdx
}

// ScrollToSelected scrolls the viewport to ensure the selected item is visible.
func (l *LazyList) ScrollToSelected() {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return
	}

	// Get selected item position
	itemY := l.getItemPosition(l.selectedIdx)
	itemHeight := l.itemHeights[l.selectedIdx].height

	// Check if item is above viewport
	if itemY < l.offset {
		l.offset = itemY
		l.dirtyViewport = true
		return
	}

	// Check if item is below viewport
	itemBottom := itemY + itemHeight
	viewportBottom := l.offset + l.height

	if itemBottom > viewportBottom {
		// Scroll so item bottom is at viewport bottom
		l.offset = itemBottom - l.height
		l.clampOffset()
		l.dirtyViewport = true
	}
}

// Mouse interaction methods

// HandleMouseDown handles mouse button down events.
// Returns true if the event was handled.
func (l *LazyList) HandleMouseDown(x, y int) bool {
	if x < 0 || y < 0 || x >= l.width || y >= l.height {
		return false
	}

	// Find which item was clicked
	clickY := l.offset + y
	itemIdx := l.findItemAtY(clickY)

	if itemIdx < 0 {
		return false
	}

	// Calculate item-relative Y position.
	itemY := clickY - l.getItemPosition(itemIdx)

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

// HandleMouseDrag handles mouse drag events.
func (l *LazyList) HandleMouseDrag(x, y int) {
	if !l.mouseDown {
		return
	}

	// Find item under cursor
	if y >= 0 && y < l.height {
		dragY := l.offset + y
		itemIdx := l.findItemAtY(dragY)
		if itemIdx >= 0 {
			l.mouseDragItem = itemIdx
			// Calculate item-relative Y position.
			l.mouseDragY = dragY - l.getItemPosition(itemIdx)
			l.mouseDragX = x
		}
	}

	// Update highlight if item supports it.
	l.updateHighlight()
}

// HandleMouseUp handles mouse button up events.
func (l *LazyList) HandleMouseUp(x, y int) {
	if !l.mouseDown {
		return
	}

	l.mouseDown = false

	// Final highlight update.
	l.updateHighlight()
}

// findItemAtY finds the item index at the given Y coordinate (in content space, not viewport).
func (l *LazyList) findItemAtY(y int) int {
	if y < 0 || len(l.items) == 0 {
		return -1
	}

	pos := 0
	for i := 0; i < len(l.items); i++ {
		itemHeight := l.itemHeights[i].height
		if y >= pos && y < pos+itemHeight {
			return i
		}
		pos += itemHeight
	}

	return -1
}

// updateHighlight updates the highlight range for highlightable items.
// Supports highlighting within a single item and respects drag direction.
func (l *LazyList) updateHighlight() {
	if l.mouseDownItem < 0 {
		return
	}

	// Get start and end item indices.
	downItemIdx := l.mouseDownItem
	dragItemIdx := l.mouseDragItem

	// Determine selection direction.
	draggingDown := dragItemIdx > downItemIdx ||
		(dragItemIdx == downItemIdx && l.mouseDragY > l.mouseDownY) ||
		(dragItemIdx == downItemIdx && l.mouseDragY == l.mouseDownY && l.mouseDragX >= l.mouseDownX)

	// Determine actual start and end based on direction.
	var startItemIdx, endItemIdx int
	var startLine, startCol, endLine, endCol int

	if draggingDown {
		// Normal forward selection.
		startItemIdx = downItemIdx
		endItemIdx = dragItemIdx
		startLine = l.mouseDownY
		startCol = l.mouseDownX
		endLine = l.mouseDragY
		endCol = l.mouseDragX
	} else {
		// Backward selection (dragging up).
		startItemIdx = dragItemIdx
		endItemIdx = downItemIdx
		startLine = l.mouseDragY
		startCol = l.mouseDragX
		endLine = l.mouseDownY
		endCol = l.mouseDownX
	}

	// Clear all highlights first.
	for i, item := range l.items {
		if h, ok := item.(Highlightable); ok {
			h.SetHighlight(-1, -1, -1, -1)
			delete(l.renderedCache, i)
			l.dirtyItems[i] = true
		}
	}

	// Highlight all items in range.
	for idx := startItemIdx; idx <= endItemIdx; idx++ {
		item, ok := l.items[idx].(Highlightable)
		if !ok {
			continue
		}

		if idx == startItemIdx && idx == endItemIdx {
			// Single item selection.
			item.SetHighlight(startLine, startCol, endLine, endCol)
		} else if idx == startItemIdx {
			// First item - from start position to end of item.
			itemHeight := l.itemHeights[idx].height
			item.SetHighlight(startLine, startCol, itemHeight-1, 9999) // 9999 = end of line
		} else if idx == endItemIdx {
			// Last item - from start of item to end position.
			item.SetHighlight(0, 0, endLine, endCol)
		} else {
			// Middle item - fully highlighted.
			itemHeight := l.itemHeights[idx].height
			item.SetHighlight(0, 0, itemHeight-1, 9999)
		}

		delete(l.renderedCache, idx)
		l.dirtyItems[idx] = true
	}
}

// ClearHighlight clears any active text highlighting.
func (l *LazyList) ClearHighlight() {
	for i, item := range l.items {
		if h, ok := item.(Highlightable); ok {
			h.SetHighlight(-1, -1, -1, -1)
			delete(l.renderedCache, i)
			l.dirtyItems[i] = true
		}
	}
	l.mouseDownItem = -1
	l.mouseDragItem = -1
}

// GetHighlightedText returns the plain text content of all highlighted regions
// across items, without any styling. Returns empty string if no highlights exist.
func (l *LazyList) GetHighlightedText() string {
	var result strings.Builder

	// Iterate through items to find highlighted ones.
	for i, item := range l.items {
		h, ok := item.(Highlightable)
		if !ok {
			continue
		}

		startLine, startCol, endLine, endCol := h.GetHighlight()
		if startLine < 0 {
			continue
		}

		// Ensure item is rendered so we can access its buffer.
		if _, ok := l.renderedCache[i]; !ok {
			l.renderItem(i)
		}

		cached := l.renderedCache[i]
		if cached == nil || cached.buffer == nil {
			continue
		}

		buf := cached.buffer
		itemHeight := cached.height

		// Extract text from highlighted region in item buffer.
		for y := startLine; y <= endLine && y < itemHeight; y++ {
			if y >= buf.Height() {
				break
			}

			line := buf.Line(y)

			// Determine column range for this line.
			colStart := 0
			if y == startLine {
				colStart = startCol
			}

			colEnd := len(line)
			if y == endLine {
				colEnd = min(endCol, len(line))
			}

			// Track last non-empty position to trim trailing spaces.
			lastContentX := -1
			for x := colStart; x < colEnd && x < len(line); x++ {
				cell := line.At(x)
				if cell == nil || cell.IsZero() {
					continue
				}
				if cell.Content != "" && cell.Content != " " {
					lastContentX = x
				}
			}

			// Extract text from cells, up to last content.
			endX := colEnd
			if lastContentX >= 0 {
				endX = lastContentX + 1
			}

			for x := colStart; x < endX && x < len(line); x++ {
				cell := line.At(x)
				if cell != nil && !cell.IsZero() {
					result.WriteString(cell.Content)
				}
			}

			// Add newline if not the last line.
			if y < endLine {
				result.WriteString("\n")
			}
		}

		// Add newline between items if this isn't the last highlighted item.
		if i < len(l.items)-1 {
			nextHasHighlight := false
			for j := i + 1; j < len(l.items); j++ {
				if h, ok := l.items[j].(Highlightable); ok {
					s, _, _, _ := h.GetHighlight()
					if s >= 0 {
						nextHasHighlight = true
						break
					}
				}
			}
			if nextHasHighlight {
				result.WriteString("\n")
			}
		}
	}

	return result.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
