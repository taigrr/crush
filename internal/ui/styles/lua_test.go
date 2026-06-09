package styles

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// colorRGBA normalizes a color to comparable RGBA components.
func colorRGBA(c color.Color) [4]uint32 {
	r, g, b, a := c.RGBA()
	return [4]uint32{r, g, b, a}
}

func TestLoadThemeFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "midnight.lua")
	require.NoError(t, os.WriteFile(path, []byte(`
return {
  name = "Midnight",
  is_dark = true,
  primary = "#7c6f9f",
  bg_base = "#1e1e2e",
  fg_base = "#cdd6f4",
}
`), 0o644))

	theme, err := LoadThemeFile(path)
	require.NoError(t, err)
	require.Equal(t, "Midnight", theme.Name)
	require.True(t, theme.IsDark)
	// Background should reflect the provided bg_base.
	require.Equal(t, colorRGBA(lipgloss.Color("#1e1e2e")), colorRGBA(theme.Styles.Background))
}

func TestLoadThemeFile_NameFallsBackToFilename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "solarized.lua")
	require.NoError(t, os.WriteFile(path, []byte(`return { primary = "#268bd2" }`), 0o644))

	theme, err := LoadThemeFile(path)
	require.NoError(t, err)
	require.Equal(t, "solarized", theme.Name)
	require.True(t, theme.IsDark) // defaults to dark
}

func TestLoadThemeFile_NonTableReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.lua")
	require.NoError(t, os.WriteFile(path, []byte(`return 42`), 0o644))

	_, err := LoadThemeFile(path)
	require.Error(t, err)
}

func TestLoadUserThemes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.lua"), []byte(`return { name = "Beta" }`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.lua"), []byte(`return { name = "Alpha" }`), 0o644))
	// A builtin-colliding theme is skipped.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.lua"), []byte(`return { name = "charmtone" }`), 0o644))
	// A non-lua file is ignored.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(`ignore me`), 0o644))

	themes, errs := LoadUserThemes(dir)
	require.Len(t, themes, 2)
	require.Equal(t, "Alpha", themes[0].Name)
	require.Equal(t, "Beta", themes[1].Name)
	// The collision is reported.
	require.NotEmpty(t, errs)
}

func TestLoadUserThemes_MissingDirIsNotError(t *testing.T) {
	t.Parallel()
	themes, errs := LoadUserThemes(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Empty(t, themes)
	require.Empty(t, errs)
}
