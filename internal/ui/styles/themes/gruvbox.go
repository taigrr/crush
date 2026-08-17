package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// GruvboxDark returns the Gruvbox Dark "medium" theme by morhetz.
// https://github.com/morhetz/gruvbox
func GruvboxDark() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#fabd2f"), // bright yellow
		Secondary: lipgloss.Color("#fe8019"), // bright orange
		Accent:    lipgloss.Color("#b8bb26"), // bright green
		Keyword:   lipgloss.Color("#fb4934"), // bright red

		FgBase:       lipgloss.Color("#ebdbb2"), // fg
		FgSubtle:     lipgloss.Color("#d5c4a1"), // fg2
		FgMoreSubtle: lipgloss.Color("#bdae93"), // fg3
		FgMostSubtle: lipgloss.Color("#928374"), // gray

		OnPrimary: lipgloss.Color("#282828"),

		BgBase:         lipgloss.Color("#282828"), // bg
		BgLeastVisible: lipgloss.Color("#1d2021"), // bg0_h
		BgLessVisible:  lipgloss.Color("#3c3836"), // bg1
		BgMostVisible:  lipgloss.Color("#504945"), // bg2
		Separator:      lipgloss.Color("#665c54"), // bg3

		Destructive:       lipgloss.Color("#fb4934"),
		Error:             lipgloss.Color("#cc241d"),
		Warning:           lipgloss.Color("#fe8019"),
		WarningSubtle:     lipgloss.Color("#fabd2f"),
		Denied:            lipgloss.Color("#fb4934"),
		Busy:              lipgloss.Color("#fabd2f"),
		Info:              lipgloss.Color("#83a598"),
		InfoMoreSubtle:    lipgloss.Color("#458588"),
		InfoMostSubtle:    lipgloss.Color("#076678"),
		Success:           lipgloss.Color("#b8bb26"),
		SuccessMoreSubtle: lipgloss.Color("#8ec07c"),
		SuccessMostSubtle: lipgloss.Color("#427b58"),

		DiffAddFg:        lipgloss.Color("#b8bb26"),
		DiffAddBg:        lipgloss.Color("#34381f"),
		DiffAddBgEmph:    lipgloss.Color("#2b2f1a"),
		DiffRemoveFg:     lipgloss.Color("#fb4934"),
		DiffRemoveBg:     lipgloss.Color("#3c2828"),
		DiffRemoveBgEmph: lipgloss.Color("#322020"),

		Hypercredit: lipgloss.Color("#d3869b"), // bright purple

		SyntaxLink:            lipgloss.Color("#83a598"),
		SyntaxImage:           lipgloss.Color("#d3869b"),
		SyntaxCommentPreproc:  lipgloss.Color("#fabd2f"),
		SyntaxKeywordReserved: lipgloss.Color("#fb4934"),
		SyntaxKeywordType:     lipgloss.Color("#fabd2f"),
		SyntaxOperator:        lipgloss.Color("#fe8019"),
		SyntaxNameBuiltin:     lipgloss.Color("#fabd2f"),
		SyntaxNameTag:         lipgloss.Color("#83a598"),
		SyntaxNameAttribute:   lipgloss.Color("#8ec07c"),
		SyntaxNameClass:       lipgloss.Color("#fabd2f"),
		SyntaxNameDecorator:   lipgloss.Color("#d3869b"),
		SyntaxLiteralString:   lipgloss.Color("#b8bb26"),
	})
}

func GruvboxLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#8f6f00"),
		Secondary: lipgloss.Color("#af3a03"),
		Accent:    lipgloss.Color("#5f6f00"),
		Keyword:   lipgloss.Color("#9d0006"),

		FgBase:       lipgloss.Color("#3c3836"),
		FgSubtle:     lipgloss.Color("#504945"),
		FgMoreSubtle: lipgloss.Color("#665c54"),
		FgMostSubtle: lipgloss.Color("#7c6f64"),

		OnPrimary: lipgloss.Color("#fbf1c7"),

		BgBase:         lipgloss.Color("#fbf1c7"),
		BgLeastVisible: lipgloss.Color("#f2e5bc"),
		BgLessVisible:  lipgloss.Color("#ebdbb2"),
		BgMostVisible:  lipgloss.Color("#bdae93"),
		Separator:      lipgloss.Color("#bdae93"),

		Destructive:       lipgloss.Color("#9d0006"),
		Error:             lipgloss.Color("#9d0006"),
		Warning:           lipgloss.Color("#af3a03"),
		WarningSubtle:     lipgloss.Color("#7c6500"),
		Denied:            lipgloss.Color("#9d0006"),
		Busy:              lipgloss.Color("#7c6500"),
		Info:              lipgloss.Color("#076678"),
		InfoMoreSubtle:    lipgloss.Color("#427b58"),
		InfoMostSubtle:    lipgloss.Color("#665c54"),
		Success:           lipgloss.Color("#5f6f00"),
		SuccessMoreSubtle: lipgloss.Color("#427b58"),
		SuccessMostSubtle: lipgloss.Color("#665c54"),

		DiffAddFg:        lipgloss.Color("#5f6f00"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#9d0006"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#af3a03"),

		SyntaxLink:            lipgloss.Color("#076678"),
		SyntaxImage:           lipgloss.Color("#8f3f71"),
		SyntaxCommentPreproc:  lipgloss.Color("#af3a03"),
		SyntaxKeywordReserved: lipgloss.Color("#8f3f71"),
		SyntaxKeywordType:     lipgloss.Color("#427b58"),
		SyntaxOperator:        lipgloss.Color("#9d0006"),
		SyntaxNameBuiltin:     lipgloss.Color("#076678"),
		SyntaxNameTag:         lipgloss.Color("#9d0006"),
		SyntaxNameAttribute:   lipgloss.Color("#af3a03"),
		SyntaxNameClass:       lipgloss.Color("#076678"),
		SyntaxNameDecorator:   lipgloss.Color("#8f3f71"),
		SyntaxLiteralString:   lipgloss.Color("#5f6f00"),
	})
}
