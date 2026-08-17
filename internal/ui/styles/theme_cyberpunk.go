package styles

import "charm.land/lipgloss/v2"

// Cyberpunk returns the Cyberpunk theme — toxic green on black with neon
// cyan, electric yellow, and hot red accents. Adapted from cyberpunk.vim.
// https://github.com/taigrr/cyberpunk.vim
func Cyberpunk() Styles {
	return quickStyle(quickStyleOpts{
		// Brand: toxic green primary, electric yellow secondary, neon
		// cyan accent, hot red keyword.
		primary:   lipgloss.Color("#408000"),
		secondary: lipgloss.Color("#ffd302"),
		accent:    lipgloss.Color("#0eeafa"),
		keyword:   lipgloss.Color("#ff0000"),

		fgBase:       lipgloss.Color("#408000"),
		fgSubtle:     lipgloss.Color("#cdb1ad"),
		fgMoreSubtle: lipgloss.Color("#888888"),
		fgMostSubtle: lipgloss.Color("#444444"),

		onPrimary: lipgloss.Color("#000000"),

		bgBase:         lipgloss.Color("#000000"),
		bgLeastVisible: lipgloss.Color("#0a0a0a"),
		bgLessVisible:  lipgloss.Color("#1a1a1a"),
		bgMostVisible:  lipgloss.Color("#333333"),
		separator:      lipgloss.Color("#1a1a1a"),

		destructive:       lipgloss.Color("#ff0000"),
		error:             lipgloss.Color("#ff0000"),
		warning:           lipgloss.Color("#ffd302"),
		warningSubtle:     lipgloss.Color("#cdb1ad"),
		denied:            lipgloss.Color("#ff0000"),
		busy:              lipgloss.Color("#ffd302"),
		info:              lipgloss.Color("#0197dd"),
		infoMoreSubtle:    lipgloss.Color("#0eeafa"),
		infoMostSubtle:    lipgloss.Color("#0c35bf"),
		success:           lipgloss.Color("#408000"),
		successMoreSubtle: lipgloss.Color("#0eeafa"),
		successMostSubtle: lipgloss.Color("#003300"),

		// Diff colors come straight from cyberpunk.vim's DiffAdd / DiffDelete.
		diffAddFg:        lipgloss.Color("#408000"),
		diffAddBg:        lipgloss.Color("#003300"),
		diffAddBgEmph:    lipgloss.Color("#002200"),
		diffRemoveFg:     lipgloss.Color("#ff0000"),
		diffRemoveBg:     lipgloss.Color("#330000"),
		diffRemoveBgEmph: lipgloss.Color("#220000"),

		hypercredit: lipgloss.Color("#0eeafa"),

		syntaxLink:            lipgloss.Color("#0eeafa"),
		syntaxImage:           lipgloss.Color("#cdb1ad"),
		syntaxCommentPreproc:  lipgloss.Color("#0eeafa"), // PreProc
		syntaxKeywordReserved: lipgloss.Color("#ffd302"), // Statement
		syntaxKeywordType:     lipgloss.Color("#ffd302"), // Type
		syntaxOperator:        lipgloss.Color("#ff0000"), // Operator
		syntaxNameBuiltin:     lipgloss.Color("#0197dd"), // Identifier
		syntaxNameTag:         lipgloss.Color("#ffd302"),
		syntaxNameAttribute:   lipgloss.Color("#0197dd"),
		syntaxNameClass:       lipgloss.Color("#ffd302"),
		syntaxNameDecorator:   lipgloss.Color("#0eeafa"),
		syntaxLiteralString:   lipgloss.Color("#0197dd"), // Constant

		// Brand surfaces: cyan↔yellow neon glow for the header gradient,
		// green↔cyan for the working indicator.
		headerCharm:     lipgloss.Color("#0eeafa"),
		headerDiagonals: lipgloss.Color("#ffd302"),
		logoGradFrom:    lipgloss.Color("#0eeafa"),
		logoGradTo:      lipgloss.Color("#ffd302"),
		workingGradFrom: lipgloss.Color("#408000"),
		workingGradTo:   lipgloss.Color("#0eeafa"),
	})
}

func CyberpunkLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#356a00"),
		secondary: lipgloss.Color("#806900"),
		accent:    lipgloss.Color("#007784"),
		keyword:   lipgloss.Color("#b00000"),

		fgBase:       lipgloss.Color("#24331d"),
		fgSubtle:     lipgloss.Color("#3c4d34"),
		fgMoreSubtle: lipgloss.Color("#586750"),
		fgMostSubtle: lipgloss.Color("#708069"),

		onPrimary: lipgloss.Color("#f6fff2"),

		bgBase:         lipgloss.Color("#f6fff2"),
		bgLeastVisible: lipgloss.Color("#eaf6e5"),
		bgLessVisible:  lipgloss.Color("#d8ead0"),
		bgMostVisible:  lipgloss.Color("#9bb78f"),
		separator:      lipgloss.Color("#9bb78f"),

		destructive:       lipgloss.Color("#b00000"),
		error:             lipgloss.Color("#b00000"),
		warning:           lipgloss.Color("#9a4b00"),
		warningSubtle:     lipgloss.Color("#756000"),
		denied:            lipgloss.Color("#b00000"),
		busy:              lipgloss.Color("#756000"),
		info:              lipgloss.Color("#006d9e"),
		infoMoreSubtle:    lipgloss.Color("#007784"),
		infoMostSubtle:    lipgloss.Color("#586750"),
		success:           lipgloss.Color("#356a00"),
		successMoreSubtle: lipgloss.Color("#007784"),
		successMostSubtle: lipgloss.Color("#586750"),

		diffAddFg:        lipgloss.Color("#356a00"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#b00000"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#806900"),

		syntaxLink:            lipgloss.Color("#006d9e"),
		syntaxImage:           lipgloss.Color("#6f42a5"),
		syntaxCommentPreproc:  lipgloss.Color("#9a4b00"),
		syntaxKeywordReserved: lipgloss.Color("#6f42a5"),
		syntaxKeywordType:     lipgloss.Color("#007784"),
		syntaxOperator:        lipgloss.Color("#b00000"),
		syntaxNameBuiltin:     lipgloss.Color("#006d9e"),
		syntaxNameTag:         lipgloss.Color("#b00000"),
		syntaxNameAttribute:   lipgloss.Color("#9a4b00"),
		syntaxNameClass:       lipgloss.Color("#006d9e"),
		syntaxNameDecorator:   lipgloss.Color("#6f42a5"),
		syntaxLiteralString:   lipgloss.Color("#356a00"),
	})
}
