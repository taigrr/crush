package themes

import (
	"strings"

	"github.com/taigrr/crush/internal/ui/styles"
)

// DefaultThemeName is the name of the default builtin theme.
const DefaultThemeName = "charmtone"

// ThemeInfo describes a selectable theme for the picker.
type ThemeInfo struct {
	Name string
}

// builtinTheme pairs explicit dark and light Styles builders.
type builtinTheme struct {
	dark  func() styles.Styles
	light func() styles.Styles
}

// builtinThemes maps theme names to their builders. Names are matched
// case-insensitively (see normalizeThemeName).
var builtinThemes = map[string]builtinTheme{
	"charmtone":         {dark: CharmtonePantera, light: CharmtonePanteraLight},
	"hypercrush":        {dark: HypercrushObsidiana, light: HypercrushObsidianaLight},
	"tokyo-night":       {dark: TokyoNight, light: TokyoNightLight},
	"catppuccin-mocha":  {dark: CatppuccinMocha, light: CatppuccinLatte},
	"dracula":           {dark: Dracula, light: DraculaLight},
	"nord":              {dark: Nord, light: NordLight},
	"gruvbox-dark":      {dark: GruvboxDark, light: GruvboxLight},
	"rose-pine":         {dark: RosePine, light: RosePineDawn},
	"cyberpunk":         {dark: Cyberpunk, light: CyberpunkLight},
	"vscode-dark":       {dark: VSCodeDark, light: VSCodeLight},
	"monochrome":        {dark: Monochrome, light: MonochromeLight},
	"monochrome-green":  {dark: MonochromeGreen, light: MonochromeGreenLight},
	"monochrome-blue":   {dark: MonochromeBlue, light: MonochromeBlueLight},
	"monochrome-yellow": {dark: MonochromeYellow, light: MonochromeYellowLight},
	"monochrome-purple": {dark: MonochromePurple, light: MonochromePurpleLight},
	"monochrome-red":    {dark: MonochromeRed, light: MonochromeRedLight},
}

// builtinThemeOrder controls the display order in the theme picker.
var builtinThemeOrder = []string{
	"charmtone",
	"hypercrush",
	"tokyo-night",
	"catppuccin-mocha",
	"dracula",
	"nord",
	"gruvbox-dark",
	"rose-pine",
	"cyberpunk",
	"vscode-dark",
	"monochrome",
	"monochrome-green",
	"monochrome-blue",
	"monochrome-yellow",
	"monochrome-purple",
	"monochrome-red",
}

// normalizeThemeName lowercases and trims a theme name so lookups are
// case-insensitive and whitespace-tolerant.
func normalizeThemeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// NormalizeThemeName normalizes a theme name for case-insensitive,
// whitespace-tolerant comparison.
func NormalizeThemeName(name string) string {
	return normalizeThemeName(name)
}

// BuiltinThemeInfos returns the builtin themes in display order.
func BuiltinThemeInfos() []ThemeInfo {
	infos := make([]ThemeInfo, 0, len(builtinThemeOrder))
	for _, name := range builtinThemeOrder {
		infos = append(infos, ThemeInfo{Name: name})
	}
	return infos
}

// BuiltinThemeByName returns the Styles for a builtin theme by name. The
// lookup is case-insensitive. The boolean reports whether the theme exists.
func BuiltinThemeByName(name string, isDark ...bool) (styles.Styles, bool) {
	t, ok := builtinThemes[normalizeThemeName(name)]
	if !ok {
		return styles.Styles{}, false
	}
	if len(isDark) == 0 || isDark[0] {
		return t.dark(), true
	}
	return t.light(), true
}

// IsBuiltinTheme reports whether name refers to a builtin theme.
func IsBuiltinTheme(name string) bool {
	_, ok := builtinThemes[normalizeThemeName(name)]
	return ok
}

// ThemeForProvider returns the Styles associated with the given provider
// ID. Unknown or empty provider IDs yield the default Charmtone Pantera
// theme.
func ThemeForProvider(providerID string, isDark ...bool) styles.Styles {
	name := DefaultThemeName
	if providerID == "hyper" {
		name = "hypercrush"
	}
	theme, _ := BuiltinThemeByName(name, isDark...)
	return theme
}

// ResolveTheme returns the Styles for the named theme, searching builtins
// first and then user Lua themes in themesDir. If name is empty or cannot be
// resolved, it falls back to the provider-derived default theme.
func ResolveTheme(name, themesDir, providerID string, isDark ...bool) styles.Styles {
	if normalizeThemeName(name) != "" {
		if s, ok := BuiltinThemeByName(name, isDark...); ok {
			return s
		}
		if themesDir != "" {
			themes, _ := LoadUserThemes(themesDir)
			for _, t := range themes {
				if normalizeThemeName(t.Name) == normalizeThemeName(name) {
					return t.Styles
				}
			}
		}
	}
	return ThemeForProvider(providerID, isDark...)
}

// ResolveSwarmTheme returns the SwarmThemeConfig for the named theme,
// or the zero value (meaning "use the built-in defaults") when the
// theme is a builtin or cannot be found. Builtin themes never
// customize the swarm palette; user Lua themes may set the optional
// top-level `swarm` sub-table (see loadSwarmTheme).
func ResolveSwarmTheme(name, themesDir string) SwarmThemeConfig {
	if normalizeThemeName(name) == "" || themesDir == "" {
		return SwarmThemeConfig{}
	}
	if IsBuiltinTheme(name) {
		return SwarmThemeConfig{}
	}
	themes, _ := LoadUserThemes(themesDir)
	for _, t := range themes {
		if normalizeThemeName(t.Name) == normalizeThemeName(name) {
			return t.Swarm
		}
	}
	return SwarmThemeConfig{}
}
