package qr

import (
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
)

// QuietZone is the light border, in modules, around a rendered symbol.
const QuietZone = 4

// Half-block glyphs: each terminal cell shows two vertically stacked
// modules.
const (
	glyphFull  = "█"
	glyphUpper = "▀"
	glyphLower = "▄"
	glyphEmpty = " "
)

// HalfBlocks renders the symbol with a quiet zone using half-block
// characters, two module rows per text line.
//
// Glyphs are drawn in the terminal's foreground color. By default the
// glyphs mark light modules, which suits the common light-on-dark
// terminal; invert makes glyphs mark dark modules for dark-on-light
// terminals. Either way the scanner sees dark modules on a light field.
func (c *Code) HalfBlocks(invert bool) string {
	ink := func(x, y int) bool { return c.Dark(x, y) == invert }
	span := c.Size + 2*QuietZone
	var sb strings.Builder
	for row := 0; row < span; row += 2 {
		y := row - QuietZone
		for col := range span {
			x := col - QuietZone
			top := ink(x, y)
			bottom := ink(x, y+1)
			switch {
			case top && bottom:
				sb.WriteString(glyphFull)
			case top:
				sb.WriteString(glyphUpper)
			case bottom:
				sb.WriteString(glyphLower)
			default:
				sb.WriteString(glyphEmpty)
			}
		}
		if row+2 < span {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// Image returns the symbol as a grayscale image with a quiet zone and
// scale pixels per module.
func (c *Code) Image(scale int) image.Image {
	scale = max(scale, 1)
	span := (c.Size + 2*QuietZone) * scale
	img := image.NewGray(image.Rect(0, 0, span, span))
	for py := range span {
		for px := range span {
			value := color.Gray{Y: 0xFF}
			if c.Dark(px/scale-QuietZone, py/scale-QuietZone) {
				value = color.Gray{Y: 0x00}
			}
			img.SetGray(px, py, value)
		}
	}
	return img
}

// WritePNG writes the symbol as a PNG image.
func (c *Code) WritePNG(w io.Writer, scale int) error {
	return png.Encode(w, c.Image(scale))
}
