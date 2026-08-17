package styles

import "charm.land/lipgloss/v2"

// RosePine returns the Rose Pine "main" dark theme.
// https://rosepinetheme.com
func RosePine() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#c4a7e7"), // iris
		secondary: lipgloss.Color("#ebbcba"), // rose
		accent:    lipgloss.Color("#9ccfd8"), // foam
		keyword:   lipgloss.Color("#eb6f92"), // love

		fgBase:       lipgloss.Color("#e0def4"), // text
		fgSubtle:     lipgloss.Color("#a8a4b8"),
		fgMoreSubtle: lipgloss.Color("#908caa"), // subtle
		fgMostSubtle: lipgloss.Color("#6e6a86"), // muted

		onPrimary: lipgloss.Color("#191724"),

		bgBase:         lipgloss.Color("#191724"), // base
		bgLeastVisible: lipgloss.Color("#1f1d2e"), // surface
		bgLessVisible:  lipgloss.Color("#26233a"), // overlay
		bgMostVisible:  lipgloss.Color("#403d52"), // highlight high
		separator:      lipgloss.Color("#26233a"),

		destructive:       lipgloss.Color("#eb6f92"),
		error:             lipgloss.Color("#eb6f92"),
		warning:           lipgloss.Color("#f6c177"), // gold
		warningSubtle:     lipgloss.Color("#ebbcba"),
		denied:            lipgloss.Color("#eb6f92"),
		busy:              lipgloss.Color("#f6c177"),
		info:              lipgloss.Color("#9ccfd8"),
		infoMoreSubtle:    lipgloss.Color("#31748f"), // pine
		infoMostSubtle:    lipgloss.Color("#403d52"),
		success:           lipgloss.Color("#9ccfd8"),
		successMoreSubtle: lipgloss.Color("#c4a7e7"),
		successMostSubtle: lipgloss.Color("#31748f"),

		diffAddFg:        lipgloss.Color("#9ccfd8"),
		diffAddBg:        lipgloss.Color("#21303a"),
		diffAddBgEmph:    lipgloss.Color("#1c2832"),
		diffRemoveFg:     lipgloss.Color("#eb6f92"),
		diffRemoveBg:     lipgloss.Color("#37252f"),
		diffRemoveBgEmph: lipgloss.Color("#2e1f28"),

		hypercredit: lipgloss.Color("#f6c177"),

		syntaxLink:            lipgloss.Color("#9ccfd8"),
		syntaxImage:           lipgloss.Color("#c4a7e7"),
		syntaxCommentPreproc:  lipgloss.Color("#f6c177"),
		syntaxKeywordReserved: lipgloss.Color("#c4a7e7"),
		syntaxKeywordType:     lipgloss.Color("#9ccfd8"),
		syntaxOperator:        lipgloss.Color("#31748f"),
		syntaxNameBuiltin:     lipgloss.Color("#ebbcba"),
		syntaxNameTag:         lipgloss.Color("#c4a7e7"),
		syntaxNameAttribute:   lipgloss.Color("#f6c177"),
		syntaxNameClass:       lipgloss.Color("#ebbcba"),
		syntaxNameDecorator:   lipgloss.Color("#f6c177"),
		syntaxLiteralString:   lipgloss.Color("#9ccfd8"),
	})
}

func RosePineDawn() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#575279"),
		secondary: lipgloss.Color("#d7827e"),
		accent:    lipgloss.Color("#286983"),
		keyword:   lipgloss.Color("#b4637a"),

		fgBase:       lipgloss.Color("#575279"),
		fgSubtle:     lipgloss.Color("#6e6a86"),
		fgMoreSubtle: lipgloss.Color("#797593"),
		fgMostSubtle: lipgloss.Color("#8f899f"),

		onPrimary: lipgloss.Color("#faf4ed"),

		bgBase:         lipgloss.Color("#faf4ed"),
		bgLeastVisible: lipgloss.Color("#fffaf3"),
		bgLessVisible:  lipgloss.Color("#f2e9de"),
		bgMostVisible:  lipgloss.Color("#c4b8aa"),
		separator:      lipgloss.Color("#c4b8aa"),

		destructive:       lipgloss.Color("#b4637a"),
		error:             lipgloss.Color("#b4637a"),
		warning:           lipgloss.Color("#a15d30"),
		warningSubtle:     lipgloss.Color("#806423"),
		denied:            lipgloss.Color("#b4637a"),
		busy:              lipgloss.Color("#806423"),
		info:              lipgloss.Color("#286983"),
		infoMoreSubtle:    lipgloss.Color("#287080"),
		infoMostSubtle:    lipgloss.Color("#797593"),
		success:           lipgloss.Color("#47765b"),
		successMoreSubtle: lipgloss.Color("#287080"),
		successMostSubtle: lipgloss.Color("#797593"),

		diffAddFg:        lipgloss.Color("#47765b"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#b4637a"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#d7827e"),

		syntaxLink:            lipgloss.Color("#286983"),
		syntaxImage:           lipgloss.Color("#575279"),
		syntaxCommentPreproc:  lipgloss.Color("#a15d30"),
		syntaxKeywordReserved: lipgloss.Color("#575279"),
		syntaxKeywordType:     lipgloss.Color("#287080"),
		syntaxOperator:        lipgloss.Color("#b4637a"),
		syntaxNameBuiltin:     lipgloss.Color("#286983"),
		syntaxNameTag:         lipgloss.Color("#b4637a"),
		syntaxNameAttribute:   lipgloss.Color("#a15d30"),
		syntaxNameClass:       lipgloss.Color("#286983"),
		syntaxNameDecorator:   lipgloss.Color("#575279"),
		syntaxLiteralString:   lipgloss.Color("#47765b"),
	})
}
