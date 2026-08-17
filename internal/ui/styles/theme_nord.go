package styles

import "charm.land/lipgloss/v2"

// Nord returns the Nord theme by Sven Greb.
// https://www.nordtheme.com
func Nord() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#88c0d0"), // frost4
		secondary: lipgloss.Color("#81a1c1"), // frost3
		accent:    lipgloss.Color("#a3be8c"), // aurora green
		keyword:   lipgloss.Color("#b48ead"), // aurora purple

		fgBase:       lipgloss.Color("#eceff4"), // snow3
		fgSubtle:     lipgloss.Color("#e5e9f0"), // snow2
		fgMoreSubtle: lipgloss.Color("#d8dee9"), // snow1
		fgMostSubtle: lipgloss.Color("#7b88a1"),

		onPrimary: lipgloss.Color("#2e3440"),

		bgBase:         lipgloss.Color("#2e3440"), // polar1
		bgLeastVisible: lipgloss.Color("#272c36"),
		bgLessVisible:  lipgloss.Color("#3b4252"), // polar2
		bgMostVisible:  lipgloss.Color("#434c5e"), // polar3
		separator:      lipgloss.Color("#4c566a"), // polar4

		destructive:       lipgloss.Color("#bf616a"),
		error:             lipgloss.Color("#bf616a"),
		warning:           lipgloss.Color("#d08770"),
		warningSubtle:     lipgloss.Color("#ebcb8b"),
		denied:            lipgloss.Color("#bf616a"),
		busy:              lipgloss.Color("#ebcb8b"),
		info:              lipgloss.Color("#88c0d0"),
		infoMoreSubtle:    lipgloss.Color("#81a1c1"),
		infoMostSubtle:    lipgloss.Color("#5e81ac"),
		success:           lipgloss.Color("#a3be8c"),
		successMoreSubtle: lipgloss.Color("#8fbcbb"),
		successMostSubtle: lipgloss.Color("#5e81ac"),

		diffAddFg:        lipgloss.Color("#a3be8c"),
		diffAddBg:        lipgloss.Color("#37413a"),
		diffAddBgEmph:    lipgloss.Color("#2f3832"),
		diffRemoveFg:     lipgloss.Color("#bf616a"),
		diffRemoveBg:     lipgloss.Color("#3f3037"),
		diffRemoveBgEmph: lipgloss.Color("#36272d"),

		hypercredit: lipgloss.Color("#88c0d0"),

		syntaxLink:            lipgloss.Color("#88c0d0"),
		syntaxImage:           lipgloss.Color("#b48ead"),
		syntaxCommentPreproc:  lipgloss.Color("#ebcb8b"),
		syntaxKeywordReserved: lipgloss.Color("#81a1c1"),
		syntaxKeywordType:     lipgloss.Color("#8fbcbb"),
		syntaxOperator:        lipgloss.Color("#81a1c1"),
		syntaxNameBuiltin:     lipgloss.Color("#88c0d0"),
		syntaxNameTag:         lipgloss.Color("#81a1c1"),
		syntaxNameAttribute:   lipgloss.Color("#8fbcbb"),
		syntaxNameClass:       lipgloss.Color("#8fbcbb"),
		syntaxNameDecorator:   lipgloss.Color("#d08770"),
		syntaxLiteralString:   lipgloss.Color("#a3be8c"),
	})
}

func NordLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#3b6e7a"),
		secondary: lipgloss.Color("#4c6480"),
		accent:    lipgloss.Color("#557547"),
		keyword:   lipgloss.Color("#7b4f71"),

		fgBase:       lipgloss.Color("#2e3440"),
		fgSubtle:     lipgloss.Color("#3b4252"),
		fgMoreSubtle: lipgloss.Color("#4c566a"),
		fgMostSubtle: lipgloss.Color("#667287"),

		onPrimary: lipgloss.Color("#eceff4"),

		bgBase:         lipgloss.Color("#eceff4"),
		bgLeastVisible: lipgloss.Color("#e5e9f0"),
		bgLessVisible:  lipgloss.Color("#d8dee9"),
		bgMostVisible:  lipgloss.Color("#9aa5b5"),
		separator:      lipgloss.Color("#9aa5b5"),

		destructive:       lipgloss.Color("#9b4049"),
		error:             lipgloss.Color("#9b4049"),
		warning:           lipgloss.Color("#96543f"),
		warningSubtle:     lipgloss.Color("#806500"),
		denied:            lipgloss.Color("#9b4049"),
		busy:              lipgloss.Color("#806500"),
		info:              lipgloss.Color("#4c6480"),
		infoMoreSubtle:    lipgloss.Color("#39706f"),
		infoMostSubtle:    lipgloss.Color("#4c566a"),
		success:           lipgloss.Color("#557547"),
		successMoreSubtle: lipgloss.Color("#39706f"),
		successMostSubtle: lipgloss.Color("#4c566a"),

		diffAddFg:        lipgloss.Color("#557547"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#9b4049"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#4c6480"),

		syntaxLink:            lipgloss.Color("#4c6480"),
		syntaxImage:           lipgloss.Color("#7b4f71"),
		syntaxCommentPreproc:  lipgloss.Color("#96543f"),
		syntaxKeywordReserved: lipgloss.Color("#7b4f71"),
		syntaxKeywordType:     lipgloss.Color("#39706f"),
		syntaxOperator:        lipgloss.Color("#7b4f71"),
		syntaxNameBuiltin:     lipgloss.Color("#4c6480"),
		syntaxNameTag:         lipgloss.Color("#9b4049"),
		syntaxNameAttribute:   lipgloss.Color("#96543f"),
		syntaxNameClass:       lipgloss.Color("#4c6480"),
		syntaxNameDecorator:   lipgloss.Color("#7b4f71"),
		syntaxLiteralString:   lipgloss.Color("#557547"),
	})
}
