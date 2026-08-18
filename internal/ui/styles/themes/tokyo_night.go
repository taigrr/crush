package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// TokyoNight returns the Tokyo Night dark theme by enkia.
// https://github.com/folke/tokyonight.nvim
func TokyoNight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		// Brand: blue (primary), purple (secondary), green accent, pink keyword.
		Primary:   lipgloss.Color("#7aa2f7"),
		Secondary: lipgloss.Color("#bb9af7"),
		Accent:    lipgloss.Color("#9ece6a"),
		Keyword:   lipgloss.Color("#f7768e"),

		FgBase:       lipgloss.Color("#c0caf5"),
		FgSubtle:     lipgloss.Color("#a9b1d6"),
		FgMoreSubtle: lipgloss.Color("#737aa2"),
		FgMostSubtle: lipgloss.Color("#565f89"),

		OnPrimary: lipgloss.Color("#1a1b26"),

		BgBase:         lipgloss.Color("#1a1b26"),
		BgLeastVisible: lipgloss.Color("#16161e"),
		BgLessVisible:  lipgloss.Color("#24283b"),
		BgMostVisible:  lipgloss.Color("#292e42"),
		Separator:      lipgloss.Color("#292e42"),

		Destructive:       lipgloss.Color("#f7768e"),
		Error:             lipgloss.Color("#db4b4b"),
		Warning:           lipgloss.Color("#e0af68"),
		WarningSubtle:     lipgloss.Color("#cfc9c2"),
		Denied:            lipgloss.Color("#ff9e64"),
		Busy:              lipgloss.Color("#e0af68"),
		Info:              lipgloss.Color("#7dcfff"),
		InfoMoreSubtle:    lipgloss.Color("#7aa2f7"),
		InfoMostSubtle:    lipgloss.Color("#3d59a1"),
		Success:           lipgloss.Color("#9ece6a"),
		SuccessMoreSubtle: lipgloss.Color("#73daca"),
		SuccessMostSubtle: lipgloss.Color("#41a6b5"),

		DiffAddFg:        lipgloss.Color("#9ece6a"),
		DiffAddBg:        lipgloss.Color("#20303b"),
		DiffAddBgEmph:    lipgloss.Color("#1a2530"),
		DiffRemoveFg:     lipgloss.Color("#f7768e"),
		DiffRemoveBg:     lipgloss.Color("#37222c"),
		DiffRemoveBgEmph: lipgloss.Color("#2c1c24"),

		Hypercredit: lipgloss.Color("#bb9af7"),

		SyntaxLink:            lipgloss.Color("#7dcfff"),
		SyntaxImage:           lipgloss.Color("#bb9af7"),
		SyntaxCommentPreproc:  lipgloss.Color("#e0af68"),
		SyntaxKeywordReserved: lipgloss.Color("#bb9af7"),
		SyntaxKeywordType:     lipgloss.Color("#2ac3de"),
		SyntaxOperator:        lipgloss.Color("#89ddff"),
		SyntaxNameBuiltin:     lipgloss.Color("#f7768e"),
		SyntaxNameTag:         lipgloss.Color("#f7768e"),
		SyntaxNameAttribute:   lipgloss.Color("#e0af68"),
		SyntaxNameClass:       lipgloss.Color("#7aa2f7"),
		SyntaxNameDecorator:   lipgloss.Color("#7dcfff"),
		SyntaxLiteralString:   lipgloss.Color("#9ece6a"),
	})
}

func TokyoNightLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#34548a"),
		Secondary: lipgloss.Color("#5a4a78"),
		Accent:    lipgloss.Color("#33635c"),
		Keyword:   lipgloss.Color("#8c4351"),

		FgBase:       lipgloss.Color("#343b58"),
		FgSubtle:     lipgloss.Color("#4c505e"),
		FgMoreSubtle: lipgloss.Color("#68709a"),
		FgMostSubtle: lipgloss.Color("#777c99"),

		OnPrimary: lipgloss.Color("#e6e7ed"),

		BgBase:         lipgloss.Color("#e6e7ed"),
		BgLeastVisible: lipgloss.Color("#dcdfe7"),
		BgLessVisible:  lipgloss.Color("#cfd2dc"),
		BgMostVisible:  lipgloss.Color("#a8aec1"),
		Separator:      lipgloss.Color("#a8aec1"),

		Destructive:       lipgloss.Color("#8c4351"),
		Error:             lipgloss.Color("#8c4351"),
		Warning:           lipgloss.Color("#965027"),
		WarningSubtle:     lipgloss.Color("#8f5e15"),
		Denied:            lipgloss.Color("#8c4351"),
		Busy:              lipgloss.Color("#8f5e15"),
		Info:              lipgloss.Color("#34548a"),
		InfoMoreSubtle:    lipgloss.Color("#0f4b6e"),
		InfoMostSubtle:    lipgloss.Color("#68709a"),
		Success:           lipgloss.Color("#485e30"),
		SuccessMoreSubtle: lipgloss.Color("#0f4b6e"),
		SuccessMostSubtle: lipgloss.Color("#68709a"),

		DiffAddFg:        lipgloss.Color("#485e30"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#8c4351"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#5a4a78"),

		SyntaxLink:            lipgloss.Color("#34548a"),
		SyntaxImage:           lipgloss.Color("#5a4a78"),
		SyntaxCommentPreproc:  lipgloss.Color("#965027"),
		SyntaxKeywordReserved: lipgloss.Color("#5a4a78"),
		SyntaxKeywordType:     lipgloss.Color("#0f4b6e"),
		SyntaxOperator:        lipgloss.Color("#8c4351"),
		SyntaxNameBuiltin:     lipgloss.Color("#34548a"),
		SyntaxNameTag:         lipgloss.Color("#8c4351"),
		SyntaxNameAttribute:   lipgloss.Color("#965027"),
		SyntaxNameClass:       lipgloss.Color("#34548a"),
		SyntaxNameDecorator:   lipgloss.Color("#5a4a78"),
		SyntaxLiteralString:   lipgloss.Color("#485e30"),
	})
}
