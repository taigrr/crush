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

	// Every advertised builtin must actually build (smoke test catches a
	// new entry forgotten in the registry map vs. order slice).
	for _, info := range infos {
		dark, ok := BuiltinThemeByName(info.Name, true)
		require.True(t, ok, "BuiltinThemeByName missing for %q", info.Name)
		light, ok := BuiltinThemeByName(info.Name, false)
		require.True(t, ok, "BuiltinThemeByName missing light variant for %q", info.Name)
		require.NotNil(t, dark.Background, "%q builtin produced empty dark Styles", info.Name)
		require.NotNil(t, light.Background, "%q builtin produced empty light Styles", info.Name)
		require.NotEqual(t, colorRGBA(dark.Background), colorRGBA(light.Background), "%q variants use the same background", info.Name)
	}
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
	require.Equal(t, colorRGBA(CharmtonePanteraLight().Background), bg(ResolveTheme("charmtone", dir, "", false)))
	// User themes are fixed palettes and do not synthesize variants.
	require.Equal(t, want, bg(ResolveTheme("Neon", dir, "", false)))
	// Unknown name falls back to provider default (no panic).
	require.NotPanics(t, func() { ResolveTheme("does-not-exist", dir, "hyper") })
	// Empty name falls back to the provider's matching variant.
	require.Equal(t, colorRGBA(HypercrushObsidiana().Background), bg(ResolveTheme("", dir, "hyper")))
	require.Equal(t, colorRGBA(HypercrushObsidianaLight().Background), bg(ResolveTheme("", dir, "hyper", false)))
}
