package diffview

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// lineNumSentinel is a width-1 rune used to discover the constant prefix and
// suffix lipgloss wraps around a fixed-width line number.
const lineNumSentinel = '\uE000'

// lineNumAffix holds the pre-rendered output of a line number style around
// content of exactly width cells.
type lineNumAffix struct {
	width  int
	prefix string
	suffix string
	ok     bool
}

func newLineNumAffix(st lipgloss.Style, width int) lineNumAffix {
	if width <= 0 {
		return lineNumAffix{}
	}
	sentinel := strings.Repeat(string(lineNumSentinel), width)
	out := st.Render(sentinel)
	idx := strings.Index(out, sentinel)
	if idx < 0 || strings.ContainsRune(out[:idx], lineNumSentinel) ||
		strings.ContainsRune(out[idx+len(sentinel):], lineNumSentinel) {
		return lineNumAffix{}
	}
	return lineNumAffix{width: width, prefix: out[:idx], suffix: out[idx+len(sentinel):], ok: true}
}

// plainSingleWidth reports whether s consists of runes that lipgloss renders
// verbatim, one cell each, like the sentinel.
func plainSingleWidth(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return false
		}
	}
	return ansi.StringWidth(s) == utf8.RuneCountInString(s)
}

func (ls *LineStyle) setLineNumAffixes(beforeDigits, afterDigits int) {
	ls.numAffixes[0] = newLineNumAffix(ls.LineNumber, beforeDigits)
	if afterDigits == beforeDigits {
		ls.numAffixes[1] = ls.numAffixes[0]
	} else {
		ls.numAffixes[1] = newLineNumAffix(ls.LineNumber, afterDigits)
	}
}

// renderLineNumber is equivalent to ls.LineNumber.Render(pad(v, numDigits)).
func (ls LineStyle) renderLineNumber(v any, numDigits int) string {
	s := pad(v, numDigits)
	for _, a := range ls.numAffixes {
		if a.ok && a.width == numDigits && utf8.RuneCountInString(s) == numDigits && plainSingleWidth(s) {
			return a.prefix + s + a.suffix
		}
	}
	return ls.LineNumber.Render(s)
}

func (dv *DiffView) prerenderLineNumbers() {
	for _, ls := range []*LineStyle{
		&dv.style.DividerLine, &dv.style.MissingLine, &dv.style.EqualLine,
		&dv.style.InsertLine, &dv.style.DeleteLine, &dv.style.Filename,
	} {
		ls.setLineNumAffixes(dv.beforeNumDigits, dv.afterNumDigits)
	}
}
