package styles

import (
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func TestBuiltinThemeRegistry(t *testing.T) {
	t.Parallel()

	infos := BuiltinThemeInfos()
	require.NotEmpty(t, infos)
	require.Equal(t, "charmtone", infos[0].Name)

	_, ok := BuiltinThemeByName("charmtone")
	require.True(t, ok)
	_, ok = BuiltinThemeByName("CharmTone") // case-insensitive
	require.True(t, ok)
	_, ok = BuiltinThemeByName("nope")
	require.False(t, ok)

	require.True(t, IsBuiltinTheme("hypercrush"))
	require.False(t, IsBuiltinTheme("midnight"))
}

func TestResolveTheme(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "neon.lua"),
		[]byte(`return { name = "Neon", bg_base = "#001122" }`), 0o644))

	bg := func(s Styles) [4]uint32 { return colorRGBA(s.Background) }
	want := colorRGBA(lipgloss.Color("#001122"))

	// User theme resolves by name from dir.
	require.Equal(t, want, bg(ResolveTheme("Neon", dir, "")))
	// Case-insensitive.
	require.Equal(t, want, bg(ResolveTheme("nEoN", dir, "")))
	// Builtin takes precedence and resolves.
	require.Equal(t, colorRGBA(CharmtonePantera().Background), bg(ResolveTheme("charmtone", dir, "")))
	// Unknown name falls back to provider default (no panic).
	require.NotPanics(t, func() { ResolveTheme("does-not-exist", dir, "hyper") })
	// Empty name falls back to provider default.
	require.Equal(t, colorRGBA(HypercrushObsidiana().Background), bg(ResolveTheme("", dir, "hyper")))
}
