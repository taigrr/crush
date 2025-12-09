package lazylist

import "charm.land/lipgloss/v2"

// Item represents a single item in the lazy-loaded list.
type Item interface {
	// Render returns the string representation of the item for the given
	// width.
	Render(width int) string
}

// FocusStylable represents an item that can be styled based on focus state.
type FocusStylable interface {
	// FocusStyle returns the style to apply when the item is focused.
	FocusStyle() lipgloss.Style
	// BlurStyle returns the style to apply when the item is unfocused.
	BlurStyle() lipgloss.Style
}

// HighlightStylable represents an item that can be styled for highlighted regions.
type HighlightStylable interface {
	// HighlightStyle returns the style to apply for highlighted regions.
	HighlightStyle() lipgloss.Style
}
