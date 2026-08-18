package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// CatppuccinMocha returns the Catppuccin Mocha dark theme.
// https://github.com/catppuccin/catppuccin
func CatppuccinMocha() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#cba6f7"), // mauve
		Secondary: lipgloss.Color("#f5c2e7"), // pink
		Accent:    lipgloss.Color("#a6e3a1"), // green
		Keyword:   lipgloss.Color("#f38ba8"), // red

		FgBase:       lipgloss.Color("#cdd6f4"), // text
		FgSubtle:     lipgloss.Color("#bac2de"), // subtext1
		FgMoreSubtle: lipgloss.Color("#a6adc8"), // subtext0
		FgMostSubtle: lipgloss.Color("#7f849c"), // overlay1

		OnPrimary: lipgloss.Color("#1e1e2e"),

		BgBase:         lipgloss.Color("#1e1e2e"), // base
		BgLeastVisible: lipgloss.Color("#181825"), // mantle
		BgLessVisible:  lipgloss.Color("#313244"), // surface0
		BgMostVisible:  lipgloss.Color("#45475a"), // surface1
		Separator:      lipgloss.Color("#313244"),

		Destructive:       lipgloss.Color("#eba0ac"), // maroon
		Error:             lipgloss.Color("#f38ba8"), // red
		Warning:           lipgloss.Color("#fab387"), // peach
		WarningSubtle:     lipgloss.Color("#f9e2af"), // yellow
		Denied:            lipgloss.Color("#eba0ac"),
		Busy:              lipgloss.Color("#f9e2af"),
		Info:              lipgloss.Color("#89b4fa"), // blue
		InfoMoreSubtle:    lipgloss.Color("#74c7ec"), // sapphire
		InfoMostSubtle:    lipgloss.Color("#585b70"), // surface2
		Success:           lipgloss.Color("#a6e3a1"), // green
		SuccessMoreSubtle: lipgloss.Color("#94e2d5"), // teal
		SuccessMostSubtle: lipgloss.Color("#40a02b"),

		DiffAddFg:        lipgloss.Color("#a6e3a1"),
		DiffAddBg:        lipgloss.Color("#26332b"),
		DiffAddBgEmph:    lipgloss.Color("#1f2a23"),
		DiffRemoveFg:     lipgloss.Color("#f38ba8"),
		DiffRemoveBg:     lipgloss.Color("#3a2b30"),
		DiffRemoveBgEmph: lipgloss.Color("#2f2227"),

		Hypercredit: lipgloss.Color("#f5e0dc"), // rosewater

		SyntaxLink:            lipgloss.Color("#89b4fa"),
		SyntaxImage:           lipgloss.Color("#f5c2e7"),
		SyntaxCommentPreproc:  lipgloss.Color("#f9e2af"),
		SyntaxKeywordReserved: lipgloss.Color("#cba6f7"),
		SyntaxKeywordType:     lipgloss.Color("#94e2d5"),
		SyntaxOperator:        lipgloss.Color("#89dceb"), // sky
		SyntaxNameBuiltin:     lipgloss.Color("#f5c2e7"),
		SyntaxNameTag:         lipgloss.Color("#cba6f7"),
		SyntaxNameAttribute:   lipgloss.Color("#fab387"),
		SyntaxNameClass:       lipgloss.Color("#f9e2af"),
		SyntaxNameDecorator:   lipgloss.Color("#f9e2af"),
		SyntaxLiteralString:   lipgloss.Color("#a6e3a1"),
	})
}

func CatppuccinLatte() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#8839ef"),
		Secondary: lipgloss.Color("#ea76cb"),
		Accent:    lipgloss.Color("#40a02b"),
		Keyword:   lipgloss.Color("#d20f39"),

		FgBase:       lipgloss.Color("#4c4f69"),
		FgSubtle:     lipgloss.Color("#5c5f77"),
		FgMoreSubtle: lipgloss.Color("#6c6f85"),
		FgMostSubtle: lipgloss.Color("#7c7f93"),

		OnPrimary: lipgloss.Color("#eff1f5"),

		BgBase:         lipgloss.Color("#eff1f5"),
		BgLeastVisible: lipgloss.Color("#e6e9ef"),
		BgLessVisible:  lipgloss.Color("#dce0e8"),
		BgMostVisible:  lipgloss.Color("#9ca0b0"),
		Separator:      lipgloss.Color("#9ca0b0"),

		Destructive:       lipgloss.Color("#d20f39"),
		Error:             lipgloss.Color("#d20f39"),
		Warning:           lipgloss.Color("#b45b00"),
		WarningSubtle:     lipgloss.Color("#8c6f00"),
		Denied:            lipgloss.Color("#d20f39"),
		Busy:              lipgloss.Color("#8c6f00"),
		Info:              lipgloss.Color("#1e66f5"),
		InfoMoreSubtle:    lipgloss.Color("#047f8f"),
		InfoMostSubtle:    lipgloss.Color("#6c6f85"),
		Success:           lipgloss.Color("#287a15"),
		SuccessMoreSubtle: lipgloss.Color("#047f8f"),
		SuccessMostSubtle: lipgloss.Color("#6c6f85"),

		DiffAddFg:        lipgloss.Color("#287a15"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#d20f39"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#ea76cb"),

		SyntaxLink:            lipgloss.Color("#1e66f5"),
		SyntaxImage:           lipgloss.Color("#8839ef"),
		SyntaxCommentPreproc:  lipgloss.Color("#b45b00"),
		SyntaxKeywordReserved: lipgloss.Color("#8839ef"),
		SyntaxKeywordType:     lipgloss.Color("#047f8f"),
		SyntaxOperator:        lipgloss.Color("#d20f39"),
		SyntaxNameBuiltin:     lipgloss.Color("#1e66f5"),
		SyntaxNameTag:         lipgloss.Color("#d20f39"),
		SyntaxNameAttribute:   lipgloss.Color("#b45b00"),
		SyntaxNameClass:       lipgloss.Color("#1e66f5"),
		SyntaxNameDecorator:   lipgloss.Color("#8839ef"),
		SyntaxLiteralString:   lipgloss.Color("#287a15"),
	})
}
