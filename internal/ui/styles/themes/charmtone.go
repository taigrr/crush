package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/taigrr/crush/internal/ui/styles"
)

// CharmtonePantera returns the Charmtone dark theme. It's the default style
// for the UI.
func CharmtonePantera() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   charmtone.Charple,
		Secondary: charmtone.Dolly,
		Accent:    charmtone.Bok,
		Keyword:   charmtone.Blush,

		FgBase:       charmtone.Sash,
		FgMoreSubtle: charmtone.Squid,
		FgSubtle:     charmtone.Smoke,
		FgMostSubtle: charmtone.Oyster,

		OnPrimary: charmtone.Butter,

		BgBase:         charmtone.Pepper,
		BgLeastVisible: charmtone.BBQ,
		BgLessVisible:  charmtone.Char,
		BgMostVisible:  charmtone.Iron,

		Separator: charmtone.Char,

		Destructive:       charmtone.Coral,
		Error:             charmtone.Sriracha,
		WarningSubtle:     charmtone.Zest,
		Warning:           charmtone.Mustard,
		Denied:            charmtone.Tang,
		Busy:              charmtone.Citron,
		Info:              charmtone.Malibu,
		InfoMoreSubtle:    charmtone.Sardine,
		InfoMostSubtle:    charmtone.Damson,
		Success:           charmtone.Julep,
		SuccessMoreSubtle: charmtone.Bok,
		SuccessMostSubtle: charmtone.Guac,

		DiffAddFg:        lipgloss.Color("#629657"),
		DiffAddBg:        lipgloss.Color("#323931"),
		DiffAddBgEmph:    lipgloss.Color("#2b322a"),
		DiffRemoveFg:     lipgloss.Color("#a45c59"),
		DiffRemoveBg:     lipgloss.Color("#383030"),
		DiffRemoveBgEmph: lipgloss.Color("#312929"),

		Hypercredit: charmtone.Dolly,

		SyntaxLink:            charmtone.Zinc,
		SyntaxImage:           charmtone.Cheeky,
		SyntaxCommentPreproc:  charmtone.Bengal,
		SyntaxKeywordReserved: charmtone.Pony,
		SyntaxKeywordType:     charmtone.Guppy,
		SyntaxOperator:        charmtone.Salmon,
		SyntaxNameBuiltin:     charmtone.Cheeky,
		SyntaxNameTag:         charmtone.Mauve,
		SyntaxNameAttribute:   charmtone.Hazy,
		SyntaxNameClass:       charmtone.Salt,
		SyntaxNameDecorator:   charmtone.Citron,
		SyntaxLiteralString:   charmtone.Cumin,
	})
}

func CharmtonePanteraLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#6c3fc5"),
		Secondary: lipgloss.Color("#9c2f73"),
		Accent:    lipgloss.Color("#287a3d"),
		Keyword:   lipgloss.Color("#b4234d"),

		FgBase:       lipgloss.Color("#2f2923"),
		FgSubtle:     lipgloss.Color("#514940"),
		FgMoreSubtle: lipgloss.Color("#6d6257"),
		FgMostSubtle: lipgloss.Color("#887b6d"),

		OnPrimary: lipgloss.Color("#fffaf3"),

		BgBase:         lipgloss.Color("#fffaf3"),
		BgLeastVisible: lipgloss.Color("#f7efe4"),
		BgLessVisible:  lipgloss.Color("#eadfce"),
		BgMostVisible:  lipgloss.Color("#c8b9a5"),
		Separator:      lipgloss.Color("#c8b9a5"),

		Destructive:       lipgloss.Color("#b42318"),
		Error:             lipgloss.Color("#b42318"),
		Warning:           lipgloss.Color("#a34f00"),
		WarningSubtle:     lipgloss.Color("#8a6500"),
		Denied:            lipgloss.Color("#b42318"),
		Busy:              lipgloss.Color("#8a6500"),
		Info:              lipgloss.Color("#1769aa"),
		InfoMoreSubtle:    lipgloss.Color("#087f8c"),
		InfoMostSubtle:    lipgloss.Color("#6d6257"),
		Success:           lipgloss.Color("#287a3d"),
		SuccessMoreSubtle: lipgloss.Color("#087f8c"),
		SuccessMostSubtle: lipgloss.Color("#6d6257"),

		DiffAddFg:        lipgloss.Color("#287a3d"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#b42318"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#9c2f73"),

		SyntaxLink:            lipgloss.Color("#1769aa"),
		SyntaxImage:           lipgloss.Color("#6c3fc5"),
		SyntaxCommentPreproc:  lipgloss.Color("#a34f00"),
		SyntaxKeywordReserved: lipgloss.Color("#6c3fc5"),
		SyntaxKeywordType:     lipgloss.Color("#087f8c"),
		SyntaxOperator:        lipgloss.Color("#b4234d"),
		SyntaxNameBuiltin:     lipgloss.Color("#1769aa"),
		SyntaxNameTag:         lipgloss.Color("#b42318"),
		SyntaxNameAttribute:   lipgloss.Color("#a34f00"),
		SyntaxNameClass:       lipgloss.Color("#1769aa"),
		SyntaxNameDecorator:   lipgloss.Color("#6c3fc5"),
		SyntaxLiteralString:   lipgloss.Color("#287a3d"),
	})
}
