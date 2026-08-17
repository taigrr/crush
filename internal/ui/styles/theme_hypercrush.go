package styles

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// HypercrushObsidiana returns the Hypercrush dark theme.
func HypercrushObsidiana() Styles {
	return quickStyle(quickStyleOpts{
		primary:   charmtone.Charple,
		secondary: charmtone.Dolly,
		accent:    charmtone.Bok,

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

func HypercrushObsidianaLight() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#4b43a8"),
		secondary: lipgloss.Color("#b02f7d"),
		accent:    lipgloss.Color("#147a58"),
		keyword:   lipgloss.Color("#b4234d"),

		fgBase:       lipgloss.Color("#29243b"),
		fgSubtle:     lipgloss.Color("#49425e"),
		fgMoreSubtle: lipgloss.Color("#655d78"),
		fgMostSubtle: lipgloss.Color("#81778f"),

		onPrimary: lipgloss.Color("#f7f5ff"),

		bgBase:         lipgloss.Color("#f7f5ff"),
		bgLeastVisible: lipgloss.Color("#eeebfa"),
		bgLessVisible:  lipgloss.Color("#dfd9f0"),
		bgMostVisible:  lipgloss.Color("#b8afd2"),
		separator:      lipgloss.Color("#b8afd2"),

		destructive:       lipgloss.Color("#b4232f"),
		error:             lipgloss.Color("#b4232f"),
		warning:           lipgloss.Color("#9a4b00"),
		warningSubtle:     lipgloss.Color("#806500"),
		denied:            lipgloss.Color("#b4232f"),
		busy:              lipgloss.Color("#806500"),
		info:              lipgloss.Color("#315ca8"),
		infoMoreSubtle:    lipgloss.Color("#087787"),
		infoMostSubtle:    lipgloss.Color("#655d78"),
		success:           lipgloss.Color("#147a58"),
		successMoreSubtle: lipgloss.Color("#087787"),
		successMostSubtle: lipgloss.Color("#655d78"),

		diffAddFg:        lipgloss.Color("#147a58"),
		diffAddBg:        lipgloss.Color("#dff2e1"),
		diffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		diffRemoveFg:     lipgloss.Color("#b4232f"),
		diffRemoveBg:     lipgloss.Color("#f9dfdf"),
		diffRemoveBgEmph: lipgloss.Color("#f2caca"),

		hypercredit: lipgloss.Color("#b02f7d"),

		syntaxLink:            lipgloss.Color("#315ca8"),
		syntaxImage:           lipgloss.Color("#4b43a8"),
		syntaxCommentPreproc:  lipgloss.Color("#9a4b00"),
		syntaxKeywordReserved: lipgloss.Color("#4b43a8"),
		syntaxKeywordType:     lipgloss.Color("#087787"),
		syntaxOperator:        lipgloss.Color("#b4234d"),
		syntaxNameBuiltin:     lipgloss.Color("#315ca8"),
		syntaxNameTag:         lipgloss.Color("#b4232f"),
		syntaxNameAttribute:   lipgloss.Color("#9a4b00"),
		syntaxNameClass:       lipgloss.Color("#315ca8"),
		syntaxNameDecorator:   lipgloss.Color("#4b43a8"),
		syntaxLiteralString:   lipgloss.Color("#147a58"),
	})
}
