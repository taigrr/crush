package themes

import (
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/styles"
)

// The Monochrome family is a grayscale palette where a single accent carries
// every point of emphasis. Diffs are the one other exception, staying
// red/green because the add/remove distinction carries meaning a gray ramp
// cannot.
//
// Only the accent changes between family members, so the grayscale body of
// the palette lives once in monochromeDark/monochromeLight and each variant
// supplies a monochromeAccent.

// monochromeAccent holds the chromatic hexes of one Monochrome variant.
// Every hex must clear WCAG AA (4.5:1) against its variant's background,
// because each is used both as text on that background and as a filled
// surface behind onAccent text. TestMonochromeContrast enforces this.
type monochromeAccent struct {
	// base carries brand and emphasis: primary, accent, keyword, info,
	// headings, links, and the Hypercredit glyph.
	base string
	// strong is error text: a brighter (dark variant) or darker (light
	// variant) base, so it still reads as an alarm against a palette that
	// has no other red.
	strong string
	// soft is the denied/interrupt tag background.
	soft string
	// subtle is the recessed accent (infoMoreSubtle), which the TUI uses
	// only as the agent-task tag background. It recedes by dropping chroma
	// rather than lightness, because lightness is what carries its contrast
	// against onAccent text.
	subtle string
	// onAccent is the text color placed on top of the four hexes above.
	// In the dark variant every such surface is a light color (a bright
	// accent, or one of the light grays used for focused buttons and
	// archived rows), so this is near-black there and white in the light
	// variant, not the other way around.
	onAccent string
}

// Accent sets. The dark bases are inherited from the pi monochrome-accent
// palette, kept verbatim where they clear AA against #181818 and minimally
// lightened where they do not (blue, purple, and red were too dark to read).
// The light bases keep the same hue and saturation, darkened to roughly
// 5.3:1 against white to match the hand-picked orange pair the family
// started from.
const (
	monochromeOnDark  = "#181818" // bgBase of the dark variant
	monochromeOnLight = "#ffffff" // bgBase of the light variant
)

var (
	monochromeOrangeDark  = monochromeAccent{base: "#ff4b00", strong: "#ff6f33", soft: "#ff6829", subtle: "#bd7252", onAccent: monochromeOnDark}
	monochromeOrangeLight = monochromeAccent{base: "#c43b00", strong: "#a83300", soft: "#c43b00", subtle: "#8e5036", onAccent: monochromeOnLight}

	monochromeGreenDark  = monochromeAccent{base: "#00d05a", strong: "#04ff71", soft: "#00f96c", subtle: "#399762", onAccent: monochromeOnDark}
	monochromeGreenLight = monochromeAccent{base: "#007d36", strong: "#00612a", soft: "#007d36", subtle: "#225b3b", onAccent: monochromeOnLight}

	monochromeBlueDark  = monochromeAccent{base: "#2482ff", strong: "#579fff", soft: "#4d99ff", subtle: "#608bc3", onAccent: monochromeOnDark}
	monochromeBlueLight = monochromeAccent{base: "#0064e8", strong: "#0058cc", soft: "#0064e8", subtle: "#406da8", onAccent: monochromeOnLight}

	monochromeYellowDark  = monochromeAccent{base: "#ffd000", strong: "#ffe367", soft: "#ffd829", subtle: "#b9a446", onAccent: monochromeOnDark}
	monochromeYellowLight = monochromeAccent{base: "#816900", strong: "#655200", soft: "#816900", subtle: "#5e5323", onAccent: monochromeOnLight}

	monochromePurpleDark  = monochromeAccent{base: "#c04bff", strong: "#d27eff", soft: "#ce74ff", subtle: "#b17dce", onAccent: monochromeOnDark}
	monochromePurpleLight = monochromeAccent{base: "#a400fc", strong: "#9200e0", soft: "#a400fc", subtle: "#8f45b7", onAccent: monochromeOnLight}

	monochromeRedDark  = monochromeAccent{base: "#ff2b48", strong: "#ff5e74", soft: "#ff546b", subtle: "#c66975", onAccent: monochromeOnDark}
	monochromeRedLight = monochromeAccent{base: "#d9001e", strong: "#bd001a", soft: "#d9001e", subtle: "#9d3c49", onAccent: monochromeOnLight}
)

// monochromeDark builds the dark grayscale body around an accent.
func monochromeDark(a monochromeAccent) styles.Styles {
	base := lipgloss.Color(a.base)
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   base,
		Secondary: lipgloss.Color("#b8b8b8"),
		Accent:    base,
		Keyword:   base,

		FgBase:       lipgloss.Color("#c7c7c7"),
		FgSubtle:     lipgloss.Color("#b0b0b0"),
		FgMoreSubtle: lipgloss.Color("#9a9a9a"),
		FgMostSubtle: lipgloss.Color("#707070"),

		OnPrimary: lipgloss.Color(a.onAccent),

		BgBase:         lipgloss.Color("#181818"),
		BgLeastVisible: lipgloss.Color("#141414"),
		BgLessVisible:  lipgloss.Color("#202020"),
		BgMostVisible:  lipgloss.Color("#383838"),
		Separator:      lipgloss.Color("#4d4d4d"),

		Destructive:       base,
		Error:             lipgloss.Color(a.strong),
		Warning:           lipgloss.Color("#adadad"),
		WarningSubtle:     lipgloss.Color("#e2e2e2"),
		Denied:            lipgloss.Color(a.soft),
		Busy:              lipgloss.Color("#c7c7c7"),
		Info:              base,
		InfoMoreSubtle:    lipgloss.Color(a.subtle),
		InfoMostSubtle:    lipgloss.Color("#8f8f8f"),
		Success:           lipgloss.Color("#b8b8b8"),
		SuccessMoreSubtle: lipgloss.Color("#a8a8a8"),
		SuccessMostSubtle: lipgloss.Color("#8f8f8f"),

		DiffAddFg:        lipgloss.Color("#6bb36b"),
		DiffAddBg:        lipgloss.Color("#1b2a1b"),
		DiffAddBgEmph:    lipgloss.Color("#162216"),
		DiffRemoveFg:     lipgloss.Color("#d4595c"),
		DiffRemoveBg:     lipgloss.Color("#2c1b1c"),
		DiffRemoveBgEmph: lipgloss.Color("#241617"),
		DiffAddText:      lipgloss.Color("#6bb36b"),
		DiffRemoveText:   lipgloss.Color("#d4595c"),

		Hypercredit: base,

		SyntaxLink:            base,
		SyntaxImage:           base,
		SyntaxCommentPreproc:  lipgloss.Color("#858585"),
		SyntaxKeywordReserved: lipgloss.Color("#d0d0d0"),
		SyntaxKeywordType:     lipgloss.Color("#b4b4b4"),
		SyntaxOperator:        lipgloss.Color("#969696"),
		SyntaxNameBuiltin:     lipgloss.Color("#bcbcbc"),
		SyntaxNameTag:         lipgloss.Color("#d0d0d0"),
		SyntaxNameAttribute:   lipgloss.Color("#c8c8c8"),
		SyntaxNameClass:       lipgloss.Color("#dedede"),
		SyntaxNameDecorator:   lipgloss.Color("#a8a8a8"),
		SyntaxLiteralString:   lipgloss.Color("#a8a8a8"),
	})
}

// monochromeLight builds the light grayscale body around an accent.
func monochromeLight(a monochromeAccent) styles.Styles {
	base := lipgloss.Color(a.base)
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:   base,
		Secondary: lipgloss.Color("#525252"),
		Accent:    base,
		Keyword:   base,

		FgBase:       lipgloss.Color("#242424"),
		FgSubtle:     lipgloss.Color("#333333"),
		FgMoreSubtle: lipgloss.Color("#525252"),
		FgMostSubtle: lipgloss.Color("#707070"),

		OnPrimary: lipgloss.Color(a.onAccent),

		BgBase:         lipgloss.Color("#ffffff"),
		BgLeastVisible: lipgloss.Color("#f3f3f3"),
		BgLessVisible:  lipgloss.Color("#e8e8e8"),
		BgMostVisible:  lipgloss.Color("#c4c4c4"),
		Separator:      lipgloss.Color("#c4c4c4"),

		Destructive:       base,
		Error:             lipgloss.Color(a.strong),
		Warning:           lipgloss.Color("#5c5c5c"),
		WarningSubtle:     lipgloss.Color("#474747"),
		Denied:            lipgloss.Color(a.soft),
		Busy:              lipgloss.Color("#8a8a8a"),
		Info:              base,
		InfoMoreSubtle:    lipgloss.Color(a.subtle),
		InfoMostSubtle:    lipgloss.Color("#666666"),
		Success:           lipgloss.Color("#3d3d3d"),
		SuccessMoreSubtle: lipgloss.Color("#4a4a4a"),
		SuccessMostSubtle: lipgloss.Color("#5c5c5c"),

		DiffAddFg:        lipgloss.Color("#2f7d32"),
		DiffAddBg:        lipgloss.Color("#e3f2e3"),
		DiffAddBgEmph:    lipgloss.Color("#d2e9d3"),
		DiffRemoveFg:     lipgloss.Color("#b3261e"),
		DiffRemoveBg:     lipgloss.Color("#fbe4e2"),
		DiffRemoveBgEmph: lipgloss.Color("#f6d2cf"),
		DiffAddText:      lipgloss.Color("#2f7d32"),
		DiffRemoveText:   lipgloss.Color("#b3261e"),

		Hypercredit: base,

		SyntaxLink:            base,
		SyntaxImage:           base,
		SyntaxCommentPreproc:  lipgloss.Color("#7d7d7d"),
		SyntaxKeywordReserved: lipgloss.Color("#474747"),
		SyntaxKeywordType:     lipgloss.Color("#595959"),
		SyntaxOperator:        lipgloss.Color("#666666"),
		SyntaxNameBuiltin:     lipgloss.Color("#3d3d3d"),
		SyntaxNameTag:         lipgloss.Color("#474747"),
		SyntaxNameAttribute:   lipgloss.Color("#333333"),
		SyntaxNameClass:       lipgloss.Color("#242424"),
		SyntaxNameDecorator:   lipgloss.Color("#525252"),
		SyntaxLiteralString:   lipgloss.Color("#525252"),
	})
}

// Monochrome returns the dark Monochrome theme with the default orange
// accent.
func Monochrome() styles.Styles { return monochromeDark(monochromeOrangeDark) }

// MonochromeLight returns the light Monochrome theme with the default orange
// accent.
func MonochromeLight() styles.Styles { return monochromeLight(monochromeOrangeLight) }

// MonochromeGreen returns the dark Monochrome theme with a green accent.
func MonochromeGreen() styles.Styles { return monochromeDark(monochromeGreenDark) }

// MonochromeGreenLight returns the light Monochrome theme with a green
// accent.
func MonochromeGreenLight() styles.Styles { return monochromeLight(monochromeGreenLight) }

// MonochromeBlue returns the dark Monochrome theme with a blue accent.
func MonochromeBlue() styles.Styles { return monochromeDark(monochromeBlueDark) }

// MonochromeBlueLight returns the light Monochrome theme with a blue accent.
func MonochromeBlueLight() styles.Styles { return monochromeLight(monochromeBlueLight) }

// MonochromeYellow returns the dark Monochrome theme with a yellow accent.
func MonochromeYellow() styles.Styles { return monochromeDark(monochromeYellowDark) }

// MonochromeYellowLight returns the light Monochrome theme with a yellow
// accent.
func MonochromeYellowLight() styles.Styles { return monochromeLight(monochromeYellowLight) }

// MonochromePurple returns the dark Monochrome theme with a purple accent.
func MonochromePurple() styles.Styles { return monochromeDark(monochromePurpleDark) }

// MonochromePurpleLight returns the light Monochrome theme with a purple
// accent.
func MonochromePurpleLight() styles.Styles { return monochromeLight(monochromePurpleLight) }

// MonochromeRed returns the dark Monochrome theme with a red accent.
func MonochromeRed() styles.Styles { return monochromeDark(monochromeRedDark) }

// MonochromeRedLight returns the light Monochrome theme with a red accent.
func MonochromeRedLight() styles.Styles { return monochromeLight(monochromeRedLight) }
