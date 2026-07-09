package styles

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// DefaultThemeName is the name of the default builtin theme.
const DefaultThemeName = "charmtone"

// ThemeInfo describes a selectable theme for the picker.
type ThemeInfo struct {
	Name   string
	IsDark bool
}

// builtinTheme pairs a Styles builder with dark/light metadata.
type builtinTheme struct {
	isDark bool
	build  func() Styles
}

// builtinThemes maps theme names to their builders. Names are matched
// case-insensitively (see normalizeThemeName).
var builtinThemes = map[string]builtinTheme{
	"charmtone":        {isDark: true, build: CharmtonePantera},
	"hypercrush":       {isDark: true, build: HypercrushObsidiana},
	"tokyo-night":      {isDark: true, build: TokyoNight},
	"catppuccin-mocha": {isDark: true, build: CatppuccinMocha},
	"dracula":          {isDark: true, build: Dracula},
	"nord":             {isDark: true, build: Nord},
	"gruvbox-dark":     {isDark: true, build: GruvboxDark},
	"rose-pine":        {isDark: true, build: RosePine},
	"cyberpunk":        {isDark: true, build: Cyberpunk},
	"vscode-dark":      {isDark: true, build: VSCodeDark},
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
		infos = append(infos, ThemeInfo{Name: name, IsDark: builtinThemes[name].isDark})
	}
	return infos
}

// BuiltinThemeByName returns the Styles for a builtin theme by name. The
// lookup is case-insensitive. The boolean reports whether the theme exists.
func BuiltinThemeByName(name string) (Styles, bool) {
	t, ok := builtinThemes[normalizeThemeName(name)]
	if !ok {
		return Styles{}, false
	}
	return t.build(), true
}

// IsBuiltinTheme reports whether name refers to a builtin theme.
func IsBuiltinTheme(name string) bool {
	_, ok := builtinThemes[normalizeThemeName(name)]
	return ok
}

// ThemeForProvider returns the Styles associated with the given provider
// ID. Unknown or empty provider IDs yield the default Charmtone Pantera
// theme.
func ThemeForProvider(providerID string) Styles {
	switch providerID {
	case "hyper":
		return HypercrushObsidiana()
	default:
		return CharmtonePantera()
	}
}

// ResolveTheme returns the Styles for the named theme, searching builtins
// first and then user Lua themes in themesDir. If name is empty or cannot be
// resolved, it falls back to the provider-derived default theme.
func ResolveTheme(name, themesDir, providerID string) Styles {
	if normalizeThemeName(name) != "" {
		if s, ok := BuiltinThemeByName(name); ok {
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
	return ThemeForProvider(providerID)
}

// CharmtonePantera returns the Charmtone dark theme. It's the default style
// for the UI.
func CharmtonePantera() Styles {
	return quickStyle(quickStyleOpts{
		primary:   charmtone.Charple,
		secondary: charmtone.Dolly,
		accent:    charmtone.Bok,
		keyword:   charmtone.Blush,

		fgBase:       charmtone.Sash,
		fgMoreSubtle: charmtone.Squid,
		fgSubtle:     charmtone.Smoke,
		fgMostSubtle: charmtone.Oyster,

		onPrimary: charmtone.Butter,

		bgBase:         charmtone.Pepper,
		bgLeastVisible: charmtone.BBQ,
		bgLessVisible:  charmtone.Char,
		bgMostVisible:  charmtone.Iron,

		separator: charmtone.Char,

		destructive:       charmtone.Coral,
		error:             charmtone.Sriracha,
		warningSubtle:     charmtone.Zest,
		warning:           charmtone.Mustard,
		denied:            charmtone.Tang,
		busy:              charmtone.Citron,
		info:              charmtone.Malibu,
		infoMoreSubtle:    charmtone.Sardine,
		infoMostSubtle:    charmtone.Damson,
		success:           charmtone.Julep,
		successMoreSubtle: charmtone.Bok,
		successMostSubtle: charmtone.Guac,

		diffAddFg:        lipgloss.Color("#629657"),
		diffAddBg:        lipgloss.Color("#323931"),
		diffAddBgEmph:    lipgloss.Color("#2b322a"),
		diffRemoveFg:     lipgloss.Color("#a45c59"),
		diffRemoveBg:     lipgloss.Color("#383030"),
		diffRemoveBgEmph: lipgloss.Color("#312929"),

		hypercredit: charmtone.Dolly,

		syntaxLink:            charmtone.Zinc,
		syntaxImage:           charmtone.Cheeky,
		syntaxCommentPreproc:  charmtone.Bengal,
		syntaxKeywordReserved: charmtone.Pony,
		syntaxKeywordType:     charmtone.Guppy,
		syntaxOperator:        charmtone.Salmon,
		syntaxNameBuiltin:     charmtone.Cheeky,
		syntaxNameTag:         charmtone.Mauve,
		syntaxNameAttribute:   charmtone.Hazy,
		syntaxNameClass:       charmtone.Salt,
		syntaxNameDecorator:   charmtone.Citron,
		syntaxLiteralString:   charmtone.Cumin,
	})
}

// HypercrushObsidiana returns the Hypercrush dark theme.
func HypercrushObsidiana() Styles {
	return quickStyle(quickStyleOpts{
		primary:   charmtone.Charple,
		secondary: charmtone.Dolly,
		accent:    charmtone.Bok,

		fgBase:       charmtone.Sash,
		fgMoreSubtle: charmtone.Squid,
		fgSubtle:     charmtone.Smoke,
		fgMostSubtle: charmtone.Oyster,

		onPrimary: charmtone.Butter,

		bgBase:         charmtone.Pepper,
		bgLeastVisible: charmtone.BBQ,
		bgLessVisible:  charmtone.Char,
		bgMostVisible:  charmtone.Iron,

		separator: charmtone.Char,

		destructive:       charmtone.Coral,
		error:             charmtone.Sriracha,
		warningSubtle:     charmtone.Zest,
		warning:           charmtone.Mustard,
		denied:            charmtone.Tang,
		busy:              charmtone.Citron,
		info:              charmtone.Malibu,
		infoMoreSubtle:    charmtone.Sardine,
		infoMostSubtle:    charmtone.Damson,
		success:           charmtone.Julep,
		successMoreSubtle: charmtone.Bok,
		successMostSubtle: charmtone.Guac,

		diffAddFg:        lipgloss.Color("#629657"),
		diffAddBg:        lipgloss.Color("#323931"),
		diffAddBgEmph:    lipgloss.Color("#2b322a"),
		diffRemoveFg:     lipgloss.Color("#a45c59"),
		diffRemoveBg:     lipgloss.Color("#383030"),
		diffRemoveBgEmph: lipgloss.Color("#312929"),

		hypercredit: charmtone.Dolly,

		syntaxLink:            charmtone.Zinc,
		syntaxImage:           charmtone.Cheeky,
		syntaxCommentPreproc:  charmtone.Bengal,
		syntaxKeywordReserved: charmtone.Pony,
		syntaxKeywordType:     charmtone.Guppy,
		syntaxOperator:        charmtone.Salmon,
		syntaxNameBuiltin:     charmtone.Cheeky,
		syntaxNameTag:         charmtone.Mauve,
		syntaxNameAttribute:   charmtone.Hazy,
		syntaxNameClass:       charmtone.Salt,
		syntaxNameDecorator:   charmtone.Citron,
		syntaxLiteralString:   charmtone.Cumin,
	})
}
