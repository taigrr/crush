package styles

import "charm.land/lipgloss/v2"

// This file defines a curated set of community-favorite color palettes as
// builtin themes. Each is a fully-specified palette (no cascading fallbacks
// to Charmtone) so the look matches the upstream theme as closely as a
// terminal palette permits. Hex values come from the official palette of
// each project.

// TokyoNight returns the Tokyo Night dark theme by enkia.
// https://github.com/folke/tokyonight.nvim
func TokyoNight() Styles {
	return quickStyle(quickStyleOpts{
		// Brand: blue (primary), purple (secondary), green accent, pink keyword.
		primary:   lipgloss.Color("#7aa2f7"),
		secondary: lipgloss.Color("#bb9af7"),
		accent:    lipgloss.Color("#9ece6a"),
		keyword:   lipgloss.Color("#f7768e"),

		fgBase:       lipgloss.Color("#c0caf5"),
		fgSubtle:     lipgloss.Color("#a9b1d6"),
		fgMoreSubtle: lipgloss.Color("#737aa2"),
		fgMostSubtle: lipgloss.Color("#565f89"),

		onPrimary: lipgloss.Color("#1a1b26"),

		bgBase:         lipgloss.Color("#1a1b26"),
		bgLeastVisible: lipgloss.Color("#16161e"),
		bgLessVisible:  lipgloss.Color("#24283b"),
		bgMostVisible:  lipgloss.Color("#292e42"),
		separator:      lipgloss.Color("#292e42"),

		destructive:       lipgloss.Color("#f7768e"),
		error:             lipgloss.Color("#db4b4b"),
		warning:           lipgloss.Color("#e0af68"),
		warningSubtle:     lipgloss.Color("#cfc9c2"),
		denied:            lipgloss.Color("#ff9e64"),
		busy:              lipgloss.Color("#e0af68"),
		info:              lipgloss.Color("#7dcfff"),
		infoMoreSubtle:    lipgloss.Color("#7aa2f7"),
		infoMostSubtle:    lipgloss.Color("#3d59a1"),
		success:           lipgloss.Color("#9ece6a"),
		successMoreSubtle: lipgloss.Color("#73daca"),
		successMostSubtle: lipgloss.Color("#41a6b5"),

		diffAddFg:        lipgloss.Color("#9ece6a"),
		diffAddBg:        lipgloss.Color("#20303b"),
		diffAddBgEmph:    lipgloss.Color("#1a2530"),
		diffRemoveFg:     lipgloss.Color("#f7768e"),
		diffRemoveBg:     lipgloss.Color("#37222c"),
		diffRemoveBgEmph: lipgloss.Color("#2c1c24"),

		hypercredit: lipgloss.Color("#bb9af7"),

		syntaxLink:            lipgloss.Color("#7dcfff"),
		syntaxImage:           lipgloss.Color("#bb9af7"),
		syntaxCommentPreproc:  lipgloss.Color("#e0af68"),
		syntaxKeywordReserved: lipgloss.Color("#bb9af7"),
		syntaxKeywordType:     lipgloss.Color("#2ac3de"),
		syntaxOperator:        lipgloss.Color("#89ddff"),
		syntaxNameBuiltin:     lipgloss.Color("#f7768e"),
		syntaxNameTag:         lipgloss.Color("#f7768e"),
		syntaxNameAttribute:   lipgloss.Color("#e0af68"),
		syntaxNameClass:       lipgloss.Color("#7aa2f7"),
		syntaxNameDecorator:   lipgloss.Color("#7dcfff"),
		syntaxLiteralString:   lipgloss.Color("#9ece6a"),
	})
}

// CatppuccinMocha returns the Catppuccin Mocha dark theme.
// https://github.com/catppuccin/catppuccin
func CatppuccinMocha() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#cba6f7"), // mauve
		secondary: lipgloss.Color("#f5c2e7"), // pink
		accent:    lipgloss.Color("#a6e3a1"), // green
		keyword:   lipgloss.Color("#f38ba8"), // red

		fgBase:       lipgloss.Color("#cdd6f4"), // text
		fgSubtle:     lipgloss.Color("#bac2de"), // subtext1
		fgMoreSubtle: lipgloss.Color("#a6adc8"), // subtext0
		fgMostSubtle: lipgloss.Color("#7f849c"), // overlay1

		onPrimary: lipgloss.Color("#1e1e2e"),

		bgBase:         lipgloss.Color("#1e1e2e"), // base
		bgLeastVisible: lipgloss.Color("#181825"), // mantle
		bgLessVisible:  lipgloss.Color("#313244"), // surface0
		bgMostVisible:  lipgloss.Color("#45475a"), // surface1
		separator:      lipgloss.Color("#313244"),

		destructive:       lipgloss.Color("#eba0ac"), // maroon
		error:             lipgloss.Color("#f38ba8"), // red
		warning:           lipgloss.Color("#fab387"), // peach
		warningSubtle:     lipgloss.Color("#f9e2af"), // yellow
		denied:            lipgloss.Color("#eba0ac"),
		busy:              lipgloss.Color("#f9e2af"),
		info:              lipgloss.Color("#89b4fa"), // blue
		infoMoreSubtle:    lipgloss.Color("#74c7ec"), // sapphire
		infoMostSubtle:    lipgloss.Color("#585b70"), // surface2
		success:           lipgloss.Color("#a6e3a1"), // green
		successMoreSubtle: lipgloss.Color("#94e2d5"), // teal
		successMostSubtle: lipgloss.Color("#40a02b"),

		diffAddFg:        lipgloss.Color("#a6e3a1"),
		diffAddBg:        lipgloss.Color("#26332b"),
		diffAddBgEmph:    lipgloss.Color("#1f2a23"),
		diffRemoveFg:     lipgloss.Color("#f38ba8"),
		diffRemoveBg:     lipgloss.Color("#3a2b30"),
		diffRemoveBgEmph: lipgloss.Color("#2f2227"),

		hypercredit: lipgloss.Color("#f5e0dc"), // rosewater

		syntaxLink:            lipgloss.Color("#89b4fa"),
		syntaxImage:           lipgloss.Color("#f5c2e7"),
		syntaxCommentPreproc:  lipgloss.Color("#f9e2af"),
		syntaxKeywordReserved: lipgloss.Color("#cba6f7"),
		syntaxKeywordType:     lipgloss.Color("#94e2d5"),
		syntaxOperator:        lipgloss.Color("#89dceb"), // sky
		syntaxNameBuiltin:     lipgloss.Color("#f5c2e7"),
		syntaxNameTag:         lipgloss.Color("#cba6f7"),
		syntaxNameAttribute:   lipgloss.Color("#fab387"),
		syntaxNameClass:       lipgloss.Color("#f9e2af"),
		syntaxNameDecorator:   lipgloss.Color("#f9e2af"),
		syntaxLiteralString:   lipgloss.Color("#a6e3a1"),
	})
}

// Dracula returns the Dracula theme.
// https://draculatheme.com
func Dracula() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#bd93f9"), // purple
		secondary: lipgloss.Color("#ff79c6"), // pink
		accent:    lipgloss.Color("#50fa7b"), // green
		keyword:   lipgloss.Color("#ff79c6"),

		fgBase:       lipgloss.Color("#f8f8f2"),
		fgSubtle:     lipgloss.Color("#bfbfbb"),
		fgMoreSubtle: lipgloss.Color("#8b8d99"),
		fgMostSubtle: lipgloss.Color("#6272a4"),

		onPrimary: lipgloss.Color("#282a36"),

		bgBase:         lipgloss.Color("#282a36"),
		bgLeastVisible: lipgloss.Color("#21222c"),
		bgLessVisible:  lipgloss.Color("#343746"),
		bgMostVisible:  lipgloss.Color("#44475a"),
		separator:      lipgloss.Color("#44475a"),

		destructive:       lipgloss.Color("#ff5555"),
		error:             lipgloss.Color("#ff5555"),
		warning:           lipgloss.Color("#ffb86c"),
		warningSubtle:     lipgloss.Color("#f1fa8c"),
		denied:            lipgloss.Color("#ff5555"),
		busy:              lipgloss.Color("#f1fa8c"),
		info:              lipgloss.Color("#8be9fd"),
		infoMoreSubtle:    lipgloss.Color("#6272a4"),
		infoMostSubtle:    lipgloss.Color("#44475a"),
		success:           lipgloss.Color("#50fa7b"),
		successMoreSubtle: lipgloss.Color("#8be9fd"),
		successMostSubtle: lipgloss.Color("#6272a4"),

		diffAddFg:        lipgloss.Color("#50fa7b"),
		diffAddBg:        lipgloss.Color("#2a3a32"),
		diffAddBgEmph:    lipgloss.Color("#22302a"),
		diffRemoveFg:     lipgloss.Color("#ff5555"),
		diffRemoveBg:     lipgloss.Color("#3a2a2c"),
		diffRemoveBgEmph: lipgloss.Color("#302224"),

		hypercredit: lipgloss.Color("#ff79c6"),

		syntaxLink:            lipgloss.Color("#8be9fd"),
		syntaxImage:           lipgloss.Color("#ff79c6"),
		syntaxCommentPreproc:  lipgloss.Color("#ffb86c"),
		syntaxKeywordReserved: lipgloss.Color("#ff79c6"),
		syntaxKeywordType:     lipgloss.Color("#8be9fd"),
		syntaxOperator:        lipgloss.Color("#ff79c6"),
		syntaxNameBuiltin:     lipgloss.Color("#bd93f9"),
		syntaxNameTag:         lipgloss.Color("#ff79c6"),
		syntaxNameAttribute:   lipgloss.Color("#50fa7b"),
		syntaxNameClass:       lipgloss.Color("#50fa7b"),
		syntaxNameDecorator:   lipgloss.Color("#50fa7b"),
		syntaxLiteralString:   lipgloss.Color("#f1fa8c"),
	})
}

// Nord returns the Nord theme by Sven Greb.
// https://www.nordtheme.com
func Nord() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#88c0d0"), // frost4
		secondary: lipgloss.Color("#81a1c1"), // frost3
		accent:    lipgloss.Color("#a3be8c"), // aurora green
		keyword:   lipgloss.Color("#b48ead"), // aurora purple

		fgBase:       lipgloss.Color("#eceff4"), // snow3
		fgSubtle:     lipgloss.Color("#e5e9f0"), // snow2
		fgMoreSubtle: lipgloss.Color("#d8dee9"), // snow1
		fgMostSubtle: lipgloss.Color("#7b88a1"),

		onPrimary: lipgloss.Color("#2e3440"),

		bgBase:         lipgloss.Color("#2e3440"), // polar1
		bgLeastVisible: lipgloss.Color("#272c36"),
		bgLessVisible:  lipgloss.Color("#3b4252"), // polar2
		bgMostVisible:  lipgloss.Color("#434c5e"), // polar3
		separator:      lipgloss.Color("#4c566a"), // polar4

		destructive:       lipgloss.Color("#bf616a"),
		error:             lipgloss.Color("#bf616a"),
		warning:           lipgloss.Color("#d08770"),
		warningSubtle:     lipgloss.Color("#ebcb8b"),
		denied:            lipgloss.Color("#bf616a"),
		busy:              lipgloss.Color("#ebcb8b"),
		info:              lipgloss.Color("#88c0d0"),
		infoMoreSubtle:    lipgloss.Color("#81a1c1"),
		infoMostSubtle:    lipgloss.Color("#5e81ac"),
		success:           lipgloss.Color("#a3be8c"),
		successMoreSubtle: lipgloss.Color("#8fbcbb"),
		successMostSubtle: lipgloss.Color("#5e81ac"),

		diffAddFg:        lipgloss.Color("#a3be8c"),
		diffAddBg:        lipgloss.Color("#37413a"),
		diffAddBgEmph:    lipgloss.Color("#2f3832"),
		diffRemoveFg:     lipgloss.Color("#bf616a"),
		diffRemoveBg:     lipgloss.Color("#3f3037"),
		diffRemoveBgEmph: lipgloss.Color("#36272d"),

		hypercredit: lipgloss.Color("#88c0d0"),

		syntaxLink:            lipgloss.Color("#88c0d0"),
		syntaxImage:           lipgloss.Color("#b48ead"),
		syntaxCommentPreproc:  lipgloss.Color("#ebcb8b"),
		syntaxKeywordReserved: lipgloss.Color("#81a1c1"),
		syntaxKeywordType:     lipgloss.Color("#8fbcbb"),
		syntaxOperator:        lipgloss.Color("#81a1c1"),
		syntaxNameBuiltin:     lipgloss.Color("#88c0d0"),
		syntaxNameTag:         lipgloss.Color("#81a1c1"),
		syntaxNameAttribute:   lipgloss.Color("#8fbcbb"),
		syntaxNameClass:       lipgloss.Color("#8fbcbb"),
		syntaxNameDecorator:   lipgloss.Color("#d08770"),
		syntaxLiteralString:   lipgloss.Color("#a3be8c"),
	})
}

// GruvboxDark returns the Gruvbox Dark "medium" theme by morhetz.
// https://github.com/morhetz/gruvbox
func GruvboxDark() Styles {
	return quickStyle(quickStyleOpts{
		primary:   lipgloss.Color("#fabd2f"), // bright yellow
		secondary: lipgloss.Color("#fe8019"), // bright orange
		accent:    lipgloss.Color("#b8bb26"), // bright green
		keyword:   lipgloss.Color("#fb4934"), // bright red

		fgBase:       lipgloss.Color("#ebdbb2"), // fg
		fgSubtle:     lipgloss.Color("#d5c4a1"), // fg2
		fgMoreSubtle: lipgloss.Color("#bdae93"), // fg3
		fgMostSubtle: lipgloss.Color("#928374"), // gray

		onPrimary: lipgloss.Color("#282828"),

		bgBase:         lipgloss.Color("#282828"), // bg
		bgLeastVisible: lipgloss.Color("#1d2021"), // bg0_h
		bgLessVisible:  lipgloss.Color("#3c3836"), // bg1
		bgMostVisible:  lipgloss.Color("#504945"), // bg2
		separator:      lipgloss.Color("#665c54"), // bg3

		destructive:       lipgloss.Color("#fb4934"),
		error:             lipgloss.Color("#cc241d"),
		warning:           lipgloss.Color("#fe8019"),
		warningSubtle:     lipgloss.Color("#fabd2f"),
		denied:            lipgloss.Color("#fb4934"),
		busy:              lipgloss.Color("#fabd2f"),
		info:              lipgloss.Color("#83a598"),
		infoMoreSubtle:    lipgloss.Color("#458588"),
		infoMostSubtle:    lipgloss.Color("#076678"),
		success:           lipgloss.Color("#b8bb26"),
		successMoreSubtle: lipgloss.Color("#8ec07c"),
		successMostSubtle: lipgloss.Color("#427b58"),

		diffAddFg:        lipgloss.Color("#b8bb26"),
		diffAddBg:        lipgloss.Color("#34381f"),
		diffAddBgEmph:    lipgloss.Color("#2b2f1a"),
		diffRemoveFg:     lipgloss.Color("#fb4934"),
		diffRemoveBg:     lipgloss.Color("#3c2828"),
		diffRemoveBgEmph: lipgloss.Color("#322020"),

		hypercredit: lipgloss.Color("#d3869b"), // bright purple

		syntaxLink:            lipgloss.Color("#83a598"),
		syntaxImage:           lipgloss.Color("#d3869b"),
		syntaxCommentPreproc:  lipgloss.Color("#fabd2f"),
		syntaxKeywordReserved: lipgloss.Color("#fb4934"),
		syntaxKeywordType:     lipgloss.Color("#fabd2f"),
		syntaxOperator:        lipgloss.Color("#fe8019"),
		syntaxNameBuiltin:     lipgloss.Color("#fabd2f"),
		syntaxNameTag:         lipgloss.Color("#83a598"),
		syntaxNameAttribute:   lipgloss.Color("#8ec07c"),
		syntaxNameClass:       lipgloss.Color("#fabd2f"),
		syntaxNameDecorator:   lipgloss.Color("#d3869b"),
		syntaxLiteralString:   lipgloss.Color("#b8bb26"),
	})
}

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
