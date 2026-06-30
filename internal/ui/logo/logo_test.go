package logo

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testOpts(edition string) Opts {
	return Opts{
		FieldColor:   lipgloss.Color("#444444"),
		TitleColorA:  lipgloss.Color("#ff0000"),
		TitleColorB:  lipgloss.Color("#0000ff"),
		CharmColor:   lipgloss.Color("#ffffff"),
		VersionColor: lipgloss.Color("#888888"),
		Edition:      edition,
	}
}

// TestFieldRow_PlainWhenNoEdition verifies that without an edition label the
// banner row is just diagonals of the requested width.
func TestFieldRow_PlainWhenNoEdition(t *testing.T) {
	t.Parallel()
	row := fieldRow(lipgloss.NewStyle(), testOpts(""), 20)
	plain := ansi.Strip(row)
	assert.Equal(t, 20, lipgloss.Width(row))
	assert.Equal(t, strings.Repeat(diag, 20), plain)
}

// TestFieldRow_CentersEdition verifies the edition label is centered within
// the diagonal banner and the total width is preserved.
func TestFieldRow_CentersEdition(t *testing.T) {
	t.Parallel()
	const width = 30
	row := fieldRow(lipgloss.NewStyle(), testOpts("taigrr edition"), width)
	plain := ansi.Strip(row)

	require.Equal(t, width, lipgloss.Width(row),
		"the labeled banner must keep the requested width")
	require.Contains(t, plain, "taigrr edition")

	// Padded label is " taigrr edition " (16 cells); diagonals split the
	// remaining 14 cells as 7 left / 7 right.
	assert.Equal(t, strings.Repeat(diag, 7)+" taigrr edition "+strings.Repeat(diag, 7), plain)
}

// TestFieldRow_FallsBackWhenLabelTooWide verifies that a label that does not
// fit the width is dropped in favor of a plain diagonal row, never
// overflowing.
func TestFieldRow_FallsBackWhenLabelTooWide(t *testing.T) {
	t.Parallel()
	const width = 8
	row := fieldRow(lipgloss.NewStyle(), testOpts("taigrr edition"), width)
	plain := ansi.Strip(row)
	assert.Equal(t, width, lipgloss.Width(row))
	assert.Equal(t, strings.Repeat(diag, width), plain,
		"a label wider than the banner must fall back to plain diagonals")
}

// TestRender_CompactIncludesEditionBanner verifies the compact (sidebar)
// logo's top banner row carries the edition label.
func TestRender_CompactIncludesEditionBanner(t *testing.T) {
	t.Parallel()
	out := Render(lipgloss.NewStyle(), "v1.0.0", true, testOpts("taigrr edition"))
	topRow := ansi.Strip(strings.SplitN(out, "\n", 2)[0])
	assert.Contains(t, topRow, "taigrr edition",
		"the compact logo's top diagonal banner must show the edition label")
}

// TestRender_WideIncludesEditionBanner verifies the wide (landing/header)
// logo interrupts its top diagonal banner with the edition label when there
// is room.
func TestRender_WideIncludesEditionBanner(t *testing.T) {
	t.Parallel()
	o := testOpts("taigrr edition")
	o.Width = 120
	out := Render(lipgloss.NewStyle(), "v1.0.0", false, o)
	topRow := ansi.Strip(strings.SplitN(out, "\n", 2)[0])
	assert.Contains(t, topRow, "taigrr edition",
		"the wide logo's top diagonal banner must show the edition label")
}
