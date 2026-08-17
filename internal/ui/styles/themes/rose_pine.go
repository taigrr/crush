package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// RosePine returns the Rose Pine "main" dark theme.
// https://rosepinetheme.com
func RosePine() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#c4a7e7"), // iris
		Secondary: lipgloss.Color("#ebbcba"), // rose
		Accent:    lipgloss.Color("#9ccfd8"), // foam
		Keyword:   lipgloss.Color("#eb6f92"), // love

		FgBase:       lipgloss.Color("#e0def4"), // text
		FgSubtle:     lipgloss.Color("#a8a4b8"),
		FgMoreSubtle: lipgloss.Color("#908caa"), // subtle
		FgMostSubtle: lipgloss.Color("#6e6a86"), // muted

		OnPrimary: lipgloss.Color("#191724"),

		BgBase:         lipgloss.Color("#191724"), // base
		BgLeastVisible: lipgloss.Color("#1f1d2e"), // surface
		BgLessVisible:  lipgloss.Color("#26233a"), // overlay
		BgMostVisible:  lipgloss.Color("#403d52"), // highlight high
		Separator:      lipgloss.Color("#26233a"),

		Destructive:       lipgloss.Color("#eb6f92"),
		Error:             lipgloss.Color("#eb6f92"),
		Warning:           lipgloss.Color("#f6c177"), // gold
		WarningSubtle:     lipgloss.Color("#ebbcba"),
		Denied:            lipgloss.Color("#eb6f92"),
		Busy:              lipgloss.Color("#f6c177"),
		Info:              lipgloss.Color("#9ccfd8"),
		InfoMoreSubtle:    lipgloss.Color("#31748f"), // pine
		InfoMostSubtle:    lipgloss.Color("#403d52"),
		Success:           lipgloss.Color("#9ccfd8"),
		SuccessMoreSubtle: lipgloss.Color("#c4a7e7"),
		SuccessMostSubtle: lipgloss.Color("#31748f"),

		DiffAddFg:        lipgloss.Color("#9ccfd8"),
		DiffAddBg:        lipgloss.Color("#21303a"),
		DiffAddBgEmph:    lipgloss.Color("#1c2832"),
		DiffRemoveFg:     lipgloss.Color("#eb6f92"),
		DiffRemoveBg:     lipgloss.Color("#37252f"),
		DiffRemoveBgEmph: lipgloss.Color("#2e1f28"),

		Hypercredit: lipgloss.Color("#f6c177"),

		SyntaxLink:            lipgloss.Color("#9ccfd8"),
		SyntaxImage:           lipgloss.Color("#c4a7e7"),
		SyntaxCommentPreproc:  lipgloss.Color("#f6c177"),
		SyntaxKeywordReserved: lipgloss.Color("#c4a7e7"),
		SyntaxKeywordType:     lipgloss.Color("#9ccfd8"),
		SyntaxOperator:        lipgloss.Color("#31748f"),
		SyntaxNameBuiltin:     lipgloss.Color("#ebbcba"),
		SyntaxNameTag:         lipgloss.Color("#c4a7e7"),
		SyntaxNameAttribute:   lipgloss.Color("#f6c177"),
		SyntaxNameClass:       lipgloss.Color("#ebbcba"),
		SyntaxNameDecorator:   lipgloss.Color("#f6c177"),
		SyntaxLiteralString:   lipgloss.Color("#9ccfd8"),
	})
}

func RosePineDawn() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#575279"),
		Secondary: lipgloss.Color("#d7827e"),
		Accent:    lipgloss.Color("#286983"),
		Keyword:   lipgloss.Color("#b4637a"),

		FgBase:       lipgloss.Color("#575279"),
		FgSubtle:     lipgloss.Color("#6e6a86"),
		FgMoreSubtle: lipgloss.Color("#797593"),
		FgMostSubtle: lipgloss.Color("#8f899f"),

		OnPrimary: lipgloss.Color("#faf4ed"),

		BgBase:         lipgloss.Color("#faf4ed"),
		BgLeastVisible: lipgloss.Color("#fffaf3"),
		BgLessVisible:  lipgloss.Color("#f2e9de"),
		BgMostVisible:  lipgloss.Color("#c4b8aa"),
		Separator:      lipgloss.Color("#c4b8aa"),

		Destructive:       lipgloss.Color("#b4637a"),
		Error:             lipgloss.Color("#b4637a"),
		Warning:           lipgloss.Color("#a15d30"),
		WarningSubtle:     lipgloss.Color("#806423"),
		Denied:            lipgloss.Color("#b4637a"),
		Busy:              lipgloss.Color("#806423"),
		Info:              lipgloss.Color("#286983"),
		InfoMoreSubtle:    lipgloss.Color("#287080"),
		InfoMostSubtle:    lipgloss.Color("#797593"),
		Success:           lipgloss.Color("#47765b"),
		SuccessMoreSubtle: lipgloss.Color("#287080"),
		SuccessMostSubtle: lipgloss.Color("#797593"),

		DiffAddFg:        lipgloss.Color("#47765b"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#b4637a"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#d7827e"),

		SyntaxLink:            lipgloss.Color("#286983"),
		SyntaxImage:           lipgloss.Color("#575279"),
		SyntaxCommentPreproc:  lipgloss.Color("#a15d30"),
		SyntaxKeywordReserved: lipgloss.Color("#575279"),
		SyntaxKeywordType:     lipgloss.Color("#287080"),
		SyntaxOperator:        lipgloss.Color("#b4637a"),
		SyntaxNameBuiltin:     lipgloss.Color("#286983"),
		SyntaxNameTag:         lipgloss.Color("#b4637a"),
		SyntaxNameAttribute:   lipgloss.Color("#a15d30"),
		SyntaxNameClass:       lipgloss.Color("#286983"),
		SyntaxNameDecorator:   lipgloss.Color("#575279"),
		SyntaxLiteralString:   lipgloss.Color("#47765b"),
	})
}
