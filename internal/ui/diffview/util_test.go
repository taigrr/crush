package diffview

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPad(t *testing.T) {
	tests := []struct {
		input    any
		width    int
		expected string
	}{
		{7, 2, " 7"},
		{7, 3, "  7"},
		{"a", 2, " a"},
		{"a", 3, "  a"},
		{"…", 2, " …"},
		{"…", 3, "  …"},
	}

	for _, tt := range tests {
		result := pad(tt.input, tt.width)
		if result != tt.expected {
			t.Errorf("expected %q, got %q", tt.expected, result)
		}
	}
}

func TestRenderLineNumberMatchesLipgloss(t *testing.T) {
	t.Parallel()
	for _, st := range []Style{DefaultLightStyle(), DefaultDarkStyle()} {
		dv := New().Style(st)
		dv.adjustStyles()
		for _, ls := range []LineStyle{dv.style.EqualLine, dv.style.InsertLine, dv.style.DeleteLine, dv.style.MissingLine, dv.style.DividerLine, dv.style.Filename} {
			for _, digits := range []int{1, 2, 3, 4} {
				ls.setLineNumAffixes(digits, digits+1)
				for _, v := range []any{" ", "…", 0, 7, 42, 999, 12345} {
					for _, d := range []int{digits, digits + 1} {
						require.Equal(t, ls.LineNumber.Render(pad(v, d)), ls.renderLineNumber(v, d))
					}
				}
			}
		}
	}
}
