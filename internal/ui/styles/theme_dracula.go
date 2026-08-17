package styles

import "charm.land/lipgloss/v2"

// Dracula returns the Dracula theme.
// https://draculatheme.com
func Dracula() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#bd93f9"), // purple
		secondary: lipgloss.Color("#ff79c6"), // pink
		accent:    lipgloss.Color("#50fa7b"), // green
		keyword:   lipgloss.Color("#ff79c6"),

		fgBase:       lipgloss.Color("#f8f8f2"),
		fgSubtle:     lipgloss.Color("#bfbfbb"),
		fgMoreSubtle: lipgloss.Color("#8b8d99"),
		fgMostSubtle: lipgloss.Color("#6272a4"),

		onPrimary: lipgloss.Color("#282a36"),

		bgBase:         lipgloss.Color("#282a36"),
		bgLeastVisible: lipgloss.Color("#21222c"),
		bgLessVisible:  lipgloss.Color("#343746"),
		bgMostVisible:  lipgloss.Color("#44475a"),
		separator:      lipgloss.Color("#44475a"),

		destructive:       lipgloss.Color("#ff5555"),
		error:             lipgloss.Color("#ff5555"),
		warning:           lipgloss.Color("#ffb86c"),
		warningSubtle:     lipgloss.Color("#f1fa8c"),
		denied:            lipgloss.Color("#ff5555"),
		busy:              lipgloss.Color("#f1fa8c"),
		info:              lipgloss.Color("#8be9fd"),
		infoMoreSubtle:    lipgloss.Color("#6272a4"),
		infoMostSubtle:    lipgloss.Color("#44475a"),
		success:           lipgloss.Color("#50fa7b"),
		successMoreSubtle: lipgloss.Color("#8be9fd"),
		successMostSubtle: lipgloss.Color("#6272a4"),

		diffAddFg:        lipgloss.Color("#50fa7b"),
		diffAddBg:        lipgloss.Color("#2a3a32"),
		diffAddBgEmph:    lipgloss.Color("#22302a"),
		diffRemoveFg:     lipgloss.Color("#ff5555"),
		diffRemoveBg:     lipgloss.Color("#3a2a2c"),
		diffRemoveBgEmph: lipgloss.Color("#302224"),

		hypercredit: lipgloss.Color("#ff79c6"),

		syntaxLink:            lipgloss.Color("#8be9fd"),
		syntaxImage:           lipgloss.Color("#ff79c6"),
		syntaxCommentPreproc:  lipgloss.Color("#ffb86c"),
		syntaxKeywordReserved: lipgloss.Color("#ff79c6"),
		syntaxKeywordType:     lipgloss.Color("#8be9fd"),
		syntaxOperator:        lipgloss.Color("#ff79c6"),
		syntaxNameBuiltin:     lipgloss.Color("#bd93f9"),
		syntaxNameTag:         lipgloss.Color("#ff79c6"),
		syntaxNameAttribute:   lipgloss.Color("#50fa7b"),
		syntaxNameClass:       lipgloss.Color("#50fa7b"),
		syntaxNameDecorator:   lipgloss.Color("#50fa7b"),
		syntaxLiteralString:   lipgloss.Color("#f1fa8c"),
	})
}

func DraculaLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#6f42c1"),
		secondary: lipgloss.Color("#a71972"),
		accent:    lipgloss.Color("#287a3d"),
		keyword:   lipgloss.Color("#a71972"),

		fgBase:       lipgloss.Color("#282a36"),
		fgSubtle:     lipgloss.Color("#44475a"),
		fgMoreSubtle: lipgloss.Color("#5f6170"),
		fgMostSubtle: lipgloss.Color("#747789"),

		onPrimary: lipgloss.Color("#f8f8f2"),

		bgBase:         lipgloss.Color("#f8f8f2"),
		bgLeastVisible: lipgloss.Color("#eeeeea"),
		bgLessVisible:  lipgloss.Color("#e2e2dc"),
		bgMostVisible:  lipgloss.Color("#aaaab0"),
		separator:      lipgloss.Color("#aaaab0"),

		destructive:       lipgloss.Color("#b4232f"),
		error:             lipgloss.Color("#b4232f"),
		warning:           lipgloss.Color("#a34f00"),
		warningSubtle:     lipgloss.Color("#806c00"),
		denied:            lipgloss.Color("#b4232f"),
		busy:              lipgloss.Color("#806c00"),
		info:              lipgloss.Color("#1769aa"),
		infoMoreSubtle:    lipgloss.Color("#087f8c"),
		infoMostSubtle:    lipgloss.Color("#5f6170"),
		success:           lipgloss.Color("#287a3d"),
		successMoreSubtle: lipgloss.Color("#087f8c"),
		successMostSubtle: lipgloss.Color("#5f6170"),

		diffAddFg:        lipgloss.Color("#287a3d"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#b4232f"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#a71972"),

		syntaxLink:            lipgloss.Color("#1769aa"),
		syntaxImage:           lipgloss.Color("#6f42c1"),
		syntaxCommentPreproc:  lipgloss.Color("#a34f00"),
		syntaxKeywordReserved: lipgloss.Color("#6f42c1"),
		syntaxKeywordType:     lipgloss.Color("#087f8c"),
		syntaxOperator:        lipgloss.Color("#a71972"),
		syntaxNameBuiltin:     lipgloss.Color("#1769aa"),
		syntaxNameTag:         lipgloss.Color("#b4232f"),
		syntaxNameAttribute:   lipgloss.Color("#a34f00"),
		syntaxNameClass:       lipgloss.Color("#1769aa"),
		syntaxNameDecorator:   lipgloss.Color("#6f42c1"),
		syntaxLiteralString:   lipgloss.Color("#287a3d"),
	})
}
