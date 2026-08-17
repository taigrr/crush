package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type lightThemePalette struct {
	primary    string
	secondary  string
	accent     string
	keyword    string
	background string
	surface    string
	overlay    string
	border     string
	foreground string
	subtle     string
	muted      string
	faint      string
	red        string
	yellow     string
	blue       string
	green      string
	cyan       string
	purple     string
	orange     string
}

func lightTheme(p lightThemePalette) Styles {
	color := func(value string) color.Color { return lipgloss.Color(value) }
	return quickStyle(quickStyleOpts{
		primary: color(p.primary), secondary: color(p.secondary), accent: color(p.accent), keyword: color(p.keyword),
		fgBase: color(p.foreground), fgSubtle: color(p.subtle), fgMoreSubtle: color(p.muted), fgMostSubtle: color(p.faint),
		onPrimary: color(p.background),
		bgBase:    color(p.background), bgLeastVisible: color(p.surface), bgLessVisible: color(p.overlay), bgMostVisible: color(p.border), separator: color(p.border),
		destructive: color(p.red), error: color(p.red), warning: color(p.orange), warningSubtle: color(p.yellow), denied: color(p.red), busy: color(p.yellow),
		info: color(p.blue), infoMoreSubtle: color(p.cyan), infoMostSubtle: color(p.muted),
		success: color(p.green), successMoreSubtle: color(p.cyan), successMostSubtle: color(p.muted),
		diffAddFg: color(p.green), diffAddBg: color("#dff2e1"), diffAddBgEmph: color("#c8e8cc"),
		diffRemoveFg: color(p.red), diffRemoveBg: color("#f9dfdf"), diffRemoveBgEmph: color("#f2caca"),
		hypercredit: color(p.secondary),
		syntaxLink:  color(p.blue), syntaxImage: color(p.purple), syntaxCommentPreproc: color(p.orange),
		syntaxKeywordReserved: color(p.purple), syntaxKeywordType: color(p.cyan), syntaxOperator: color(p.keyword),
		syntaxNameBuiltin: color(p.blue), syntaxNameTag: color(p.red), syntaxNameAttribute: color(p.orange),
		syntaxNameClass: color(p.blue), syntaxNameDecorator: color(p.purple), syntaxLiteralString: color(p.green),
	})
}

func CharmtonePanteraLight() Styles {
	return lightTheme(lightThemePalette{primary: "#6c3fc5", secondary: "#9c2f73", accent: "#287a3d", keyword: "#b4234d", background: "#fffaf3", surface: "#f7efe4", overlay: "#eadfce", border: "#c8b9a5", foreground: "#2f2923", subtle: "#514940", muted: "#6d6257", faint: "#887b6d", red: "#b42318", yellow: "#8a6500", blue: "#1769aa", green: "#287a3d", cyan: "#087f8c", purple: "#6c3fc5", orange: "#a34f00"})
}

func HypercrushObsidianaLight() Styles {
	return lightTheme(lightThemePalette{primary: "#4b43a8", secondary: "#b02f7d", accent: "#147a58", keyword: "#b4234d", background: "#f7f5ff", surface: "#eeebfa", overlay: "#dfd9f0", border: "#b8afd2", foreground: "#29243b", subtle: "#49425e", muted: "#655d78", faint: "#81778f", red: "#b4232f", yellow: "#806500", blue: "#315ca8", green: "#147a58", cyan: "#087787", purple: "#4b43a8", orange: "#9a4b00"})
}

func TokyoNightLight() Styles {
	return lightTheme(lightThemePalette{primary: "#34548a", secondary: "#5a4a78", accent: "#33635c", keyword: "#8c4351", background: "#e6e7ed", surface: "#dcdfe7", overlay: "#cfd2dc", border: "#a8aec1", foreground: "#343b58", subtle: "#4c505e", muted: "#68709a", faint: "#777c99", red: "#8c4351", yellow: "#8f5e15", blue: "#34548a", green: "#485e30", cyan: "#0f4b6e", purple: "#5a4a78", orange: "#965027"})
}

func CatppuccinLatte() Styles {
	return lightTheme(lightThemePalette{primary: "#8839ef", secondary: "#ea76cb", accent: "#40a02b", keyword: "#d20f39", background: "#eff1f5", surface: "#e6e9ef", overlay: "#dce0e8", border: "#9ca0b0", foreground: "#4c4f69", subtle: "#5c5f77", muted: "#6c6f85", faint: "#7c7f93", red: "#d20f39", yellow: "#8c6f00", blue: "#1e66f5", green: "#287a15", cyan: "#047f8f", purple: "#8839ef", orange: "#b45b00"})
}

func DraculaLight() Styles {
	return lightTheme(lightThemePalette{primary: "#6f42c1", secondary: "#a71972", accent: "#287a3d", keyword: "#a71972", background: "#f8f8f2", surface: "#eeeeea", overlay: "#e2e2dc", border: "#aaaab0", foreground: "#282a36", subtle: "#44475a", muted: "#5f6170", faint: "#747789", red: "#b4232f", yellow: "#806c00", blue: "#1769aa", green: "#287a3d", cyan: "#087f8c", purple: "#6f42c1", orange: "#a34f00"})
}

func NordLight() Styles {
	return lightTheme(lightThemePalette{primary: "#3b6e7a", secondary: "#4c6480", accent: "#557547", keyword: "#7b4f71", background: "#eceff4", surface: "#e5e9f0", overlay: "#d8dee9", border: "#9aa5b5", foreground: "#2e3440", subtle: "#3b4252", muted: "#4c566a", faint: "#667287", red: "#9b4049", yellow: "#806500", blue: "#4c6480", green: "#557547", cyan: "#39706f", purple: "#7b4f71", orange: "#96543f"})
}

func GruvboxLight() Styles {
	return lightTheme(lightThemePalette{primary: "#8f6f00", secondary: "#af3a03", accent: "#5f6f00", keyword: "#9d0006", background: "#fbf1c7", surface: "#f2e5bc", overlay: "#ebdbb2", border: "#bdae93", foreground: "#3c3836", subtle: "#504945", muted: "#665c54", faint: "#7c6f64", red: "#9d0006", yellow: "#7c6500", blue: "#076678", green: "#5f6f00", cyan: "#427b58", purple: "#8f3f71", orange: "#af3a03"})
}

func CyberpunkLight() Styles {
	return lightTheme(lightThemePalette{primary: "#356a00", secondary: "#806900", accent: "#007784", keyword: "#b00000", background: "#f6fff2", surface: "#eaf6e5", overlay: "#d8ead0", border: "#9bb78f", foreground: "#24331d", subtle: "#3c4d34", muted: "#586750", faint: "#708069", red: "#b00000", yellow: "#756000", blue: "#006d9e", green: "#356a00", cyan: "#007784", purple: "#6f42a5", orange: "#9a4b00"})
}

func RosePineDawn() Styles {
	return lightTheme(lightThemePalette{primary: "#575279", secondary: "#d7827e", accent: "#286983", keyword: "#b4637a", background: "#faf4ed", surface: "#fffaf3", overlay: "#f2e9de", border: "#c4b8aa", foreground: "#575279", subtle: "#6e6a86", muted: "#797593", faint: "#8f899f", red: "#b4637a", yellow: "#806423", blue: "#286983", green: "#47765b", cyan: "#287080", purple: "#575279", orange: "#a15d30"})
}

func VSCodeLight() Styles {
	return lightTheme(lightThemePalette{primary: "#005fb8", secondary: "#007f6e", accent: "#8f3985", keyword: "#8f3985", background: "#ffffff", surface: "#f3f3f3", overlay: "#e8e8e8", border: "#b8b8b8", foreground: "#242424", subtle: "#3b3b3b", muted: "#5f5f5f", faint: "#767676", red: "#b52020", yellow: "#786000", blue: "#005fb8", green: "#287a3d", cyan: "#007f6e", purple: "#8f3985", orange: "#a34f00"})
}
