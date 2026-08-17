package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// VSCodeDark returns a theme based on Visual Studio Code's Dark+ scheme.
// https://github.com/microsoft/vscode
func VSCodeDark() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#569cd6"), // blue
		Secondary: lipgloss.Color("#4ec9b0"), // teal
		Accent:    lipgloss.Color("#c586c0"), // magenta
		Keyword:   lipgloss.Color("#c586c0"),

		FgBase:       lipgloss.Color("#d4d4d4"),
		FgSubtle:     lipgloss.Color("#bbbbbb"),
		FgMoreSubtle: lipgloss.Color("#9d9d9d"),
		FgMostSubtle: lipgloss.Color("#6a6a6a"),

		OnPrimary: lipgloss.Color("#ffffff"),

		BgBase:         lipgloss.Color("#1e1e1e"),
		BgLeastVisible: lipgloss.Color("#181818"),
		BgLessVisible:  lipgloss.Color("#252526"),
		BgMostVisible:  lipgloss.Color("#3c3c3c"),
		Separator:      lipgloss.Color("#3c3c3c"),

		Destructive:       lipgloss.Color("#f48771"),
		Error:             lipgloss.Color("#f14c4c"),
		Warning:           lipgloss.Color("#ffcc00"),
		WarningSubtle:     lipgloss.Color("#cca700"),
		Denied:            lipgloss.Color("#f48771"),
		Busy:              lipgloss.Color("#dcdcaa"),
		Info:              lipgloss.Color("#569cd6"),
		InfoMoreSubtle:    lipgloss.Color("#4a7aa8"),
		InfoMostSubtle:    lipgloss.Color("#2d4a63"),
		Success:           lipgloss.Color("#89d185"),
		SuccessMoreSubtle: lipgloss.Color("#4ec9b0"),
		SuccessMostSubtle: lipgloss.Color("#3a7a5e"),

		DiffAddFg:        lipgloss.Color("#89d185"),
		DiffAddBg:        lipgloss.Color("#203428"),
		DiffAddBgEmph:    lipgloss.Color("#1b2c22"),
		DiffRemoveFg:     lipgloss.Color("#f14c4c"),
		DiffRemoveBg:     lipgloss.Color("#3a2526"),
		DiffRemoveBgEmph: lipgloss.Color("#301e1f"),

		Hypercredit: lipgloss.Color("#569cd6"),

		SyntaxLink:            lipgloss.Color("#569cd6"),
		SyntaxImage:           lipgloss.Color("#c586c0"),
		SyntaxCommentPreproc:  lipgloss.Color("#6a9955"),
		SyntaxKeywordReserved: lipgloss.Color("#c586c0"),
		SyntaxKeywordType:     lipgloss.Color("#4ec9b0"),
		SyntaxOperator:        lipgloss.Color("#d4d4d4"),
		SyntaxNameBuiltin:     lipgloss.Color("#dcdcaa"),
		SyntaxNameTag:         lipgloss.Color("#569cd6"),
		SyntaxNameAttribute:   lipgloss.Color("#9cdcfe"),
		SyntaxNameClass:       lipgloss.Color("#4ec9b0"),
		SyntaxNameDecorator:   lipgloss.Color("#dcdcaa"),
		SyntaxLiteralString:   lipgloss.Color("#ce9178"),
	})
}

func VSCodeLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#005fb8"),
		Secondary: lipgloss.Color("#007f6e"),
		Accent:    lipgloss.Color("#8f3985"),
		Keyword:   lipgloss.Color("#8f3985"),

		FgBase:       lipgloss.Color("#242424"),
		FgSubtle:     lipgloss.Color("#3b3b3b"),
		FgMoreSubtle: lipgloss.Color("#5f5f5f"),
		FgMostSubtle: lipgloss.Color("#767676"),

		OnPrimary: lipgloss.Color("#ffffff"),

		BgBase:         lipgloss.Color("#ffffff"),
		BgLeastVisible: lipgloss.Color("#f3f3f3"),
		BgLessVisible:  lipgloss.Color("#e8e8e8"),
		BgMostVisible:  lipgloss.Color("#b8b8b8"),
		Separator:      lipgloss.Color("#b8b8b8"),

		Destructive:       lipgloss.Color("#b52020"),
		Error:             lipgloss.Color("#b52020"),
		Warning:           lipgloss.Color("#a34f00"),
		WarningSubtle:     lipgloss.Color("#786000"),
		Denied:            lipgloss.Color("#b52020"),
		Busy:              lipgloss.Color("#786000"),
		Info:              lipgloss.Color("#005fb8"),
		InfoMoreSubtle:    lipgloss.Color("#007f6e"),
		InfoMostSubtle:    lipgloss.Color("#5f5f5f"),
		Success:           lipgloss.Color("#287a3d"),
		SuccessMoreSubtle: lipgloss.Color("#007f6e"),
		SuccessMostSubtle: lipgloss.Color("#5f5f5f"),

		DiffAddFg:        lipgloss.Color("#287a3d"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#b52020"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#007f6e"),

		SyntaxLink:            lipgloss.Color("#005fb8"),
		SyntaxImage:           lipgloss.Color("#8f3985"),
		SyntaxCommentPreproc:  lipgloss.Color("#a34f00"),
		SyntaxKeywordReserved: lipgloss.Color("#8f3985"),
		SyntaxKeywordType:     lipgloss.Color("#007f6e"),
		SyntaxOperator:        lipgloss.Color("#8f3985"),
		SyntaxNameBuiltin:     lipgloss.Color("#005fb8"),
		SyntaxNameTag:         lipgloss.Color("#b52020"),
		SyntaxNameAttribute:   lipgloss.Color("#a34f00"),
		SyntaxNameClass:       lipgloss.Color("#005fb8"),
		SyntaxNameDecorator:   lipgloss.Color("#8f3985"),
		SyntaxLiteralString:   lipgloss.Color("#287a3d"),
	})
}
