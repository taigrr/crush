package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// Nord returns the Nord theme by Sven Greb.
// https://www.nordtheme.com
func Nord() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#88c0d0"), // frost4
		Secondary: lipgloss.Color("#81a1c1"), // frost3
		Accent:    lipgloss.Color("#a3be8c"), // aurora green
		Keyword:   lipgloss.Color("#b48ead"), // aurora purple

		FgBase:       lipgloss.Color("#eceff4"), // snow3
		FgSubtle:     lipgloss.Color("#e5e9f0"), // snow2
		FgMoreSubtle: lipgloss.Color("#d8dee9"), // snow1
		FgMostSubtle: lipgloss.Color("#7b88a1"),

		OnPrimary: lipgloss.Color("#2e3440"),

		BgBase:         lipgloss.Color("#2e3440"), // polar1
		BgLeastVisible: lipgloss.Color("#272c36"),
		BgLessVisible:  lipgloss.Color("#3b4252"), // polar2
		BgMostVisible:  lipgloss.Color("#434c5e"), // polar3
		Separator:      lipgloss.Color("#4c566a"), // polar4

		Destructive:       lipgloss.Color("#bf616a"),
		Error:             lipgloss.Color("#bf616a"),
		Warning:           lipgloss.Color("#d08770"),
		WarningSubtle:     lipgloss.Color("#ebcb8b"),
		Denied:            lipgloss.Color("#bf616a"),
		Busy:              lipgloss.Color("#ebcb8b"),
		Info:              lipgloss.Color("#88c0d0"),
		InfoMoreSubtle:    lipgloss.Color("#81a1c1"),
		InfoMostSubtle:    lipgloss.Color("#5e81ac"),
		Success:           lipgloss.Color("#a3be8c"),
		SuccessMoreSubtle: lipgloss.Color("#8fbcbb"),
		SuccessMostSubtle: lipgloss.Color("#5e81ac"),

		DiffAddFg:        lipgloss.Color("#a3be8c"),
		DiffAddBg:        lipgloss.Color("#37413a"),
		DiffAddBgEmph:    lipgloss.Color("#2f3832"),
		DiffRemoveFg:     lipgloss.Color("#bf616a"),
		DiffRemoveBg:     lipgloss.Color("#3f3037"),
		DiffRemoveBgEmph: lipgloss.Color("#36272d"),

		Hypercredit: lipgloss.Color("#88c0d0"),

		SyntaxLink:            lipgloss.Color("#88c0d0"),
		SyntaxImage:           lipgloss.Color("#b48ead"),
		SyntaxCommentPreproc:  lipgloss.Color("#ebcb8b"),
		SyntaxKeywordReserved: lipgloss.Color("#81a1c1"),
		SyntaxKeywordType:     lipgloss.Color("#8fbcbb"),
		SyntaxOperator:        lipgloss.Color("#81a1c1"),
		SyntaxNameBuiltin:     lipgloss.Color("#88c0d0"),
		SyntaxNameTag:         lipgloss.Color("#81a1c1"),
		SyntaxNameAttribute:   lipgloss.Color("#8fbcbb"),
		SyntaxNameClass:       lipgloss.Color("#8fbcbb"),
		SyntaxNameDecorator:   lipgloss.Color("#d08770"),
		SyntaxLiteralString:   lipgloss.Color("#a3be8c"),
	})
}

func NordLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#3b6e7a"),
		Secondary: lipgloss.Color("#4c6480"),
		Accent:    lipgloss.Color("#557547"),
		Keyword:   lipgloss.Color("#7b4f71"),

		FgBase:       lipgloss.Color("#2e3440"),
		FgSubtle:     lipgloss.Color("#3b4252"),
		FgMoreSubtle: lipgloss.Color("#4c566a"),
		FgMostSubtle: lipgloss.Color("#667287"),

		OnPrimary: lipgloss.Color("#eceff4"),

		BgBase:         lipgloss.Color("#eceff4"),
		BgLeastVisible: lipgloss.Color("#e5e9f0"),
		BgLessVisible:  lipgloss.Color("#d8dee9"),
		BgMostVisible:  lipgloss.Color("#9aa5b5"),
		Separator:      lipgloss.Color("#9aa5b5"),

		Destructive:       lipgloss.Color("#9b4049"),
		Error:             lipgloss.Color("#9b4049"),
		Warning:           lipgloss.Color("#96543f"),
		WarningSubtle:     lipgloss.Color("#806500"),
		Denied:            lipgloss.Color("#9b4049"),
		Busy:              lipgloss.Color("#806500"),
		Info:              lipgloss.Color("#4c6480"),
		InfoMoreSubtle:    lipgloss.Color("#39706f"),
		InfoMostSubtle:    lipgloss.Color("#4c566a"),
		Success:           lipgloss.Color("#557547"),
		SuccessMoreSubtle: lipgloss.Color("#39706f"),
		SuccessMostSubtle: lipgloss.Color("#4c566a"),

		DiffAddFg:        lipgloss.Color("#557547"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#9b4049"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#4c6480"),

		SyntaxLink:            lipgloss.Color("#4c6480"),
		SyntaxImage:           lipgloss.Color("#7b4f71"),
		SyntaxCommentPreproc:  lipgloss.Color("#96543f"),
		SyntaxKeywordReserved: lipgloss.Color("#7b4f71"),
		SyntaxKeywordType:     lipgloss.Color("#39706f"),
		SyntaxOperator:        lipgloss.Color("#7b4f71"),
		SyntaxNameBuiltin:     lipgloss.Color("#4c6480"),
		SyntaxNameTag:         lipgloss.Color("#9b4049"),
		SyntaxNameAttribute:   lipgloss.Color("#96543f"),
		SyntaxNameClass:       lipgloss.Color("#4c6480"),
		SyntaxNameDecorator:   lipgloss.Color("#7b4f71"),
		SyntaxLiteralString:   lipgloss.Color("#557547"),
	})
}
