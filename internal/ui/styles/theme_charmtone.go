package styles

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// CharmtonePantera returns the Charmtone dark theme. It's the default style
// for the UI.
func CharmtonePantera() Styles {
	return quickStyle(quickStyleOpts{
		primary:   charmtone.Charple,
		secondary: charmtone.Dolly,
		accent:    charmtone.Bok,
		keyword:   charmtone.Blush,

		fgBase:       charmtone.Sash,
		fgMoreSubtle: charmtone.Squid,
		fgSubtle:     charmtone.Smoke,
		fgMostSubtle: charmtone.Oyster,

		onPrimary: charmtone.Butter,

		bgBase:         charmtone.Pepper,
		bgLeastVisible: charmtone.BBQ,
		bgLessVisible:  charmtone.Char,
		bgMostVisible:  charmtone.Iron,

		separator: charmtone.Char,

		destructive:       charmtone.Coral,
		error:             charmtone.Sriracha,
		warningSubtle:     charmtone.Zest,
		warning:           charmtone.Mustard,
		denied:            charmtone.Tang,
		busy:              charmtone.Citron,
		info:              charmtone.Malibu,
		infoMoreSubtle:    charmtone.Sardine,
		infoMostSubtle:    charmtone.Damson,
		success:           charmtone.Julep,
		successMoreSubtle: charmtone.Bok,
		successMostSubtle: charmtone.Guac,

		diffAddFg:        lipgloss.Color("#629657"),
		diffAddBg:        lipgloss.Color("#323931"),
		diffAddBgEmph:    lipgloss.Color("#2b322a"),
		diffRemoveFg:     lipgloss.Color("#a45c59"),
		diffRemoveBg:     lipgloss.Color("#383030"),
		diffRemoveBgEmph: lipgloss.Color("#312929"),

		hypercredit: charmtone.Dolly,

		syntaxLink:            charmtone.Zinc,
		syntaxImage:           charmtone.Cheeky,
		syntaxCommentPreproc:  charmtone.Bengal,
		syntaxKeywordReserved: charmtone.Pony,
		syntaxKeywordType:     charmtone.Guppy,
		syntaxOperator:        charmtone.Salmon,
		syntaxNameBuiltin:     charmtone.Cheeky,
		syntaxNameTag:         charmtone.Mauve,
		syntaxNameAttribute:   charmtone.Hazy,
		syntaxNameClass:       charmtone.Salt,
		syntaxNameDecorator:   charmtone.Citron,
		syntaxLiteralString:   charmtone.Cumin,
	})
}

func CharmtonePanteraLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#6c3fc5"),
		secondary: lipgloss.Color("#9c2f73"),
		accent:    lipgloss.Color("#287a3d"),
		keyword:   lipgloss.Color("#b4234d"),

		fgBase:       lipgloss.Color("#2f2923"),
		fgSubtle:     lipgloss.Color("#514940"),
		fgMoreSubtle: lipgloss.Color("#6d6257"),
		fgMostSubtle: lipgloss.Color("#887b6d"),

		onPrimary: lipgloss.Color("#fffaf3"),

		bgBase:         lipgloss.Color("#fffaf3"),
		bgLeastVisible: lipgloss.Color("#f7efe4"),
		bgLessVisible:  lipgloss.Color("#eadfce"),
		bgMostVisible:  lipgloss.Color("#c8b9a5"),
		separator:      lipgloss.Color("#c8b9a5"),

		destructive:       lipgloss.Color("#b42318"),
		error:             lipgloss.Color("#b42318"),
		warning:           lipgloss.Color("#a34f00"),
		warningSubtle:     lipgloss.Color("#8a6500"),
		denied:            lipgloss.Color("#b42318"),
		busy:              lipgloss.Color("#8a6500"),
		info:              lipgloss.Color("#1769aa"),
		infoMoreSubtle:    lipgloss.Color("#087f8c"),
		infoMostSubtle:    lipgloss.Color("#6d6257"),
		success:           lipgloss.Color("#287a3d"),
		successMoreSubtle: lipgloss.Color("#087f8c"),
		successMostSubtle: lipgloss.Color("#6d6257"),

		diffAddFg:        lipgloss.Color("#287a3d"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#b42318"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#9c2f73"),

		syntaxLink:            lipgloss.Color("#1769aa"),
		syntaxImage:           lipgloss.Color("#6c3fc5"),
		syntaxCommentPreproc:  lipgloss.Color("#a34f00"),
		syntaxKeywordReserved: lipgloss.Color("#6c3fc5"),
		syntaxKeywordType:     lipgloss.Color("#087f8c"),
		syntaxOperator:        lipgloss.Color("#b4234d"),
		syntaxNameBuiltin:     lipgloss.Color("#1769aa"),
		syntaxNameTag:         lipgloss.Color("#b42318"),
		syntaxNameAttribute:   lipgloss.Color("#a34f00"),
		syntaxNameClass:       lipgloss.Color("#1769aa"),
		syntaxNameDecorator:   lipgloss.Color("#6c3fc5"),
		syntaxLiteralString:   lipgloss.Color("#287a3d"),
	})
}
