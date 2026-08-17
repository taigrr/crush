package styles

import "charm.land/lipgloss/v2"

// GruvboxDark returns the Gruvbox Dark "medium" theme by morhetz.
// https://github.com/morhetz/gruvbox
func GruvboxDark() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#fabd2f"), // bright yellow
		secondary: lipgloss.Color("#fe8019"), // bright orange
		accent:    lipgloss.Color("#b8bb26"), // bright green
		keyword:   lipgloss.Color("#fb4934"), // bright red

		fgBase:       lipgloss.Color("#ebdbb2"), // fg
		fgSubtle:     lipgloss.Color("#d5c4a1"), // fg2
		fgMoreSubtle: lipgloss.Color("#bdae93"), // fg3
		fgMostSubtle: lipgloss.Color("#928374"), // gray

		onPrimary: lipgloss.Color("#282828"),

		bgBase:         lipgloss.Color("#282828"), // bg
		bgLeastVisible: lipgloss.Color("#1d2021"), // bg0_h
		bgLessVisible:  lipgloss.Color("#3c3836"), // bg1
		bgMostVisible:  lipgloss.Color("#504945"), // bg2
		separator:      lipgloss.Color("#665c54"), // bg3

		destructive:       lipgloss.Color("#fb4934"),
		error:             lipgloss.Color("#cc241d"),
		warning:           lipgloss.Color("#fe8019"),
		warningSubtle:     lipgloss.Color("#fabd2f"),
		denied:            lipgloss.Color("#fb4934"),
		busy:              lipgloss.Color("#fabd2f"),
		info:              lipgloss.Color("#83a598"),
		infoMoreSubtle:    lipgloss.Color("#458588"),
		infoMostSubtle:    lipgloss.Color("#076678"),
		success:           lipgloss.Color("#b8bb26"),
		successMoreSubtle: lipgloss.Color("#8ec07c"),
		successMostSubtle: lipgloss.Color("#427b58"),

		diffAddFg:        lipgloss.Color("#b8bb26"),
		diffAddBg:        lipgloss.Color("#34381f"),
		diffAddBgEmph:    lipgloss.Color("#2b2f1a"),
		diffRemoveFg:     lipgloss.Color("#fb4934"),
		diffRemoveBg:     lipgloss.Color("#3c2828"),
		diffRemoveBgEmph: lipgloss.Color("#322020"),

		hypercredit: lipgloss.Color("#d3869b"), // bright purple

		syntaxLink:            lipgloss.Color("#83a598"),
		syntaxImage:           lipgloss.Color("#d3869b"),
		syntaxCommentPreproc:  lipgloss.Color("#fabd2f"),
		syntaxKeywordReserved: lipgloss.Color("#fb4934"),
		syntaxKeywordType:     lipgloss.Color("#fabd2f"),
		syntaxOperator:        lipgloss.Color("#fe8019"),
		syntaxNameBuiltin:     lipgloss.Color("#fabd2f"),
		syntaxNameTag:         lipgloss.Color("#83a598"),
		syntaxNameAttribute:   lipgloss.Color("#8ec07c"),
		syntaxNameClass:       lipgloss.Color("#fabd2f"),
		syntaxNameDecorator:   lipgloss.Color("#d3869b"),
		syntaxLiteralString:   lipgloss.Color("#b8bb26"),
	})
}

func GruvboxLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#8f6f00"),
		secondary: lipgloss.Color("#af3a03"),
		accent:    lipgloss.Color("#5f6f00"),
		keyword:   lipgloss.Color("#9d0006"),

		fgBase:       lipgloss.Color("#3c3836"),
		fgSubtle:     lipgloss.Color("#504945"),
		fgMoreSubtle: lipgloss.Color("#665c54"),
		fgMostSubtle: lipgloss.Color("#7c6f64"),

		onPrimary: lipgloss.Color("#fbf1c7"),

		bgBase:         lipgloss.Color("#fbf1c7"),
		bgLeastVisible: lipgloss.Color("#f2e5bc"),
		bgLessVisible:  lipgloss.Color("#ebdbb2"),
		bgMostVisible:  lipgloss.Color("#bdae93"),
		separator:      lipgloss.Color("#bdae93"),

		destructive:       lipgloss.Color("#9d0006"),
		error:             lipgloss.Color("#9d0006"),
		warning:           lipgloss.Color("#af3a03"),
		warningSubtle:     lipgloss.Color("#7c6500"),
		denied:            lipgloss.Color("#9d0006"),
		busy:              lipgloss.Color("#7c6500"),
		info:              lipgloss.Color("#076678"),
		infoMoreSubtle:    lipgloss.Color("#427b58"),
		infoMostSubtle:    lipgloss.Color("#665c54"),
		success:           lipgloss.Color("#5f6f00"),
		successMoreSubtle: lipgloss.Color("#427b58"),
		successMostSubtle: lipgloss.Color("#665c54"),

		diffAddFg:        lipgloss.Color("#5f6f00"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#9d0006"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#af3a03"),

		syntaxLink:            lipgloss.Color("#076678"),
		syntaxImage:           lipgloss.Color("#8f3f71"),
		syntaxCommentPreproc:  lipgloss.Color("#af3a03"),
		syntaxKeywordReserved: lipgloss.Color("#8f3f71"),
		syntaxKeywordType:     lipgloss.Color("#427b58"),
		syntaxOperator:        lipgloss.Color("#9d0006"),
		syntaxNameBuiltin:     lipgloss.Color("#076678"),
		syntaxNameTag:         lipgloss.Color("#9d0006"),
		syntaxNameAttribute:   lipgloss.Color("#af3a03"),
		syntaxNameClass:       lipgloss.Color("#076678"),
		syntaxNameDecorator:   lipgloss.Color("#8f3f71"),
		syntaxLiteralString:   lipgloss.Color("#5f6f00"),
	})
}
