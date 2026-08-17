package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// Dracula returns the Dracula theme.
// https://draculatheme.com
func Dracula() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#bd93f9"), // purple
		Secondary: lipgloss.Color("#ff79c6"), // pink
		Accent:    lipgloss.Color("#50fa7b"), // green
		Keyword:   lipgloss.Color("#ff79c6"),

		FgBase:       lipgloss.Color("#f8f8f2"),
		FgSubtle:     lipgloss.Color("#bfbfbb"),
		FgMoreSubtle: lipgloss.Color("#8b8d99"),
		FgMostSubtle: lipgloss.Color("#6272a4"),

		OnPrimary: lipgloss.Color("#282a36"),

		BgBase:         lipgloss.Color("#282a36"),
		BgLeastVisible: lipgloss.Color("#21222c"),
		BgLessVisible:  lipgloss.Color("#343746"),
		BgMostVisible:  lipgloss.Color("#44475a"),
		Separator:      lipgloss.Color("#44475a"),

		Destructive:       lipgloss.Color("#ff5555"),
		Error:             lipgloss.Color("#ff5555"),
		Warning:           lipgloss.Color("#ffb86c"),
		WarningSubtle:     lipgloss.Color("#f1fa8c"),
		Denied:            lipgloss.Color("#ff5555"),
		Busy:              lipgloss.Color("#f1fa8c"),
		Info:              lipgloss.Color("#8be9fd"),
		InfoMoreSubtle:    lipgloss.Color("#6272a4"),
		InfoMostSubtle:    lipgloss.Color("#44475a"),
		Success:           lipgloss.Color("#50fa7b"),
		SuccessMoreSubtle: lipgloss.Color("#8be9fd"),
		SuccessMostSubtle: lipgloss.Color("#6272a4"),

		DiffAddFg:        lipgloss.Color("#50fa7b"),
		DiffAddBg:        lipgloss.Color("#2a3a32"),
		DiffAddBgEmph:    lipgloss.Color("#22302a"),
		DiffRemoveFg:     lipgloss.Color("#ff5555"),
		DiffRemoveBg:     lipgloss.Color("#3a2a2c"),
		DiffRemoveBgEmph: lipgloss.Color("#302224"),

		Hypercredit: lipgloss.Color("#ff79c6"),

		SyntaxLink:            lipgloss.Color("#8be9fd"),
		SyntaxImage:           lipgloss.Color("#ff79c6"),
		SyntaxCommentPreproc:  lipgloss.Color("#ffb86c"),
		SyntaxKeywordReserved: lipgloss.Color("#ff79c6"),
		SyntaxKeywordType:     lipgloss.Color("#8be9fd"),
		SyntaxOperator:        lipgloss.Color("#ff79c6"),
		SyntaxNameBuiltin:     lipgloss.Color("#bd93f9"),
		SyntaxNameTag:         lipgloss.Color("#ff79c6"),
		SyntaxNameAttribute:   lipgloss.Color("#50fa7b"),
		SyntaxNameClass:       lipgloss.Color("#50fa7b"),
		SyntaxNameDecorator:   lipgloss.Color("#50fa7b"),
		SyntaxLiteralString:   lipgloss.Color("#f1fa8c"),
	})
}

func DraculaLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#6f42c1"),
		Secondary: lipgloss.Color("#a71972"),
		Accent:    lipgloss.Color("#287a3d"),
		Keyword:   lipgloss.Color("#a71972"),

		FgBase:       lipgloss.Color("#282a36"),
		FgSubtle:     lipgloss.Color("#44475a"),
		FgMoreSubtle: lipgloss.Color("#5f6170"),
		FgMostSubtle: lipgloss.Color("#747789"),

		OnPrimary: lipgloss.Color("#f8f8f2"),

		BgBase:         lipgloss.Color("#f8f8f2"),
		BgLeastVisible: lipgloss.Color("#eeeeea"),
		BgLessVisible:  lipgloss.Color("#e2e2dc"),
		BgMostVisible:  lipgloss.Color("#aaaab0"),
		Separator:      lipgloss.Color("#aaaab0"),

		Destructive:       lipgloss.Color("#b4232f"),
		Error:             lipgloss.Color("#b4232f"),
		Warning:           lipgloss.Color("#a34f00"),
		WarningSubtle:     lipgloss.Color("#806c00"),
		Denied:            lipgloss.Color("#b4232f"),
		Busy:              lipgloss.Color("#806c00"),
		Info:              lipgloss.Color("#1769aa"),
		InfoMoreSubtle:    lipgloss.Color("#087f8c"),
		InfoMostSubtle:    lipgloss.Color("#5f6170"),
		Success:           lipgloss.Color("#287a3d"),
		SuccessMoreSubtle: lipgloss.Color("#087f8c"),
		SuccessMostSubtle: lipgloss.Color("#5f6170"),

		DiffAddFg:        lipgloss.Color("#287a3d"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#b4232f"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#a71972"),

		SyntaxLink:            lipgloss.Color("#1769aa"),
		SyntaxImage:           lipgloss.Color("#6f42c1"),
		SyntaxCommentPreproc:  lipgloss.Color("#a34f00"),
		SyntaxKeywordReserved: lipgloss.Color("#6f42c1"),
		SyntaxKeywordType:     lipgloss.Color("#087f8c"),
		SyntaxOperator:        lipgloss.Color("#a71972"),
		SyntaxNameBuiltin:     lipgloss.Color("#1769aa"),
		SyntaxNameTag:         lipgloss.Color("#b4232f"),
		SyntaxNameAttribute:   lipgloss.Color("#a34f00"),
		SyntaxNameClass:       lipgloss.Color("#1769aa"),
		SyntaxNameDecorator:   lipgloss.Color("#6f42c1"),
		SyntaxLiteralString:   lipgloss.Color("#287a3d"),
	})
}
