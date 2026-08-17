package styles

import "charm.land/lipgloss/v2"

// TokyoNight returns the Tokyo Night dark theme by enkia.
// https://github.com/folke/tokyonight.nvim
func TokyoNight() Styles {
	return quickStyle(quickStyleOpts{
		// Brand: blue (primary), purple (secondary), green accent, pink keyword.
		primary:   lipgloss.Color("#7aa2f7"),
		secondary: lipgloss.Color("#bb9af7"),
		accent:    lipgloss.Color("#9ece6a"),
		keyword:   lipgloss.Color("#f7768e"),

		fgBase:       lipgloss.Color("#c0caf5"),
		fgSubtle:     lipgloss.Color("#a9b1d6"),
		fgMoreSubtle: lipgloss.Color("#737aa2"),
		fgMostSubtle: lipgloss.Color("#565f89"),

		onPrimary: lipgloss.Color("#1a1b26"),

		bgBase:         lipgloss.Color("#1a1b26"),
		bgLeastVisible: lipgloss.Color("#16161e"),
		bgLessVisible:  lipgloss.Color("#24283b"),
		bgMostVisible:  lipgloss.Color("#292e42"),
		separator:      lipgloss.Color("#292e42"),

		destructive:       lipgloss.Color("#f7768e"),
		error:             lipgloss.Color("#db4b4b"),
		warning:           lipgloss.Color("#e0af68"),
		warningSubtle:     lipgloss.Color("#cfc9c2"),
		denied:            lipgloss.Color("#ff9e64"),
		busy:              lipgloss.Color("#e0af68"),
		info:              lipgloss.Color("#7dcfff"),
		infoMoreSubtle:    lipgloss.Color("#7aa2f7"),
		infoMostSubtle:    lipgloss.Color("#3d59a1"),
		success:           lipgloss.Color("#9ece6a"),
		successMoreSubtle: lipgloss.Color("#73daca"),
		successMostSubtle: lipgloss.Color("#41a6b5"),

		diffAddFg:        lipgloss.Color("#9ece6a"),
		diffAddBg:        lipgloss.Color("#20303b"),
		diffAddBgEmph:    lipgloss.Color("#1a2530"),
		diffRemoveFg:     lipgloss.Color("#f7768e"),
		diffRemoveBg:     lipgloss.Color("#37222c"),
		diffRemoveBgEmph: lipgloss.Color("#2c1c24"),

		hypercredit: lipgloss.Color("#bb9af7"),

		syntaxLink:            lipgloss.Color("#7dcfff"),
		syntaxImage:           lipgloss.Color("#bb9af7"),
		syntaxCommentPreproc:  lipgloss.Color("#e0af68"),
		syntaxKeywordReserved: lipgloss.Color("#bb9af7"),
		syntaxKeywordType:     lipgloss.Color("#2ac3de"),
		syntaxOperator:        lipgloss.Color("#89ddff"),
		syntaxNameBuiltin:     lipgloss.Color("#f7768e"),
		syntaxNameTag:         lipgloss.Color("#f7768e"),
		syntaxNameAttribute:   lipgloss.Color("#e0af68"),
		syntaxNameClass:       lipgloss.Color("#7aa2f7"),
		syntaxNameDecorator:   lipgloss.Color("#7dcfff"),
		syntaxLiteralString:   lipgloss.Color("#9ece6a"),
	})
}

func TokyoNightLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#34548a"),
		secondary: lipgloss.Color("#5a4a78"),
		accent:    lipgloss.Color("#33635c"),
		keyword:   lipgloss.Color("#8c4351"),

		fgBase:       lipgloss.Color("#343b58"),
		fgSubtle:     lipgloss.Color("#4c505e"),
		fgMoreSubtle: lipgloss.Color("#68709a"),
		fgMostSubtle: lipgloss.Color("#777c99"),

		onPrimary: lipgloss.Color("#e6e7ed"),

		bgBase:         lipgloss.Color("#e6e7ed"),
		bgLeastVisible: lipgloss.Color("#dcdfe7"),
		bgLessVisible:  lipgloss.Color("#cfd2dc"),
		bgMostVisible:  lipgloss.Color("#a8aec1"),
		separator:      lipgloss.Color("#a8aec1"),

		destructive:       lipgloss.Color("#8c4351"),
		error:             lipgloss.Color("#8c4351"),
		warning:           lipgloss.Color("#965027"),
		warningSubtle:     lipgloss.Color("#8f5e15"),
		denied:            lipgloss.Color("#8c4351"),
		busy:              lipgloss.Color("#8f5e15"),
		info:              lipgloss.Color("#34548a"),
		infoMoreSubtle:    lipgloss.Color("#0f4b6e"),
		infoMostSubtle:    lipgloss.Color("#68709a"),
		success:           lipgloss.Color("#485e30"),
		successMoreSubtle: lipgloss.Color("#0f4b6e"),
		successMostSubtle: lipgloss.Color("#68709a"),

		diffAddFg:        lipgloss.Color("#485e30"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#8c4351"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#5a4a78"),

		syntaxLink:            lipgloss.Color("#34548a"),
		syntaxImage:           lipgloss.Color("#5a4a78"),
		syntaxCommentPreproc:  lipgloss.Color("#965027"),
		syntaxKeywordReserved: lipgloss.Color("#5a4a78"),
		syntaxKeywordType:     lipgloss.Color("#0f4b6e"),
		syntaxOperator:        lipgloss.Color("#8c4351"),
		syntaxNameBuiltin:     lipgloss.Color("#34548a"),
		syntaxNameTag:         lipgloss.Color("#8c4351"),
		syntaxNameAttribute:   lipgloss.Color("#965027"),
		syntaxNameClass:       lipgloss.Color("#34548a"),
		syntaxNameDecorator:   lipgloss.Color("#5a4a78"),
		syntaxLiteralString:   lipgloss.Color("#485e30"),
	})
}
