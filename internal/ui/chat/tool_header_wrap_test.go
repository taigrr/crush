package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// TestToolHeaderWrapsInsteadOfTruncating verifies a long command param
// wraps across multiple lines (no truncation ellipsis) and stays within
// the given width.
func TestToolHeaderWrapsInsteadOfTruncating(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	longCmd := "AK=$(pass nds/aws/gsa-access-key 2>&1); SK=$(pass nds/aws/gsa-secret-key 2>&1) echo \"ak len: ${#AK}, sk len: ${#SK}\""

	const width = 60
	header := toolHeader(&sty, ToolStatusSuccess, "Bash", width, false, longCmd)

	lines := strings.Split(header, "\n")
	require.Greater(t, len(lines), 1, "long command must wrap to multiple lines")

	for i, ln := range lines {
		require.NotContains(t, ansi.Strip(ln), "…",
			"wrapped header must not truncate with an ellipsis (line %d)", i)
		require.LessOrEqual(t, ansi.StringWidth(ln), width,
			"line %d exceeds width", i)
	}
}

// TestToolHeaderIndentsContinuationLines verifies continuation lines are
// indented to align under the first param (i.e. past the "● Bash "
// prefix).
func TestToolHeaderIndentsContinuationLines(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	longCmd := strings.Repeat("word ", 40)

	const width = 40
	header := toolHeader(&sty, ToolStatusSuccess, "Bash", width, false, longCmd)
	lines := strings.Split(header, "\n")
	require.Greater(t, len(lines), 1)

	// Continuation lines must be indented to align under the first param
	// (past the "✓ Bash " prefix).
	for _, ln := range lines[1:] {
		stripped := ansi.Strip(ln)
		leading := len(stripped) - len(strings.TrimLeft(stripped, " "))
		require.Positive(t, leading, "continuation line must be indented")
	}
}

// TestToolHeaderShortParamSingleLine verifies short params still render on
// one line with no wrapping artifacts.
func TestToolHeaderShortParamSingleLine(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	header := toolHeader(&sty, ToolStatusSuccess, "Bash", 120, false, "ls -la")
	require.NotContains(t, header, "\n")
	require.Contains(t, ansi.Strip(header), "ls -la")
}
