package list

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/exp/ordered"
)

const maxGapSize = 100

var newlineBuffer = strings.Repeat("\n", maxGapSize)

// SimpleList is a string-based list with virtual scrolling behavior.
// Based on exp/list but simplified for our needs.
type SimpleList struct {
	// Viewport dimensions.
	width, height int

	// Scroll offset (in lines from top).
	offset int

	// Items.
	items   []Item
	itemIDs map[string]int // ID -> index mapping

	// Rendered content (all items stacked).
	rendered       string
	renderedHeight int   // Total height of rendered content in lines
	lineOffsets    []int // Byte offsets for each line (for fast slicing)

	// Rendered item metadata.
	renderedItems map[string]renderedItem

	// Selection.
	selectedIdx int
	focused     bool

	// Focus tracking.
	prevSelectedIdx int

	// Mouse/highlight state.
	mouseDown          bool
	mouseDownItem      int
	mouseDownX         int
	mouseDownY         int // viewport-relative Y
	mouseDragItem      int
	mouseDragX         int
	mouseDragY         int // viewport-relative Y
	selectionStartLine int
	selectionStartCol  int
	selectionEndLine   int
	selectionEndCol    int

	// Configuration.
	gap int // Gap between items in lines
}

type renderedItem struct {
	view   string
	height int
	start  int // Start line in rendered content
	end    int // End line in rendered content
}

// NewSimpleList creates a new simple list.
func NewSimpleList(items ...Item) *SimpleList {
	l := &SimpleList{
		items:              items,
		itemIDs:            make(map[string]int, len(items)),
		renderedItems:      make(map[string]renderedItem),
		selectedIdx:        -1,
		prevSelectedIdx:    -1,
		gap:                0,
		selectionStartLine: -1,
		selectionStartCol:  -1,
		selectionEndLine:   -1,
		selectionEndCol:    -1,
	}

	// Build ID map.
	for i, item := range items {
		if idItem, ok := item.(interface{ ID() string }); ok {
			l.itemIDs[idItem.ID()] = i
		}
	}

	return l
}

// Init initializes the list (Bubbletea lifecycle).
func (l *SimpleList) Init() tea.Cmd {
	return l.render()
}

// Update handles messages (Bubbletea lifecycle).
func (l *SimpleList) Update(msg tea.Msg) (*SimpleList, tea.Cmd) {
	return l, nil
}

// View returns the visible viewport (Bubbletea lifecycle).
func (l *SimpleList) View() string {
	if l.height <= 0 || l.width <= 0 {
		return ""
	}

	start, end := l.viewPosition()
	viewStart := max(0, start)
	viewEnd := end

	if viewStart > viewEnd {
		return ""
	}

	view := l.getLines(viewStart, viewEnd)

	// Apply width/height constraints.
	view = lipgloss.NewStyle().
		Height(l.height).
		Width(l.width).
		Render(view)

	// Apply highlighting if active.
	if l.hasSelection() {
		return l.renderSelection(view)
	}

	return view
}

// viewPosition returns the start and end line indices for the viewport.
func (l *SimpleList) viewPosition() (int, int) {
	start := max(0, l.offset)
	end := min(l.offset+l.height-1, l.renderedHeight-1)
	start = min(start, end)
	return start, end
}

// getLines returns lines [start, end] from rendered content.
func (l *SimpleList) getLines(start, end int) string {
	if len(l.lineOffsets) == 0 || start >= len(l.lineOffsets) {
		return ""
	}

	if end >= len(l.lineOffsets) {
		end = len(l.lineOffsets) - 1
	}
	if start > end {
		return ""
	}

	startOffset := l.lineOffsets[start]
	var endOffset int
	if end+1 < len(l.lineOffsets) {
		endOffset = l.lineOffsets[end+1] - 1 // Exclude newline
	} else {
		endOffset = len(l.rendered)
	}

	if startOffset >= len(l.rendered) {
		return ""
	}
	endOffset = min(endOffset, len(l.rendered))

	return l.rendered[startOffset:endOffset]
}

// render rebuilds the rendered content from all items.
func (l *SimpleList) render() tea.Cmd {
	if l.width <= 0 || l.height <= 0 || len(l.items) == 0 {
		return nil
	}

	// Set default selection if none.
	if l.selectedIdx < 0 && len(l.items) > 0 {
		l.selectedIdx = 0
	}

	// Handle focus changes.
	var focusCmd tea.Cmd
	if l.focused {
		focusCmd = l.focusSelectedItem()
	} else {
		focusCmd = l.blurSelectedItem()
	}

	// Render all items.
	var b strings.Builder
	currentLine := 0

	for i, item := range l.items {
		// Render item.
		view := l.renderItem(item)
		height := lipgloss.Height(view)

		// Store metadata.
		rItem := renderedItem{
			view:   view,
			height: height,
			start:  currentLine,
			end:    currentLine + height - 1,
		}

		if idItem, ok := item.(interface{ ID() string }); ok {
			l.renderedItems[idItem.ID()] = rItem
		}

		// Append to rendered content.
		b.WriteString(view)

		// Add gap after item (except last).
		gap := l.gap
		if i == len(l.items)-1 {
			gap = 0
		}

		if gap > 0 {
			if gap <= maxGapSize {
				b.WriteString(newlineBuffer[:gap])
			} else {
				b.WriteString(strings.Repeat("\n", gap))
			}
		}

		currentLine += height + gap
	}

	l.setRendered(b.String())

	// Scroll to selected item.
	if l.focused && l.selectedIdx >= 0 {
		l.scrollToSelection()
	}

	return focusCmd
}

// renderItem renders a single item.
func (l *SimpleList) renderItem(item Item) string {
	// Create a buffer for the item.
	buf := uv.NewScreenBuffer(l.width, 1000) // Max height
	area := uv.Rect(0, 0, l.width, 1000)
	item.Draw(&buf, area)

	// Find actual height.
	height := l.measureBufferHeight(&buf)
	if height == 0 {
		height = 1
	}

	// Render to string.
	return buf.Render()
}

// measureBufferHeight finds the actual content height in a buffer.
func (l *SimpleList) measureBufferHeight(buf *uv.ScreenBuffer) int {
	height := buf.Height()

	// Scan from bottom up to find last non-empty line.
	for y := height - 1; y >= 0; y-- {
		line := buf.Line(y)
		if l.lineHasContent(line) {
			return y + 1
		}
	}

	return 0
}

// lineHasContent checks if a line has any non-empty cells.
func (l *SimpleList) lineHasContent(line uv.Line) bool {
	for x := 0; x < len(line); x++ {
		cell := line.At(x)
		if cell != nil && !cell.IsZero() && cell.Content != "" && cell.Content != " " {
			return true
		}
	}
	return false
}

// setRendered updates the rendered content and caches line offsets.
func (l *SimpleList) setRendered(rendered string) {
	l.rendered = rendered
	l.renderedHeight = lipgloss.Height(rendered)

	// Build line offset cache.
	if len(rendered) > 0 {
		l.lineOffsets = make([]int, 0, l.renderedHeight)
		l.lineOffsets = append(l.lineOffsets, 0)

		offset := 0
		for {
			idx := strings.IndexByte(rendered[offset:], '\n')
			if idx == -1 {
				break
			}
			offset += idx + 1
			l.lineOffsets = append(l.lineOffsets, offset)
		}
	} else {
		l.lineOffsets = nil
	}
}

// scrollToSelection scrolls to make the selected item visible.
func (l *SimpleList) scrollToSelection() {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return
	}

	// Get selected item metadata.
	var rItem *renderedItem
	if idItem, ok := l.items[l.selectedIdx].(interface{ ID() string }); ok {
		if ri, ok := l.renderedItems[idItem.ID()]; ok {
			rItem = &ri
		}
	}

	if rItem == nil {
		return
	}

	start, end := l.viewPosition()

	// Already visible.
	if rItem.start >= start && rItem.end <= end {
		return
	}

	// Item is above viewport - scroll up.
	if rItem.start < start {
		l.offset = rItem.start
		return
	}

	// Item is below viewport - scroll down.
	if rItem.end > end {
		l.offset = max(0, rItem.end-l.height+1)
	}
}

// Focus/blur management.

func (l *SimpleList) focusSelectedItem() tea.Cmd {
	if l.selectedIdx < 0 || !l.focused {
		return nil
	}

	var cmds []tea.Cmd

	// Blur previous.
	if l.prevSelectedIdx >= 0 && l.prevSelectedIdx != l.selectedIdx && l.prevSelectedIdx < len(l.items) {
		if f, ok := l.items[l.prevSelectedIdx].(Focusable); ok && f.IsFocused() {
			f.Blur()
		}
	}

	// Focus current.
	if l.selectedIdx >= 0 && l.selectedIdx < len(l.items) {
		if f, ok := l.items[l.selectedIdx].(Focusable); ok && !f.IsFocused() {
			f.Focus()
		}
	}

	l.prevSelectedIdx = l.selectedIdx
	return tea.Batch(cmds...)
}

func (l *SimpleList) blurSelectedItem() tea.Cmd {
	if l.selectedIdx < 0 || l.focused {
		return nil
	}

	if l.selectedIdx >= 0 && l.selectedIdx < len(l.items) {
		if f, ok := l.items[l.selectedIdx].(Focusable); ok && f.IsFocused() {
			f.Blur()
		}
	}

	return nil
}

// Public API.

// SetSize sets the viewport dimensions.
func (l *SimpleList) SetSize(width, height int) tea.Cmd {
	oldWidth := l.width
	l.width = width
	l.height = height

	if oldWidth != width {
		// Width changed - need to re-render.
		return l.render()
	}

	return nil
}

// Width returns the viewport width.
func (l *SimpleList) Width() int {
	return l.width
}

// Height returns the viewport height.
func (l *SimpleList) Height() int {
	return l.height
}

// GetSize returns the viewport dimensions.
func (l *SimpleList) GetSize() (int, int) {
	return l.width, l.height
}

// Items returns all items.
func (l *SimpleList) Items() []Item {
	return l.items
}

// Len returns the number of items.
func (l *SimpleList) Len() int {
	return len(l.items)
}

// SetItems replaces all items.
func (l *SimpleList) SetItems(items []Item) tea.Cmd {
	l.items = items
	l.itemIDs = make(map[string]int, len(items))
	l.renderedItems = make(map[string]renderedItem)
	l.selectedIdx = -1
	l.prevSelectedIdx = -1
	l.offset = 0

	// Build ID map.
	for i, item := range items {
		if idItem, ok := item.(interface{ ID() string }); ok {
			l.itemIDs[idItem.ID()] = i
		}
	}

	return l.render()
}

// AppendItem adds an item to the end.
func (l *SimpleList) AppendItem(item Item) tea.Cmd {
	l.items = append(l.items, item)

	if idItem, ok := item.(interface{ ID() string }); ok {
		l.itemIDs[idItem.ID()] = len(l.items) - 1
	}

	return l.render()
}

// PrependItem adds an item to the beginning.
func (l *SimpleList) PrependItem(item Item) tea.Cmd {
	l.items = append([]Item{item}, l.items...)

	// Rebuild ID map (indices shifted).
	l.itemIDs = make(map[string]int, len(l.items))
	for i, it := range l.items {
		if idItem, ok := it.(interface{ ID() string }); ok {
			l.itemIDs[idItem.ID()] = i
		}
	}

	// Adjust selection.
	if l.selectedIdx >= 0 {
		l.selectedIdx++
	}
	if l.prevSelectedIdx >= 0 {
		l.prevSelectedIdx++
	}

	return l.render()
}

// UpdateItem replaces an item at the given index.
func (l *SimpleList) UpdateItem(idx int, item Item) tea.Cmd {
	if idx < 0 || idx >= len(l.items) {
		return nil
	}

	l.items[idx] = item

	// Update ID map.
	if idItem, ok := item.(interface{ ID() string }); ok {
		l.itemIDs[idItem.ID()] = idx
	}

	return l.render()
}

// DeleteItem removes an item at the given index.
func (l *SimpleList) DeleteItem(idx int) tea.Cmd {
	if idx < 0 || idx >= len(l.items) {
		return nil
	}

	l.items = append(l.items[:idx], l.items[idx+1:]...)

	// Rebuild ID map (indices shifted).
	l.itemIDs = make(map[string]int, len(l.items))
	for i, it := range l.items {
		if idItem, ok := it.(interface{ ID() string }); ok {
			l.itemIDs[idItem.ID()] = i
		}
	}

	// Adjust selection.
	if l.selectedIdx == idx {
		if idx > 0 {
			l.selectedIdx = idx - 1
		} else if len(l.items) > 0 {
			l.selectedIdx = 0
		} else {
			l.selectedIdx = -1
		}
	} else if l.selectedIdx > idx {
		l.selectedIdx--
	}

	if l.prevSelectedIdx == idx {
		l.prevSelectedIdx = -1
	} else if l.prevSelectedIdx > idx {
		l.prevSelectedIdx--
	}

	return l.render()
}

// Focus sets the list as focused.
func (l *SimpleList) Focus() tea.Cmd {
	l.focused = true
	return l.render()
}

// Blur removes focus from the list.
func (l *SimpleList) Blur() tea.Cmd {
	l.focused = false
	return l.render()
}

// Focused returns whether the list is focused.
func (l *SimpleList) Focused() bool {
	return l.focused
}

// Selection.

// Selected returns the currently selected item index.
func (l *SimpleList) Selected() int {
	return l.selectedIdx
}

// SelectedIndex returns the currently selected item index.
func (l *SimpleList) SelectedIndex() int {
	return l.selectedIdx
}

// SelectedItem returns the currently selected item.
func (l *SimpleList) SelectedItem() Item {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return nil
	}
	return l.items[l.selectedIdx]
}

// SetSelected sets the selected item by index.
func (l *SimpleList) SetSelected(idx int) tea.Cmd {
	if idx < -1 || idx >= len(l.items) {
		return nil
	}

	if l.selectedIdx == idx {
		return nil
	}

	l.prevSelectedIdx = l.selectedIdx
	l.selectedIdx = idx

	return l.render()
}

// SelectFirst selects the first item.
func (l *SimpleList) SelectFirst() tea.Cmd {
	return l.SetSelected(0)
}

// SelectLast selects the last item.
func (l *SimpleList) SelectLast() tea.Cmd {
	if len(l.items) > 0 {
		return l.SetSelected(len(l.items) - 1)
	}
	return nil
}

// SelectNext selects the next item.
func (l *SimpleList) SelectNext() tea.Cmd {
	if l.selectedIdx < len(l.items)-1 {
		return l.SetSelected(l.selectedIdx + 1)
	}
	return nil
}

// SelectPrev selects the previous item.
func (l *SimpleList) SelectPrev() tea.Cmd {
	if l.selectedIdx > 0 {
		return l.SetSelected(l.selectedIdx - 1)
	}
	return nil
}

// SelectNextWrap selects the next item (wraps to beginning).
func (l *SimpleList) SelectNextWrap() tea.Cmd {
	if len(l.items) == 0 {
		return nil
	}
	nextIdx := (l.selectedIdx + 1) % len(l.items)
	return l.SetSelected(nextIdx)
}

// SelectPrevWrap selects the previous item (wraps to end).
func (l *SimpleList) SelectPrevWrap() tea.Cmd {
	if len(l.items) == 0 {
		return nil
	}
	prevIdx := (l.selectedIdx - 1 + len(l.items)) % len(l.items)
	return l.SetSelected(prevIdx)
}

// SelectFirstInView selects the first fully visible item.
func (l *SimpleList) SelectFirstInView() tea.Cmd {
	if len(l.items) == 0 {
		return nil
	}

	start, end := l.viewPosition()

	for i := 0; i < len(l.items); i++ {
		if idItem, ok := l.items[i].(interface{ ID() string }); ok {
			if rItem, ok := l.renderedItems[idItem.ID()]; ok {
				// Check if fully visible.
				if rItem.start >= start && rItem.end <= end {
					return l.SetSelected(i)
				}
			}
		}
	}

	return nil
}

// SelectLastInView selects the last fully visible item.
func (l *SimpleList) SelectLastInView() tea.Cmd {
	if len(l.items) == 0 {
		return nil
	}

	start, end := l.viewPosition()

	for i := len(l.items) - 1; i >= 0; i-- {
		if idItem, ok := l.items[i].(interface{ ID() string }); ok {
			if rItem, ok := l.renderedItems[idItem.ID()]; ok {
				// Check if fully visible.
				if rItem.start >= start && rItem.end <= end {
					return l.SetSelected(i)
				}
			}
		}
	}

	return nil
}

// SelectedItemInView returns true if the selected item is visible.
func (l *SimpleList) SelectedItemInView() bool {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return false
	}

	var rItem *renderedItem
	if idItem, ok := l.items[l.selectedIdx].(interface{ ID() string }); ok {
		if ri, ok := l.renderedItems[idItem.ID()]; ok {
			rItem = &ri
		}
	}

	if rItem == nil {
		return false
	}

	start, end := l.viewPosition()
	return rItem.start < end && rItem.end > start
}

// Scrolling.

// Offset returns the current scroll offset.
func (l *SimpleList) Offset() int {
	return l.offset
}

// TotalHeight returns the total height of all items.
func (l *SimpleList) TotalHeight() int {
	return l.renderedHeight
}

// ScrollBy scrolls by the given number of lines.
func (l *SimpleList) ScrollBy(deltaLines int) tea.Cmd {
	l.offset += deltaLines
	l.clampOffset()
	return nil
}

// ScrollToTop scrolls to the top.
func (l *SimpleList) ScrollToTop() tea.Cmd {
	l.offset = 0
	return nil
}

// ScrollToBottom scrolls to the bottom.
func (l *SimpleList) ScrollToBottom() tea.Cmd {
	l.offset = l.renderedHeight - l.height
	l.clampOffset()
	return nil
}

// AtTop returns true if scrolled to the top.
func (l *SimpleList) AtTop() bool {
	return l.offset <= 0
}

// AtBottom returns true if scrolled to the bottom.
func (l *SimpleList) AtBottom() bool {
	return l.offset >= l.renderedHeight-l.height
}

// ScrollToItem scrolls to make an item visible.
func (l *SimpleList) ScrollToItem(idx int) tea.Cmd {
	if idx < 0 || idx >= len(l.items) {
		return nil
	}

	var rItem *renderedItem
	if idItem, ok := l.items[idx].(interface{ ID() string }); ok {
		if ri, ok := l.renderedItems[idItem.ID()]; ok {
			rItem = &ri
		}
	}

	if rItem == nil {
		return nil
	}

	start, end := l.viewPosition()

	// Already visible.
	if rItem.start >= start && rItem.end <= end {
		return nil
	}

	// Above viewport.
	if rItem.start < start {
		l.offset = rItem.start
		return nil
	}

	// Below viewport.
	if rItem.end > end {
		l.offset = rItem.end - l.height + 1
		l.clampOffset()
	}

	return nil
}

// ScrollToSelected scrolls to the selected item.
func (l *SimpleList) ScrollToSelected() tea.Cmd {
	if l.selectedIdx >= 0 {
		return l.ScrollToItem(l.selectedIdx)
	}
	return nil
}

func (l *SimpleList) clampOffset() {
	maxOffset := l.renderedHeight - l.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	l.offset = ordered.Clamp(l.offset, 0, maxOffset)
}

// Mouse and highlighting.

// HandleMouseDown handles mouse press.
func (l *SimpleList) HandleMouseDown(x, y int) bool {
	if x < 0 || y < 0 || x >= l.width || y >= l.height {
		return false
	}

	// Find item at viewport y.
	contentY := l.offset + y
	itemIdx := l.findItemAtLine(contentY)

	if itemIdx < 0 {
		return false
	}

	l.mouseDown = true
	l.mouseDownItem = itemIdx
	l.mouseDownX = x
	l.mouseDownY = y
	l.mouseDragItem = itemIdx
	l.mouseDragX = x
	l.mouseDragY = y

	// Start selection.
	l.selectionStartLine = y
	l.selectionStartCol = x
	l.selectionEndLine = y
	l.selectionEndCol = x

	// Select item.
	l.SetSelected(itemIdx)

	return true
}

// HandleMouseDrag handles mouse drag.
func (l *SimpleList) HandleMouseDrag(x, y int) bool {
	if !l.mouseDown {
		return false
	}

	// Clamp coordinates to viewport bounds.
	clampedX := max(0, min(x, l.width-1))
	clampedY := max(0, min(y, l.height-1))

	if clampedY >= 0 && clampedY < l.height {
		contentY := l.offset + clampedY
		itemIdx := l.findItemAtLine(contentY)
		if itemIdx >= 0 {
			l.mouseDragItem = itemIdx
			l.mouseDragX = clampedX
			l.mouseDragY = clampedY
		}
	}

	// Update selection end (clamped to viewport).
	l.selectionEndLine = clampedY
	l.selectionEndCol = clampedX

	return true
}

// HandleMouseUp handles mouse release.
func (l *SimpleList) HandleMouseUp(x, y int) bool {
	if !l.mouseDown {
		return false
	}

	l.mouseDown = false

	// Final selection update (clamped to viewport).
	clampedX := max(0, min(x, l.width-1))
	clampedY := max(0, min(y, l.height-1))
	l.selectionEndLine = clampedY
	l.selectionEndCol = clampedX

	return true
}

// ClearHighlight clears the selection.
func (l *SimpleList) ClearHighlight() {
	l.selectionStartLine = -1
	l.selectionStartCol = -1
	l.selectionEndLine = -1
	l.selectionEndCol = -1
	l.mouseDown = false
	l.mouseDownItem = -1
	l.mouseDragItem = -1
}

// GetHighlightedText returns the selected text.
func (l *SimpleList) GetHighlightedText() string {
	if !l.hasSelection() {
		return ""
	}

	return l.renderSelection(l.View())
}

func (l *SimpleList) hasSelection() bool {
	return l.selectionEndCol != l.selectionStartCol || l.selectionEndLine != l.selectionStartLine
}

// renderSelection applies highlighting to the view and extracts text.
func (l *SimpleList) renderSelection(view string) string {
	// Create a screen buffer spanning the viewport.
	buf := uv.NewScreenBuffer(l.width, l.height)
	area := uv.Rect(0, 0, l.width, l.height)
	uv.NewStyledString(view).Draw(&buf, area)

	// Calculate selection bounds.
	startLine := min(l.selectionStartLine, l.selectionEndLine)
	endLine := max(l.selectionStartLine, l.selectionEndLine)
	startCol := l.selectionStartCol
	endCol := l.selectionEndCol

	if l.selectionEndLine < l.selectionStartLine {
		startCol = l.selectionEndCol
		endCol = l.selectionStartCol
	}

	// Apply highlighting.
	for y := startLine; y <= endLine && y < l.height; y++ {
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

		// Apply highlight style.
		for x := colStart; x < colEnd && x < len(line); x++ {
			cell := line.At(x)
			if cell != nil && !cell.IsZero() {
				cell = cell.Clone()
				// Toggle reverse for highlight.
				if cell.Style.Attrs&uv.AttrReverse != 0 {
					cell.Style.Attrs &^= uv.AttrReverse
				} else {
					cell.Style.Attrs |= uv.AttrReverse
				}
				buf.SetCell(x, y, cell)
			}
		}
	}

	return buf.Render()
}

// findItemAtLine finds the item index at the given content line.
func (l *SimpleList) findItemAtLine(line int) int {
	for i := 0; i < len(l.items); i++ {
		if idItem, ok := l.items[i].(interface{ ID() string }); ok {
			if rItem, ok := l.renderedItems[idItem.ID()]; ok {
				if line >= rItem.start && line <= rItem.end {
					return i
				}
			}
		}
	}
	return -1
}

// Render returns the view (for compatibility).
func (l *SimpleList) Render() string {
	return l.View()
}
