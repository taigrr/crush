package styles

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func TestWithMinimalFlattensChrome(t *testing.T) {
	primary := lipgloss.Color("#c084fc")
	var s Styles
	base := lipgloss.NewStyle()
	s.Dialog.View = base.Border(lipgloss.RoundedBorder()).BorderForeground(primary)
	s.Dialog.Quit.Frame = base.Border(lipgloss.RoundedBorder()).Padding(1, 2)
	s.Dialog.Sessions.DeletingView = s.Dialog.View.BorderForeground(lipgloss.Color("#f87171"))
	s.CompactDetails.View = base.Padding(0, 1, 1, 1).Border(lipgloss.RoundedBorder()).BorderForeground(primary)
	s.Pills.Focused = base.Padding(0, 1).BorderStyle(lipgloss.RoundedBorder()).BorderForeground(primary)
	s.Messages.UserFocused = base.PaddingLeft(1).BorderLeft(true).BorderStyle(lipgloss.Border{Left: "▌"}).BorderForeground(primary)
	s.Editor.PromptNormalIconFocused = lipgloss.NewStyle().SetString(" > ")
	s.Editor.PromptNormalIconBlurred = lipgloss.NewStyle().SetString(" > ")
	s.Editor.PromptNormalFocused = lipgloss.NewStyle().SetString("::: ")
	s.Editor.PromptNormalBlurred = lipgloss.NewStyle().SetString("::: ")

	off := s.WithMinimal(false)
	require.Equal(t, 2, off.Dialog.View.GetHorizontalBorderSize())
	require.Contains(t, s.Dialog.View.Render("x"), "╭")

	on := s.WithMinimal(true)
	require.NotNil(t, on)
	require.NotSame(t, &s, on)
	require.True(t, on.Minimal)
	require.False(t, s.Minimal)
	require.Equal(t, 2, on.Dialog.View.GetHorizontalBorderSize())
	require.Equal(t, 2, on.Dialog.View.GetVerticalBorderSize())
	rendered := on.Dialog.View.Render("x")
	require.NotContains(t, rendered, "╭")
	require.Contains(t, rendered, "┌")
	require.Equal(t, 2, on.Dialog.Quit.Frame.GetHorizontalBorderSize())
	require.Equal(t, 2, on.Dialog.Sessions.DeletingView.GetHorizontalBorderSize())
	require.Zero(t, on.CompactDetails.View.GetHorizontalBorderSize())
	require.Zero(t, on.Pills.Focused.GetVerticalBorderSize())
	require.Equal(t, s.Pills.Focused.GetVerticalPadding(), on.Pills.Focused.GetVerticalPadding())
	require.NotContains(t, on.Messages.UserFocused.Render("x"), "▌")
	require.Equal(t, "┃ ", on.Editor.PromptNormalIconFocused.String())
	require.Equal(t, " > ", s.Editor.PromptNormalIconFocused.String())

	// The source theme stays rounded so minimal mode can be toggled off.
	require.Contains(t, s.Dialog.View.Render("x"), "╭")
}
