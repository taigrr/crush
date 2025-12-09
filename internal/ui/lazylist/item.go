package lazylist

// Item represents a single item in the lazy-loaded list.
type Item interface {
	// Render returns the string representation of the item for the given
	// width.
	Render(width int) string
}

// Focusable represents an item that can gain or lose focus.
type Focusable interface {
	// Focus sets the focus state of the item.
	Focus()

	// Blur removes the focus state of the item.
	Blur()

	// Focused returns whether the item is focused.
	Focused() bool
}

// Highlightable represents an item that can have a highlighted region.
type Highlightable interface {
	// SetHighlight sets the highlight region (startLine, startCol) to (endLine, endCol).
	// Use -1 for all values to clear highlighting.
	SetHighlight(startLine, startCol, endLine, endCol int)

	// GetHighlight returns the current highlight region.
	GetHighlight() (startLine, startCol, endLine, endCol int)
}
