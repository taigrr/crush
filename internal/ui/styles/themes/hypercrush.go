package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/taigrr/crush/internal/ui/styles"
)

// HypercrushObsidiana returns the Hypercrush dark theme.
func HypercrushObsidiana() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   charmtone.Charple,
		Secondary: charmtone.Dolly,
		Accent:    charmtone.Bok,

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

func HypercrushObsidianaLight() styles.Styles {
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   lipgloss.Color("#4b43a8"),
		Secondary: lipgloss.Color("#b02f7d"),
		Accent:    lipgloss.Color("#147a58"),
		Keyword:   lipgloss.Color("#b4234d"),

		FgBase:       lipgloss.Color("#29243b"),
		FgSubtle:     lipgloss.Color("#49425e"),
		FgMoreSubtle: lipgloss.Color("#655d78"),
		FgMostSubtle: lipgloss.Color("#81778f"),

		OnPrimary: lipgloss.Color("#f7f5ff"),

		BgBase:         lipgloss.Color("#f7f5ff"),
		BgLeastVisible: lipgloss.Color("#eeebfa"),
		BgLessVisible:  lipgloss.Color("#dfd9f0"),
		BgMostVisible:  lipgloss.Color("#b8afd2"),
		Separator:      lipgloss.Color("#b8afd2"),

		Destructive:       lipgloss.Color("#b4232f"),
		Error:             lipgloss.Color("#b4232f"),
		Warning:           lipgloss.Color("#9a4b00"),
		WarningSubtle:     lipgloss.Color("#806500"),
		Denied:            lipgloss.Color("#b4232f"),
		Busy:              lipgloss.Color("#806500"),
		Info:              lipgloss.Color("#315ca8"),
		InfoMoreSubtle:    lipgloss.Color("#087787"),
		InfoMostSubtle:    lipgloss.Color("#655d78"),
		Success:           lipgloss.Color("#147a58"),
		SuccessMoreSubtle: lipgloss.Color("#087787"),
		SuccessMostSubtle: lipgloss.Color("#655d78"),

		DiffAddFg:        lipgloss.Color("#147a58"),
		DiffAddBg:        lipgloss.Color("#dff2e1"),
		DiffAddBgEmph:    lipgloss.Color("#c8e8cc"),
		DiffRemoveFg:     lipgloss.Color("#b4232f"),
		DiffRemoveBg:     lipgloss.Color("#f9dfdf"),
		DiffRemoveBgEmph: lipgloss.Color("#f2caca"),

		Hypercredit: lipgloss.Color("#b02f7d"),

		SyntaxLink:            lipgloss.Color("#315ca8"),
		SyntaxImage:           lipgloss.Color("#4b43a8"),
		SyntaxCommentPreproc:  lipgloss.Color("#9a4b00"),
		SyntaxKeywordReserved: lipgloss.Color("#4b43a8"),
		SyntaxKeywordType:     lipgloss.Color("#087787"),
		SyntaxOperator:        lipgloss.Color("#b4234d"),
		SyntaxNameBuiltin:     lipgloss.Color("#315ca8"),
		SyntaxNameTag:         lipgloss.Color("#b4232f"),
		SyntaxNameAttribute:   lipgloss.Color("#9a4b00"),
		SyntaxNameClass:       lipgloss.Color("#315ca8"),
		SyntaxNameDecorator:   lipgloss.Color("#4b43a8"),
		SyntaxLiteralString:   lipgloss.Color("#147a58"),
	})
}
