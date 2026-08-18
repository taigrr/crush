package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// Cyberpunk returns the Cyberpunk theme — toxic green on black with neon
// cyan, electric yellow, and hot red accents. Adapted from cyberpunk.vim.
// https://github.com/taigrr/cyberpunk.vim
func Cyberpunk() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		// Brand: toxic green primary, electric yellow secondary, neon
		// cyan accent, hot red keyword.
		Primary:   lipgloss.Color("#408000"),
		Secondary: lipgloss.Color("#ffd302"),
		Accent:    lipgloss.Color("#0eeafa"),
		Keyword:   lipgloss.Color("#ff0000"),

		FgBase:       lipgloss.Color("#408000"),
		FgSubtle:     lipgloss.Color("#cdb1ad"),
		FgMoreSubtle: lipgloss.Color("#888888"),
		FgMostSubtle: lipgloss.Color("#444444"),

		OnPrimary: lipgloss.Color("#000000"),

		BgBase:         lipgloss.Color("#000000"),
		BgLeastVisible: lipgloss.Color("#0a0a0a"),
		BgLessVisible:  lipgloss.Color("#1a1a1a"),
		BgMostVisible:  lipgloss.Color("#333333"),
		Separator:      lipgloss.Color("#1a1a1a"),

		Destructive:       lipgloss.Color("#ff0000"),
		Error:             lipgloss.Color("#ff0000"),
		Warning:           lipgloss.Color("#ffd302"),
		WarningSubtle:     lipgloss.Color("#cdb1ad"),
		Denied:            lipgloss.Color("#ff0000"),
		Busy:              lipgloss.Color("#ffd302"),
		Info:              lipgloss.Color("#0197dd"),
		InfoMoreSubtle:    lipgloss.Color("#0eeafa"),
		InfoMostSubtle:    lipgloss.Color("#0c35bf"),
		Success:           lipgloss.Color("#408000"),
		SuccessMoreSubtle: lipgloss.Color("#0eeafa"),
		SuccessMostSubtle: lipgloss.Color("#003300"),

		// Diff colors come straight from cyberpunk.vim's DiffAdd / DiffDelete.
		DiffAddFg:        lipgloss.Color("#408000"),
		DiffAddBg:        lipgloss.Color("#003300"),
		DiffAddBgEmph:    lipgloss.Color("#002200"),
		DiffRemoveFg:     lipgloss.Color("#ff0000"),
		DiffRemoveBg:     lipgloss.Color("#330000"),
		DiffRemoveBgEmph: lipgloss.Color("#220000"),

		Hypercredit: lipgloss.Color("#0eeafa"),

		SyntaxLink:            lipgloss.Color("#0eeafa"),
		SyntaxImage:           lipgloss.Color("#cdb1ad"),
		SyntaxCommentPreproc:  lipgloss.Color("#0eeafa"), // PreProc
		SyntaxKeywordReserved: lipgloss.Color("#ffd302"), // Statement
		SyntaxKeywordType:     lipgloss.Color("#ffd302"), // Type
		SyntaxOperator:        lipgloss.Color("#ff0000"), // Operator
		SyntaxNameBuiltin:     lipgloss.Color("#0197dd"), // Identifier
		SyntaxNameTag:         lipgloss.Color("#ffd302"),
		SyntaxNameAttribute:   lipgloss.Color("#0197dd"),
		SyntaxNameClass:       lipgloss.Color("#ffd302"),
		SyntaxNameDecorator:   lipgloss.Color("#0eeafa"),
		SyntaxLiteralString:   lipgloss.Color("#0197dd"), // Constant

		// Brand surfaces: cyan↔yellow neon glow for the header gradient,
		// green↔cyan for the working indicator.
		HeaderCharm:     lipgloss.Color("#0eeafa"),
		HeaderDiagonals: lipgloss.Color("#ffd302"),
		LogoGradFrom:    lipgloss.Color("#0eeafa"),
		LogoGradTo:      lipgloss.Color("#ffd302"),
		WorkingGradFrom: lipgloss.Color("#408000"),
		WorkingGradTo:   lipgloss.Color("#0eeafa"),
	})
}

func CyberpunkLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#356a00"),
		Secondary: lipgloss.Color("#806900"),
		Accent:    lipgloss.Color("#007784"),
		Keyword:   lipgloss.Color("#b00000"),

		FgBase:       lipgloss.Color("#24331d"),
		FgSubtle:     lipgloss.Color("#3c4d34"),
		FgMoreSubtle: lipgloss.Color("#586750"),
		FgMostSubtle: lipgloss.Color("#708069"),

		OnPrimary: lipgloss.Color("#f6fff2"),

		BgBase:         lipgloss.Color("#f6fff2"),
		BgLeastVisible: lipgloss.Color("#eaf6e5"),
		BgLessVisible:  lipgloss.Color("#d8ead0"),
		BgMostVisible:  lipgloss.Color("#9bb78f"),
		Separator:      lipgloss.Color("#9bb78f"),

		Destructive:       lipgloss.Color("#b00000"),
		Error:             lipgloss.Color("#b00000"),
		Warning:           lipgloss.Color("#9a4b00"),
		WarningSubtle:     lipgloss.Color("#756000"),
		Denied:            lipgloss.Color("#b00000"),
		Busy:              lipgloss.Color("#756000"),
		Info:              lipgloss.Color("#006d9e"),
		InfoMoreSubtle:    lipgloss.Color("#007784"),
		InfoMostSubtle:    lipgloss.Color("#586750"),
		Success:           lipgloss.Color("#356a00"),
		SuccessMoreSubtle: lipgloss.Color("#007784"),
		SuccessMostSubtle: lipgloss.Color("#586750"),

		DiffAddFg:        lipgloss.Color("#356a00"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#b00000"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#806900"),

		SyntaxLink:            lipgloss.Color("#006d9e"),
		SyntaxImage:           lipgloss.Color("#6f42a5"),
		SyntaxCommentPreproc:  lipgloss.Color("#9a4b00"),
		SyntaxKeywordReserved: lipgloss.Color("#6f42a5"),
		SyntaxKeywordType:     lipgloss.Color("#007784"),
		SyntaxOperator:        lipgloss.Color("#b00000"),
		SyntaxNameBuiltin:     lipgloss.Color("#006d9e"),
		SyntaxNameTag:         lipgloss.Color("#b00000"),
		SyntaxNameAttribute:   lipgloss.Color("#9a4b00"),
		SyntaxNameClass:       lipgloss.Color("#006d9e"),
		SyntaxNameDecorator:   lipgloss.Color("#6f42a5"),
		SyntaxLiteralString:   lipgloss.Color("#356a00"),
	})
}
