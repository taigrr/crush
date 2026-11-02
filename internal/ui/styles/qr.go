package styles

import "charm.land/lipgloss/v2"

// QRCode is the style for terminal QR codes. It is deliberately not
// themed: scanners need light modules on a dark field (or the reverse)
// with full contrast, so the colors are fixed and explicit, which also
// makes the code scan the same on dark and light terminals.
func QRCode() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#000000"))
}
