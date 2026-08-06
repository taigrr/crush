package common

import (
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
