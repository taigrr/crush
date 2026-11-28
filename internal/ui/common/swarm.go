package common

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/taigrr/simplecolorpalettes/palettes/html"
)

// SwarmSquare renders a single-cell colored Unicode block for the
// given swarm color name (an HTML palette entry like "aliceblue").
// Returns an empty string if the color is unknown or empty so callers
// can prepend the result unconditionally. Color names are normalized
// to lowercase so callers can pass any casing.
//
// The block uses ANSI truecolor via lipgloss so it displays even in
// terminals with limited palette support (lipgloss falls back
// automatically). We render the filled square glyph "■" — a single
// display cell, which is important because sidebar/picker layout is
// column-counted.
func SwarmSquare(colorName string) string {
	name := strings.ToLower(strings.TrimSpace(colorName))
	if name == "" {
		return ""
	}
	if _, ok := knownSwarmColor(name); !ok {
		return ""
	}
	c := html.GetNamedPalette().Get(name)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.ToHex())).Render("■")
}

// ThemeSwarmSquare paints a session square from the theme accent. rank is
// the session's age order, oldest first: the oldest is a dark wine shade
// of the accent and the newest is a bright warm shade of it.
func ThemeSwarmSquare(colorName string, palette []color.Color, rank, count int) string {
	accent := mostSaturated(palette)
	if colorName == "" || accent == nil {
		return ""
	}
	r, g, b, _ := accent.RGBA()
	rf, gf, bf := float64(r>>8), float64(g>>8), float64(b>>8)
	if count < 2 {
		count = 2
	}
	rank = min(max(rank, 0), count-1)
	t := float64(rank) / float64(count-1)
	// Dark end mixes toward a deep red so it reads as wine, not pink.
	// Bright end mixes toward yellow.
	rf = mix(rf*0.45+40, min(rf*1.15, 255), t)
	gf = mix(gf*0.25, min(gf*1.3+70*t, 255), t)
	bf = mix(bf*0.35, bf*0.5, t)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex(rf, gf, bf))).Render("■")
}

func mostSaturated(palette []color.Color) color.Color {
	var best color.Color
	bestSpread := -1.0
	for _, c := range palette {
		if c == nil {
			continue
		}
		r, g, b, _ := c.RGBA()
		spread := float64(max(r, g, b) - min(r, g, b))
		if spread > bestSpread {
			best, bestSpread = c, spread
		}
	}
	return best
}

func mix(dark, bright, t float64) float64 {
	return dark + (bright-dark)*t
}

func hex(r, g, b float64) string {
	rb, gb, bb := byte(r), byte(g), byte(b)
	const digits = "0123456789abcdef"
	return string([]byte{'#', digits[rb>>4], digits[rb&15], digits[gb>>4], digits[gb&15], digits[bb>>4], digits[bb&15]})
}

// knownSwarmColor reports whether the given name exists in the HTML
// palette. Cached in a package-level map on first use.
var swarmColorCache = func() map[string]struct{} {
	m := make(map[string]struct{})
	for _, name := range html.GetNamedPalette().Names() {
		m[name] = struct{}{}
	}
	return m
}()

func knownSwarmColor(name string) (struct{}, bool) {
	v, ok := swarmColorCache[name]
	return v, ok
}
