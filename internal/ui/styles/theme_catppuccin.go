package styles

import "charm.land/lipgloss/v2"

// CatppuccinMocha returns the Catppuccin Mocha dark theme.
// https://github.com/catppuccin/catppuccin
func CatppuccinMocha() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#cba6f7"), // mauve
		secondary: lipgloss.Color("#f5c2e7"), // pink
		accent:    lipgloss.Color("#a6e3a1"), // green
		keyword:   lipgloss.Color("#f38ba8"), // red

		fgBase:       lipgloss.Color("#cdd6f4"), // text
		fgSubtle:     lipgloss.Color("#bac2de"), // subtext1
		fgMoreSubtle: lipgloss.Color("#a6adc8"), // subtext0
		fgMostSubtle: lipgloss.Color("#7f849c"), // overlay1

		onPrimary: lipgloss.Color("#1e1e2e"),

		bgBase:         lipgloss.Color("#1e1e2e"), // base
		bgLeastVisible: lipgloss.Color("#181825"), // mantle
		bgLessVisible:  lipgloss.Color("#313244"), // surface0
		bgMostVisible:  lipgloss.Color("#45475a"), // surface1
		separator:      lipgloss.Color("#313244"),

		destructive:       lipgloss.Color("#eba0ac"), // maroon
		error:             lipgloss.Color("#f38ba8"), // red
		warning:           lipgloss.Color("#fab387"), // peach
		warningSubtle:     lipgloss.Color("#f9e2af"), // yellow
		denied:            lipgloss.Color("#eba0ac"),
		busy:              lipgloss.Color("#f9e2af"),
		info:              lipgloss.Color("#89b4fa"), // blue
		infoMoreSubtle:    lipgloss.Color("#74c7ec"), // sapphire
		infoMostSubtle:    lipgloss.Color("#585b70"), // surface2
		success:           lipgloss.Color("#a6e3a1"), // green
		successMoreSubtle: lipgloss.Color("#94e2d5"), // teal
		successMostSubtle: lipgloss.Color("#40a02b"),

		diffAddFg:        lipgloss.Color("#a6e3a1"),
		diffAddBg:        lipgloss.Color("#26332b"),
		diffAddBgEmph:    lipgloss.Color("#1f2a23"),
		diffRemoveFg:     lipgloss.Color("#f38ba8"),
		diffRemoveBg:     lipgloss.Color("#3a2b30"),
		diffRemoveBgEmph: lipgloss.Color("#2f2227"),

		hypercredit: lipgloss.Color("#f5e0dc"), // rosewater

		syntaxLink:            lipgloss.Color("#89b4fa"),
		syntaxImage:           lipgloss.Color("#f5c2e7"),
		syntaxCommentPreproc:  lipgloss.Color("#f9e2af"),
		syntaxKeywordReserved: lipgloss.Color("#cba6f7"),
		syntaxKeywordType:     lipgloss.Color("#94e2d5"),
		syntaxOperator:        lipgloss.Color("#89dceb"), // sky
		syntaxNameBuiltin:     lipgloss.Color("#f5c2e7"),
		syntaxNameTag:         lipgloss.Color("#cba6f7"),
		syntaxNameAttribute:   lipgloss.Color("#fab387"),
		syntaxNameClass:       lipgloss.Color("#f9e2af"),
		syntaxNameDecorator:   lipgloss.Color("#f9e2af"),
		syntaxLiteralString:   lipgloss.Color("#a6e3a1"),
	})
}

func CatppuccinLatte() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#8839ef"),
		secondary: lipgloss.Color("#ea76cb"),
		accent:    lipgloss.Color("#40a02b"),
		keyword:   lipgloss.Color("#d20f39"),

		fgBase:       lipgloss.Color("#4c4f69"),
		fgSubtle:     lipgloss.Color("#5c5f77"),
		fgMoreSubtle: lipgloss.Color("#6c6f85"),
		fgMostSubtle: lipgloss.Color("#7c7f93"),

		onPrimary: lipgloss.Color("#eff1f5"),

		bgBase:         lipgloss.Color("#eff1f5"),
		bgLeastVisible: lipgloss.Color("#e6e9ef"),
		bgLessVisible:  lipgloss.Color("#dce0e8"),
		bgMostVisible:  lipgloss.Color("#9ca0b0"),
		separator:      lipgloss.Color("#9ca0b0"),

		destructive:       lipgloss.Color("#d20f39"),
		error:             lipgloss.Color("#d20f39"),
		warning:           lipgloss.Color("#b45b00"),
		warningSubtle:     lipgloss.Color("#8c6f00"),
		denied:            lipgloss.Color("#d20f39"),
		busy:              lipgloss.Color("#8c6f00"),
		info:              lipgloss.Color("#1e66f5"),
		infoMoreSubtle:    lipgloss.Color("#047f8f"),
		infoMostSubtle:    lipgloss.Color("#6c6f85"),
		success:           lipgloss.Color("#287a15"),
		successMoreSubtle: lipgloss.Color("#047f8f"),
		successMostSubtle: lipgloss.Color("#6c6f85"),

		diffAddFg:        lipgloss.Color("#287a15"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#d20f39"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#ea76cb"),

		syntaxLink:            lipgloss.Color("#1e66f5"),
		syntaxImage:           lipgloss.Color("#8839ef"),
		syntaxCommentPreproc:  lipgloss.Color("#b45b00"),
		syntaxKeywordReserved: lipgloss.Color("#8839ef"),
		syntaxKeywordType:     lipgloss.Color("#047f8f"),
		syntaxOperator:        lipgloss.Color("#d20f39"),
		syntaxNameBuiltin:     lipgloss.Color("#1e66f5"),
		syntaxNameTag:         lipgloss.Color("#d20f39"),
		syntaxNameAttribute:   lipgloss.Color("#b45b00"),
		syntaxNameClass:       lipgloss.Color("#1e66f5"),
		syntaxNameDecorator:   lipgloss.Color("#8839ef"),
		syntaxLiteralString:   lipgloss.Color("#287a15"),
	})
}
