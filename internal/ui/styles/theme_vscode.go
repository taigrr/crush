package styles

import "charm.land/lipgloss/v2"

// VSCodeDark returns a theme based on Visual Studio Code's Dark+ scheme.
// https://github.com/microsoft/vscode
func VSCodeDark() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#569cd6"), // blue
		secondary: lipgloss.Color("#4ec9b0"), // teal
		accent:    lipgloss.Color("#c586c0"), // magenta
		keyword:   lipgloss.Color("#c586c0"),

		fgBase:       lipgloss.Color("#d4d4d4"),
		fgSubtle:     lipgloss.Color("#bbbbbb"),
		fgMoreSubtle: lipgloss.Color("#9d9d9d"),
		fgMostSubtle: lipgloss.Color("#6a6a6a"),

		onPrimary: lipgloss.Color("#ffffff"),

		bgBase:         lipgloss.Color("#1e1e1e"),
		bgLeastVisible: lipgloss.Color("#181818"),
		bgLessVisible:  lipgloss.Color("#252526"),
		bgMostVisible:  lipgloss.Color("#3c3c3c"),
		separator:      lipgloss.Color("#3c3c3c"),

		destructive:       lipgloss.Color("#f48771"),
		error:             lipgloss.Color("#f14c4c"),
		warning:           lipgloss.Color("#ffcc00"),
		warningSubtle:     lipgloss.Color("#cca700"),
		denied:            lipgloss.Color("#f48771"),
		busy:              lipgloss.Color("#dcdcaa"),
		info:              lipgloss.Color("#569cd6"),
		infoMoreSubtle:    lipgloss.Color("#4a7aa8"),
		infoMostSubtle:    lipgloss.Color("#2d4a63"),
		success:           lipgloss.Color("#89d185"),
		successMoreSubtle: lipgloss.Color("#4ec9b0"),
		successMostSubtle: lipgloss.Color("#3a7a5e"),

		diffAddFg:        lipgloss.Color("#89d185"),
		diffAddBg:        lipgloss.Color("#203428"),
		diffAddBgEmph:    lipgloss.Color("#1b2c22"),
		diffRemoveFg:     lipgloss.Color("#f14c4c"),
		diffRemoveBg:     lipgloss.Color("#3a2526"),
		diffRemoveBgEmph: lipgloss.Color("#301e1f"),

		hypercredit: lipgloss.Color("#569cd6"),

		syntaxLink:            lipgloss.Color("#569cd6"),
		syntaxImage:           lipgloss.Color("#c586c0"),
		syntaxCommentPreproc:  lipgloss.Color("#6a9955"),
		syntaxKeywordReserved: lipgloss.Color("#c586c0"),
		syntaxKeywordType:     lipgloss.Color("#4ec9b0"),
		syntaxOperator:        lipgloss.Color("#d4d4d4"),
		syntaxNameBuiltin:     lipgloss.Color("#dcdcaa"),
		syntaxNameTag:         lipgloss.Color("#569cd6"),
		syntaxNameAttribute:   lipgloss.Color("#9cdcfe"),
		syntaxNameClass:       lipgloss.Color("#4ec9b0"),
		syntaxNameDecorator:   lipgloss.Color("#dcdcaa"),
		syntaxLiteralString:   lipgloss.Color("#ce9178"),
	})
}

func VSCodeLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#005fb8"),
		secondary: lipgloss.Color("#007f6e"),
		accent:    lipgloss.Color("#8f3985"),
		keyword:   lipgloss.Color("#8f3985"),

		fgBase:       lipgloss.Color("#242424"),
		fgSubtle:     lipgloss.Color("#3b3b3b"),
		fgMoreSubtle: lipgloss.Color("#5f5f5f"),
		fgMostSubtle: lipgloss.Color("#767676"),

		onPrimary: lipgloss.Color("#ffffff"),

		bgBase:         lipgloss.Color("#ffffff"),
		bgLeastVisible: lipgloss.Color("#f3f3f3"),
		bgLessVisible:  lipgloss.Color("#e8e8e8"),
		bgMostVisible:  lipgloss.Color("#b8b8b8"),
		separator:      lipgloss.Color("#b8b8b8"),

		destructive:       lipgloss.Color("#b52020"),
		error:             lipgloss.Color("#b52020"),
		warning:           lipgloss.Color("#a34f00"),
		warningSubtle:     lipgloss.Color("#786000"),
		denied:            lipgloss.Color("#b52020"),
		busy:              lipgloss.Color("#786000"),
		info:              lipgloss.Color("#005fb8"),
		infoMoreSubtle:    lipgloss.Color("#007f6e"),
		infoMostSubtle:    lipgloss.Color("#5f5f5f"),
		success:           lipgloss.Color("#287a3d"),
		successMoreSubtle: lipgloss.Color("#007f6e"),
		successMostSubtle: lipgloss.Color("#5f5f5f"),

		diffAddFg:        lipgloss.Color("#287a3d"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#b52020"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#007f6e"),

		syntaxLink:            lipgloss.Color("#005fb8"),
		syntaxImage:           lipgloss.Color("#8f3985"),
		syntaxCommentPreproc:  lipgloss.Color("#a34f00"),
		syntaxKeywordReserved: lipgloss.Color("#8f3985"),
		syntaxKeywordType:     lipgloss.Color("#007f6e"),
		syntaxOperator:        lipgloss.Color("#8f3985"),
		syntaxNameBuiltin:     lipgloss.Color("#005fb8"),
		syntaxNameTag:         lipgloss.Color("#b52020"),
		syntaxNameAttribute:   lipgloss.Color("#a34f00"),
		syntaxNameClass:       lipgloss.Color("#005fb8"),
		syntaxNameDecorator:   lipgloss.Color("#8f3985"),
		syntaxLiteralString:   lipgloss.Color("#287a3d"),
	})
}
