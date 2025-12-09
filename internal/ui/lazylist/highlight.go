package lazylist

import (
	"image"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// DefaultHighlighter is the default highlighter function that applies inverse style.
var DefaultHighlighter Highlighter = func(s uv.Style) uv.Style {
	s.Attrs |= uv.AttrReverse
	return s
}

// Highlighter represents a function that defines how to highlight text.
type Highlighter func(uv.Style) uv.Style

// Highlight highlights a region of text within the given content and region.
func Highlight(content string, area image.Rectangle, startLine, startCol, endLine, endCol int, highlighter Highlighter) string {
	if startLine < 0 || startCol < 0 {
		return content
	}

	if highlighter == nil {
		highlighter = DefaultHighlighter
	}

	width, height := area.Dx(), area.Dy()
	buf := uv.NewScreenBuffer(width, height)
	styled := uv.NewStyledString(content)
	styled.Draw(&buf, area)

	for y := startLine; y <= endLine && y < height; y++ {
		if y >= buf.Height() {
			break
		}

		line := buf.Line(y)

		// Determine column range for this line
		colStart := 0
		if y == startLine {
			colStart = min(startCol, len(line))
		}

		colEnd := len(line)
		if y == endLine {
			colEnd = min(endCol, len(line))
		}

		// Track last non-empty position as we go
		lastContentX := -1

		// Single pass: check content and track last non-empty position
		for x := colStart; x < colEnd; x++ {
			cell := line.At(x)
			if cell == nil {
				continue
			}

			// Update last content position if non-empty
			if cell.Content != "" && cell.Content != " " {
				lastContentX = x
			}
		}

		// Only apply highlight up to last content position
		highlightEnd := colEnd
		if lastContentX >= 0 {
			highlightEnd = lastContentX + 1
		} else if lastContentX == -1 {
			highlightEnd = colStart // No content on this line
		}

		// Apply highlight style only to cells with content
		for x := colStart; x < highlightEnd; x++ {
			if !image.Pt(x, y).In(area) {
				continue
			}
			cell := line.At(x)
			cell.Style = highlighter(cell.Style)
		}
	}

	return buf.Render()
}

// RenderWithHighlight renders content with optional focus styling and highlighting.
// This is a helper that combines common rendering logic for all items.
// The content parameter should be the raw rendered content before focus styling.
// The style parameter should come from CurrentStyle() and may be nil.
// func (b *BaseHighlightable) RenderWithHighlight(content string, width int, style *lipgloss.Style) string {
// 	// Apply focus/blur styling if configured
// 	rendered := content
// 	if style != nil {
// 		rendered = style.Render(rendered)
// 	}
//
// 	if !b.HasHighlight() {
// 		return rendered
// 	}
//
// 	height := lipgloss.Height(rendered)
//
// 	// Create temp buffer to draw content with highlighting
// 	tempBuf := uv.NewScreenBuffer(width, height)
//
// 	// Draw the rendered content to temp buffer
// 	styled := uv.NewStyledString(rendered)
// 	styled.Draw(&tempBuf, uv.Rect(0, 0, width, height))
//
// 	// Apply highlighting if active
// 	b.ApplyHighlight(&tempBuf, width, height, style)
//
// 	return tempBuf.Render()
// }

// ApplyHighlight applies highlighting to a screen buffer.
// This should be called after drawing content to the buffer.
// func (b *BaseHighlightable) ApplyHighlight(buf *uv.ScreenBuffer, width, height int, style *lipgloss.Style) {
// 	if b.highlightStartLine < 0 {
// 		return
// 	}
//
// 	var (
// 		topMargin, topBorder, topPadding          int
// 		rightMargin, rightBorder, rightPadding    int
// 		bottomMargin, bottomBorder, bottomPadding int
// 		leftMargin, leftBorder, leftPadding       int
// 	)
// 	if style != nil {
// 		topMargin, rightMargin, bottomMargin, leftMargin = style.GetMargin()
// 		topBorder, rightBorder, bottomBorder, leftBorder = style.GetBorderTopSize(),
// 			style.GetBorderRightSize(),
// 			style.GetBorderBottomSize(),
// 			style.GetBorderLeftSize()
// 		topPadding, rightPadding, bottomPadding, leftPadding = style.GetPadding()
// 	}
//
// 	slog.Info("Applying highlight",
// 		"highlightStartLine", b.highlightStartLine,
// 		"highlightStartCol", b.highlightStartCol,
// 		"highlightEndLine", b.highlightEndLine,
// 		"highlightEndCol", b.highlightEndCol,
// 		"width", width,
// 		"height", height,
// 		"margins", fmt.Sprintf("%d,%d,%d,%d", topMargin, rightMargin, bottomMargin, leftMargin),
// 		"borders", fmt.Sprintf("%d,%d,%d,%d", topBorder, rightBorder, bottomBorder, leftBorder),
// 		"paddings", fmt.Sprintf("%d,%d,%d,%d", topPadding, rightPadding, bottomPadding, leftPadding),
// 	)
//
// 	// Calculate content area offsets
// 	contentArea := image.Rectangle{
// 		Min: image.Point{
// 			X: leftMargin + leftBorder + leftPadding,
// 			Y: topMargin + topBorder + topPadding,
// 		},
// 		Max: image.Point{
// 			X: width - (rightMargin + rightBorder + rightPadding),
// 			Y: height - (bottomMargin + bottomBorder + bottomPadding),
// 		},
// 	}
//
// 	for y := b.highlightStartLine; y <= b.highlightEndLine && y < height; y++ {
// 		if y >= buf.Height() {
// 			break
// 		}
//
// 		line := buf.Line(y)
//
// 		// Determine column range for this line
// 		startCol := 0
// 		if y == b.highlightStartLine {
// 			startCol = min(b.highlightStartCol, len(line))
// 		}
//
// 		endCol := len(line)
// 		if y == b.highlightEndLine {
// 			endCol = min(b.highlightEndCol, len(line))
// 		}
//
// 		// Track last non-empty position as we go
// 		lastContentX := -1
//
// 		// Single pass: check content and track last non-empty position
// 		for x := startCol; x < endCol; x++ {
// 			cell := line.At(x)
// 			if cell == nil {
// 				continue
// 			}
//
// 			// Update last content position if non-empty
// 			if cell.Content != "" && cell.Content != " " {
// 				lastContentX = x
// 			}
// 		}
//
// 		// Only apply highlight up to last content position
// 		highlightEnd := endCol
// 		if lastContentX >= 0 {
// 			highlightEnd = lastContentX + 1
// 		} else if lastContentX == -1 {
// 			highlightEnd = startCol // No content on this line
// 		}
//
// 		// Apply highlight style only to cells with content
// 		for x := startCol; x < highlightEnd; x++ {
// 			if !image.Pt(x, y).In(contentArea) {
// 				continue
// 			}
// 			cell := line.At(x)
// 			cell.Style = b.highlightStyle(cell.Style)
// 		}
// 	}
// }

// ToHighlighter converts a [lipgloss.Style] to a [Highlighter].
func ToHighlighter(lgStyle lipgloss.Style) Highlighter {
	return func(uv.Style) uv.Style {
		return ToStyle(lgStyle)
	}
}

// ToStyle converts an inline [lipgloss.Style] to a [uv.Style].
func ToStyle(lgStyle lipgloss.Style) uv.Style {
	var uvStyle uv.Style

	// Colors are already color.Color
	uvStyle.Fg = lgStyle.GetForeground()
	uvStyle.Bg = lgStyle.GetBackground()

	// Build attributes using bitwise OR
	var attrs uint8

	if lgStyle.GetBold() {
		attrs |= uv.AttrBold
	}

	if lgStyle.GetItalic() {
		attrs |= uv.AttrItalic
	}

	if lgStyle.GetUnderline() {
		uvStyle.Underline = uv.UnderlineSingle
	}

	if lgStyle.GetStrikethrough() {
		attrs |= uv.AttrStrikethrough
	}

	if lgStyle.GetFaint() {
		attrs |= uv.AttrFaint
	}

	if lgStyle.GetBlink() {
		attrs |= uv.AttrBlink
	}

	if lgStyle.GetReverse() {
		attrs |= uv.AttrReverse
	}

	uvStyle.Attrs = attrs

	return uvStyle
}

// AdjustArea adjusts the given area rectangle by subtracting margins, borders,
// and padding from the style.
func AdjustArea(area image.Rectangle, style lipgloss.Style) image.Rectangle {
	topMargin, rightMargin, bottomMargin, leftMargin := style.GetMargin()
	topBorder, rightBorder, bottomBorder, leftBorder := style.GetBorderTopSize(),
		style.GetBorderRightSize(),
		style.GetBorderBottomSize(),
		style.GetBorderLeftSize()
	topPadding, rightPadding, bottomPadding, leftPadding := style.GetPadding()

	return image.Rectangle{
		Min: image.Point{
			X: area.Min.X + leftMargin + leftBorder + leftPadding,
			Y: area.Min.Y + topMargin + topBorder + topPadding,
		},
		Max: image.Point{
			X: area.Max.X - (rightMargin + rightBorder + rightPadding),
			Y: area.Max.Y - (bottomMargin + bottomBorder + bottomPadding),
		},
	}
}
