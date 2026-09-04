package styles

import (
	"image/color"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"github.com/taigrr/crush/internal/ui/diffview"
)

// quickStyleOpts is the palette of colors used by quickStyle to simplify the
// process of building a theme.
type QuickStyleOpts struct {
	// Brand.
	Primary   color.Color
	Secondary color.Color
	Accent    color.Color
	Keyword   color.Color

	// Default foreground and background colors.
	FgBase color.Color
	BgBase color.Color

	// Low-contrast dividers, separators, and rule lines.
	Separator color.Color

	FgSubtle     color.Color
	FgMoreSubtle color.Color
	FgMostSubtle color.Color

	// Contrast pairings: foregrounds designed to sit on top of a
	// matching background role.
	OnPrimary color.Color // foreground on primary backgrounds.

	BgMostVisible  color.Color
	BgLessVisible  color.Color
	BgLeastVisible color.Color

	// Statuses.
	Destructive       color.Color
	Error             color.Color
	Warning           color.Color
	WarningSubtle     color.Color
	Denied            color.Color
	Busy              color.Color
	Info              color.Color
	InfoMoreSubtle    color.Color
	InfoMostSubtle    color.Color
	Success           color.Color
	SuccessMoreSubtle color.Color
	SuccessMostSubtle color.Color

	// Diff view. Add/remove get a foreground (line number + symbol), an
	// emphasis background (line-number gutter) and a body background (code).
	DiffAddFg        color.Color
	DiffAddBg        color.Color // code/symbol background
	DiffAddBgEmph    color.Color // line-number gutter background
	DiffRemoveFg     color.Color
	DiffRemoveBg     color.Color
	DiffRemoveBgEmph color.Color

	// Diff-derived text outside the diff view: the file list's +/- counts
	// and markdown diff blocks. Both optional; when nil they cascade to the
	// status ramp, which is what most themes want since their success/error
	// colors are already green/red. Themes whose status ramp is not
	// green/red (grayscale ones, say) set these so add/remove stays
	// legible as a diff.
	DiffAddText    color.Color
	DiffRemoveText color.Color

	// Brand accent used for the Hypercredit icon/count.
	Hypercredit color.Color

	// Syntax highlighting roles (chroma) and markdown link/image colors that
	// don't map cleanly onto the status/foreground ramps.
	SyntaxLink            color.Color
	SyntaxImage           color.Color
	SyntaxCommentPreproc  color.Color
	SyntaxKeywordReserved color.Color
	SyntaxKeywordType     color.Color
	SyntaxOperator        color.Color
	SyntaxNameBuiltin     color.Color
	SyntaxNameTag         color.Color
	SyntaxNameAttribute   color.Color
	SyntaxNameClass       color.Color
	SyntaxNameDecorator   color.Color
	SyntaxLiteralString   color.Color

	// Brand surfaces. All optional; when nil they cascade to the brand
	// pair below. This lets community themes give the header / logo /
	// gradients distinct colors without forcing every theme to specify them.
	//
	//   headerCharm     → "Charm™" label, Logo.Charm/SmallCharm/TitleColorA
	//                      Default: secondary.
	//   headerDiagonals → ╱ separators, Logo.Field/Version/SmallDiagonals/TitleColorB
	//                      Default: primary.
	//   logoGradFrom/To → header logo wordmark gradient + Logo.SmallGrad*
	//                      Default: secondary → primary.
	//   workingGradFrom/To → animated "thinking" indicator gradient
	//                         Default: primary → secondary.
	HeaderCharm     color.Color
	HeaderDiagonals color.Color
	LogoGradFrom    color.Color
	LogoGradTo      color.Color
	WorkingGradFrom color.Color
	WorkingGradTo   color.Color
}

// orColor returns a if non-nil, otherwise b. Used to cascade optional brand
// tokens to their default brand pair.
func orColor(a, b color.Color) color.Color {
	if a == nil {
		return b
	}
	return a
}

// quickStyle builds the default Styles (that is, the default theme, Charmtone
// Pantera) from a palette of semi-semanticly-named colors.
//
// The idea here is that you can do most of the work on a theme with quickStyle,
// then add overrides as needed.
func QuickStyle(o QuickStyleOpts) Styles {
	var (
		base   = lipgloss.NewStyle().Foreground(o.FgBase)
		muted  = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
		subtle = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
		s      Styles
	)

	// Cascade optional brand-surface tokens to the brand pair so themes
	// that don't override stay visually identical.
	headerCharm := orColor(o.HeaderCharm, o.Secondary)
	headerDiagonals := orColor(o.HeaderDiagonals, o.Primary)
	logoGradFrom := orColor(o.LogoGradFrom, o.Secondary)
	logoGradTo := orColor(o.LogoGradTo, o.Primary)
	workingGradFrom := orColor(o.WorkingGradFrom, o.Primary)
	workingGradTo := orColor(o.WorkingGradTo, o.Secondary)

	// Diff-derived text keeps its historical per-surface status defaults,
	// so themes that don't set these are unaffected.
	diffAddText := orColor(o.DiffAddText, o.SuccessMostSubtle)
	filesDeletions := orColor(o.DiffRemoveText, o.Error)
	genericDeleted := orColor(o.DiffRemoveText, o.Destructive)

	s.Background = o.BgBase

	// Populate color fields
	s.WorkingGradFromColor = workingGradFrom
	s.WorkingGradToColor = workingGradTo
	s.WorkingLabelColor = o.FgBase

	s.TextInput = textinput.Styles{
		Focused: textinput.StyleState{
			Text:        base,
			Placeholder: base.Foreground(o.FgMostSubtle),
			Prompt:      base.Foreground(o.Accent),
			Suggestion:  base.Foreground(o.FgMostSubtle),
		},
		Blurred: textinput.StyleState{
			Text:        base.Foreground(o.FgMoreSubtle),
			Placeholder: base.Foreground(o.FgMostSubtle),
			Prompt:      base.Foreground(o.FgMoreSubtle),
			Suggestion:  base.Foreground(o.FgMostSubtle),
		},
		Cursor: textinput.CursorStyle{
			Color: o.Secondary,
			Shape: tea.CursorBlock,
			Blink: true,
		},
	}

	s.Editor.Textarea = textarea.Styles{
		Focused: textarea.StyleState{
			Base:             base,
			Text:             base,
			LineNumber:       base.Foreground(o.FgMostSubtle),
			CursorLine:       base,
			CursorLineNumber: base.Foreground(o.FgMostSubtle),
			Placeholder:      base.Foreground(o.FgMostSubtle),
			Prompt:           base.Foreground(o.Accent),
		},
		Blurred: textarea.StyleState{
			Base:             base,
			Text:             base.Foreground(o.FgMoreSubtle),
			LineNumber:       base.Foreground(o.FgMoreSubtle),
			CursorLine:       base,
			CursorLineNumber: base.Foreground(o.FgMoreSubtle),
			Placeholder:      base.Foreground(o.FgMostSubtle),
			Prompt:           base.Foreground(o.FgMoreSubtle),
		},
		Cursor: textarea.CursorStyle{
			Color: o.Secondary,
			Shape: tea.CursorBlock,
			Blink: true,
		},
	}

	s.Markdown = ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				// BlockPrefix: "\n",
				// BlockSuffix: "\n",
				Color: hex(o.FgSubtle),
			},
			// Margin: new(uint(defaultMargin)),
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
			Indent:         new(uint(1)),
			IndentToken:    new("│ "),
		},
		List: ansi.StyleList{
			LevelIndent: defaultListIndent,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix: "\n",
				Color:       hex(o.Info),
				Bold:        new(true),
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           hex(o.OnPrimary),
				BackgroundColor: hex(o.Primary),
				Bold:            new(true),
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "## ",
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "### ",
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "#### ",
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "##### ",
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "###### ",
				Color:  hex(o.SuccessMostSubtle),
				Bold:   new(false),
			},
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut: new(true),
		},
		Emph: ansi.StylePrimitive{
			Italic: new(true),
		},
		Strong: ansi.StylePrimitive{
			Bold: new(true),
		},
		HorizontalRule: ansi.StylePrimitive{
			Color:  hex(o.Separator),
			Format: "\n--------\n",
		},
		Item: ansi.StylePrimitive{
			BlockPrefix: "• ",
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
		},
		Task: ansi.StyleTask{
			StylePrimitive: ansi.StylePrimitive{},
			Ticked:         "[✓] ",
			Unticked:       "[ ] ",
		},
		Link: ansi.StylePrimitive{
			Color:     hex(o.SyntaxLink),
			Underline: new(true),
		},
		LinkText: ansi.StylePrimitive{
			Color: hex(o.SuccessMostSubtle),
			Bold:  new(true),
		},
		Image: ansi.StylePrimitive{
			Color:     hex(o.SyntaxImage),
			Underline: new(true),
		},
		ImageText: ansi.StylePrimitive{
			Color:  hex(o.FgMoreSubtle),
			Format: "Image: {{.text}} →",
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           hex(o.Destructive),
				BackgroundColor: hex(o.BgLessVisible),
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: hex(o.BgLessVisible),
				},
				Margin: new(uint(defaultMargin)),
			},
			Chroma: &ansi.Chroma{
				Text: ansi.StylePrimitive{
					Color: hex(o.FgSubtle),
				},
				Error: ansi.StylePrimitive{
					Color:           hex(o.OnPrimary),
					BackgroundColor: hex(o.Error),
				},
				Comment: ansi.StylePrimitive{
					Color: hex(o.FgMostSubtle),
				},
				CommentPreproc: ansi.StylePrimitive{
					Color: hex(o.SyntaxCommentPreproc),
				},
				Keyword: ansi.StylePrimitive{
					Color: hex(o.Info),
				},
				KeywordReserved: ansi.StylePrimitive{
					Color: hex(o.SyntaxKeywordReserved),
				},
				KeywordNamespace: ansi.StylePrimitive{
					Color: hex(o.SyntaxKeywordReserved),
				},
				KeywordType: ansi.StylePrimitive{
					Color: hex(o.SyntaxKeywordType),
				},
				Operator: ansi.StylePrimitive{
					Color: hex(o.SyntaxOperator),
				},
				Punctuation: ansi.StylePrimitive{
					Color: hex(o.WarningSubtle),
				},
				Name: ansi.StylePrimitive{
					Color: hex(o.FgSubtle),
				},
				NameBuiltin: ansi.StylePrimitive{
					Color: hex(o.SyntaxNameBuiltin),
				},
				NameTag: ansi.StylePrimitive{
					Color: hex(o.SyntaxNameTag),
				},
				NameAttribute: ansi.StylePrimitive{
					Color: hex(o.SyntaxNameAttribute),
				},
				NameClass: ansi.StylePrimitive{
					Color:     hex(o.SyntaxNameClass),
					Underline: new(true),
					Bold:      new(true),
				},
				NameDecorator: ansi.StylePrimitive{
					Color: hex(o.SyntaxNameDecorator),
				},
				NameFunction: ansi.StylePrimitive{
					Color: hex(o.SuccessMostSubtle),
				},
				LiteralNumber: ansi.StylePrimitive{
					Color: hex(o.Success),
				},
				LiteralString: ansi.StylePrimitive{
					Color: hex(o.SyntaxLiteralString),
				},
				LiteralStringEscape: ansi.StylePrimitive{
					Color: hex(o.SuccessMoreSubtle),
				},
				GenericDeleted: ansi.StylePrimitive{
					Color: hex(genericDeleted),
				},
				GenericEmph: ansi.StylePrimitive{
					Italic: new(true),
				},
				GenericInserted: ansi.StylePrimitive{
					Color: hex(diffAddText),
				},
				GenericStrong: ansi.StylePrimitive{
					Bold: new(true),
				},
				GenericSubheading: ansi.StylePrimitive{
					Color: hex(o.FgMoreSubtle),
				},
				Background: ansi.StylePrimitive{
					BackgroundColor: hex(o.BgLessVisible),
				},
			},
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{},
			},
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix: "\n ",
		},
	}

	// QuietMarkdown style - muted colors on subtle background for thinking content.
	plainBg := hex(o.BgLeastVisible)
	plainFg := hex(o.FgMoreSubtle)
	s.QuietMarkdown = ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
			Indent:      new(uint(1)),
			IndentToken: new("│ "),
		},
		List: ansi.StyleList{
			LevelIndent: defaultListIndent,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix:     "\n",
				Bold:            new(true),
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Bold:            new(true),
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "## ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "#### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "##### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "###### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut:      new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Emph: ansi.StylePrimitive{
			Italic:          new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Strong: ansi.StylePrimitive{
			Bold:            new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		HorizontalRule: ansi.StylePrimitive{
			Format:          "\n--------\n",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Item: ansi.StylePrimitive{
			BlockPrefix:     "• ",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix:     ". ",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Task: ansi.StyleTask{
			StylePrimitive: ansi.StylePrimitive{
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
			Ticked:   "[✓] ",
			Unticked: "[ ] ",
		},
		Link: ansi.StylePrimitive{
			Underline:       new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		LinkText: ansi.StylePrimitive{
			Bold:            new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Image: ansi.StylePrimitive{
			Underline:       new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		ImageText: ansi.StylePrimitive{
			Format:          "Image: {{.text}} →",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color:           plainFg,
					BackgroundColor: plainBg,
				},
				Margin: new(uint(defaultMargin)),
			},
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color:           plainFg,
					BackgroundColor: plainBg,
				},
			},
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix:     "\n ",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
	}

	s.Help = help.Styles{
		ShortKey:       base.Foreground(o.FgMoreSubtle),
		ShortDesc:      base.Foreground(o.FgMostSubtle),
		ShortSeparator: base.Foreground(o.Separator),
		Ellipsis:       base.Foreground(o.Separator),
		FullKey:        base.Foreground(o.FgMoreSubtle),
		FullDesc:       base.Foreground(o.FgMostSubtle),
		FullSeparator:  base.Foreground(o.Separator),
	}

	s.Diff = diffview.Style{
		DividerLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.FgSubtle).
				Background(o.BgLeastVisible),
			Code: lipgloss.NewStyle().
				Foreground(o.FgSubtle).
				Background(o.BgLeastVisible),
		},
		MissingLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Background(o.BgLeastVisible),
			Code: lipgloss.NewStyle().
				Background(o.BgLeastVisible),
		},
		EqualLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.FgMoreSubtle).
				Background(o.BgBase),
			Code: lipgloss.NewStyle().
				Foreground(o.FgMoreSubtle).
				Background(o.BgBase),
		},
		InsertLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.DiffAddFg).
				Background(o.DiffAddBgEmph),
			Symbol: lipgloss.NewStyle().
				Foreground(o.DiffAddFg).
				Background(o.DiffAddBg),
			Code: lipgloss.NewStyle().
				Background(o.DiffAddBg),
		},
		DeleteLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.DiffRemoveFg).
				Background(o.DiffRemoveBgEmph),
			Symbol: lipgloss.NewStyle().
				Foreground(o.DiffRemoveFg).
				Background(o.DiffRemoveBg),
			Code: lipgloss.NewStyle().
				Background(o.DiffRemoveBg),
		},
		Filename: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.FgSubtle).
				Background(o.BgLeastVisible),
			Code: lipgloss.NewStyle().
				Foreground(o.FgSubtle).
				Background(o.BgLeastVisible),
		},
	}

	s.FilePicker = filepicker.Styles{
		DisabledCursor:   base.Foreground(o.FgMoreSubtle),
		Cursor:           base.Foreground(o.FgBase),
		Symlink:          base.Foreground(o.FgMostSubtle),
		Directory:        base.Foreground(o.Primary),
		File:             base.Foreground(o.FgBase),
		DisabledFile:     base.Foreground(o.FgMoreSubtle),
		DisabledSelected: base.Background(o.BgMostVisible).Foreground(o.FgMoreSubtle),
		Permission:       base.Foreground(o.FgMoreSubtle),
		Selected:         base.Background(o.Primary).Foreground(o.FgBase),
		FileSize:         base.Foreground(o.FgMoreSubtle),
		EmptyDirectory:   base.Foreground(o.FgMoreSubtle).PaddingLeft(2).SetString("Empty directory"),
	}

	// borders
	s.ToolCallSuccess = lipgloss.NewStyle().Foreground(o.Success).SetString(ToolSuccess)

	s.Header.Charm = base.Foreground(headerCharm)
	s.Header.Diagonals = base.Foreground(headerDiagonals)
	s.Header.Percentage = muted
	s.Header.Hypercredit = base.Foreground(o.Hypercredit)
	s.Header.Keystroke = muted
	s.Header.KeystrokeTip = subtle
	s.Header.WorkingDir = muted
	s.Header.Separator = subtle
	s.Header.Wrapper = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Header.LogoGradCanvas = lipgloss.NewStyle()
	s.Header.LogoGradFromColor = logoGradFrom
	s.Header.LogoGradToColor = logoGradTo

	s.CompactDetails.Title = base
	s.CompactDetails.View = base.Padding(0, 1, 1, 1).Border(lipgloss.RoundedBorder()).BorderForeground(o.Primary)
	s.CompactDetails.Version = lipgloss.NewStyle().Foreground(o.Separator)

	// Tool rendering styles
	s.Tool.IconPending = base.Foreground(o.SuccessMostSubtle).SetString(ToolPending)
	s.Tool.IconSuccess = base.Foreground(o.Success).SetString(ToolSuccess)
	s.Tool.IconError = base.Foreground(o.Error).SetString(ToolError)
	s.Tool.IconCancelled = muted.SetString(ToolPending)

	s.Tool.NameNormal = base.Foreground(o.Info)
	s.Tool.NameNested = base.Foreground(o.Info)

	s.Tool.ParamMain = subtle
	s.Tool.ParamKey = subtle

	// Content rendering - prepared styles that accept width parameter
	s.Tool.ContentLine = muted.Background(o.BgLeastVisible)
	s.Tool.ContentTruncation = muted.Background(o.BgLeastVisible)
	s.Tool.ContentCodeLine = base.Background(o.BgBase).PaddingLeft(2)
	s.Tool.ContentCodeTruncation = muted.Background(o.BgBase).PaddingLeft(2)
	s.Tool.ContentCodeBg = o.BgBase
	s.Tool.Body = base.PaddingLeft(2)

	// Deprecated - kept for backward compatibility
	s.Tool.ContentBg = muted.Background(o.BgLeastVisible)
	s.Tool.ContentText = muted
	s.Tool.ContentLineNumber = base.Foreground(o.FgMoreSubtle).Background(o.BgBase).PaddingRight(1).PaddingLeft(1)

	s.Tool.StateWaiting = base.Foreground(o.FgMostSubtle)
	s.Tool.StateCancelled = base.Foreground(o.FgMostSubtle)
	s.Tool.HintKey = base.Foreground(o.FgMoreSubtle)
	s.Tool.HintText = base.Foreground(o.FgMostSubtle)

	s.Tool.ErrorTag = base.Padding(0, 1).Background(o.Destructive).Foreground(o.OnPrimary)
	s.Tool.ErrorMessage = base.Foreground(o.FgSubtle)

	s.Tool.WarnTag = base.Padding(0, 1).Background(o.Denied).Foreground(o.BgBase).Bold(true)
	s.Tool.WarnMessage = base.Foreground(o.FgSubtle)

	// Diff and multi-edit styles
	s.Tool.DiffTruncation = muted.Background(o.BgLeastVisible).PaddingLeft(2)
	s.Tool.NoteTag = base.Padding(0, 1).Background(o.Info).Foreground(o.OnPrimary)
	s.Tool.NoteMessage = base.Foreground(o.FgSubtle)

	// Job header styles
	s.Tool.JobIconPending = base.Foreground(o.SuccessMostSubtle)
	s.Tool.JobIconError = base.Foreground(o.Error)
	s.Tool.JobIconSuccess = base.Foreground(o.Success)
	s.Tool.JobToolName = base.Foreground(o.Info)
	s.Tool.JobAction = base.Foreground(o.InfoMostSubtle)
	s.Tool.JobPID = muted
	s.Tool.JobDescription = subtle

	// Agent task styles
	s.Tool.AgentTaskTag = base.Bold(true).Padding(0, 1).MarginLeft(2).Background(o.InfoMoreSubtle).Foreground(o.OnPrimary)
	s.Tool.AgentPrompt = muted

	// Agentic fetch styles
	s.Tool.AgenticFetchPromptTag = base.Bold(true).Padding(0, 1).MarginLeft(2).Background(o.Success).Foreground(o.Separator)

	// Todo styles
	s.Tool.TodoRatio = base.Foreground(o.InfoMostSubtle)
	s.Tool.TodoCompletedIcon = base.Foreground(o.Success)
	s.Tool.TodoInProgressIcon = base.Foreground(o.SuccessMostSubtle)
	s.Tool.TodoPendingIcon = base.Foreground(o.FgMoreSubtle)
	s.Tool.TodoStatusNote = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Tool.TodoItem = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Tool.TodoJustStarted = lipgloss.NewStyle().Foreground(o.FgBase)

	// MCP styles
	s.Tool.MCPName = base.Foreground(o.Info)
	s.Tool.MCPToolName = base.Foreground(o.InfoMostSubtle)
	s.Tool.MCPArrow = base.Foreground(o.Info).SetString(ArrowRightIcon)

	// Loading indicators for images, skills
	s.Tool.ResourceLoadedText = base.Foreground(o.Success)
	s.Tool.ResourceLoadedIndicator = base.Foreground(o.SuccessMostSubtle)
	s.Tool.ResourceName = base
	s.Tool.MediaType = base
	s.Tool.ResourceSize = base.Foreground(o.FgMoreSubtle)

	// Hook styles
	s.Tool.HookLabel = base.Foreground(o.SuccessMoreSubtle)
	s.Tool.HookName = base
	s.Tool.HookMatcher = base.Foreground(o.FgMoreSubtle)
	s.Tool.HookArrow = base.Foreground(o.SuccessMoreSubtle)
	s.Tool.HookDetail = base.Foreground(o.FgMoreSubtle)
	s.Tool.HookOK = base.Foreground(o.SuccessMostSubtle)
	s.Tool.HookDenied = base.Foreground(o.Error)
	s.Tool.HookDeniedLabel = base.Foreground(o.Destructive)
	s.Tool.HookDeniedReason = base.Foreground(o.BgMostVisible)
	s.Tool.HookRewrote = base.Foreground(o.BgMostVisible)

	// Tool-call action verbs and result-list styling.
	s.Tool.ActionCreate = lipgloss.NewStyle().Foreground(o.SuccessMoreSubtle)
	s.Tool.ActionDestroy = lipgloss.NewStyle().Foreground(o.Destructive)
	s.Tool.ResultEmpty = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Tool.ResultTruncation = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Tool.ResultItemName = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Tool.ResultItemDesc = lipgloss.NewStyle().Foreground(o.FgMostSubtle)

	// Buttons
	s.Button.Focused = lipgloss.NewStyle().Foreground(o.OnPrimary).Background(o.Secondary)
	s.Button.Blurred = lipgloss.NewStyle().Foreground(o.FgBase).Background(o.BgLessVisible)

	// Editor
	s.Editor.PromptNormalIconFocused = lipgloss.NewStyle().Foreground(o.SuccessMostSubtle).SetString(" > ")
	s.Editor.PromptNormalIconBlurred = s.Editor.PromptNormalIconFocused.Foreground(o.FgMoreSubtle)
	s.Editor.PromptBangIconFocused = lipgloss.NewStyle().Foreground(o.WarningSubtle).Bold(true).SetString(" $ ")
	s.Editor.PromptBangIconBlurred = s.Editor.PromptBangIconFocused.UnsetBold().Foreground(o.FgMoreSubtle)
	s.Editor.PromptNormalFocused = lipgloss.NewStyle().Foreground(o.SuccessMostSubtle).SetString("::: ")
	s.Editor.PromptNormalBlurred = s.Editor.PromptNormalFocused.Foreground(o.FgMoreSubtle)
	s.Editor.PromptYoloIconFocused = lipgloss.NewStyle().MarginRight(1).Foreground(o.FgMostSubtle).Background(o.Busy).Bold(true).SetString(" ! ")
	s.Editor.PromptYoloIconBlurred = s.Editor.PromptYoloIconFocused.Foreground(o.BgBase).Background(o.FgMoreSubtle)
	s.Editor.PromptYoloDotsFocused = lipgloss.NewStyle().MarginRight(1).Foreground(o.WarningSubtle).SetString(":::")
	s.Editor.PromptYoloDotsBlurred = s.Editor.PromptYoloDotsFocused.Foreground(o.FgMoreSubtle)

	s.Radio.On = lipgloss.NewStyle().Foreground(o.FgSubtle).SetString(RadioOn)
	s.Radio.Off = lipgloss.NewStyle().Foreground(o.FgSubtle).SetString(RadioOff)
	s.Radio.Label = lipgloss.NewStyle().Foreground(o.FgSubtle)

	// Logo
	s.Logo.FieldColor = headerDiagonals
	s.Logo.TitleColorA = headerCharm
	s.Logo.TitleColorB = headerDiagonals
	s.Logo.CharmColor = headerCharm
	s.Logo.VersionColor = headerDiagonals
	s.Logo.SmallCharm = lipgloss.NewStyle().Foreground(headerCharm)
	s.Logo.SmallDiagonals = lipgloss.NewStyle().Foreground(headerDiagonals)
	s.Logo.GradCanvas = lipgloss.NewStyle()
	s.Logo.SmallGradFromColor = logoGradFrom
	s.Logo.SmallGradToColor = logoGradTo

	// Section
	s.Section.Title = subtle
	s.Section.Line = base.Foreground(o.Separator)

	// Initialize
	s.Initialize.Header = base
	s.Initialize.Content = muted
	s.Initialize.Accent = base.Foreground(o.SuccessMostSubtle)

	// ResourceGroup (LSP/MCP/skills sidebar lists).
	s.Resource.Heading = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Resource.Name = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Resource.StatusText = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Resource.OfflineIcon = lipgloss.NewStyle().Foreground(o.BgMostVisible).SetString("●")
	s.Resource.BusyIcon = s.Resource.OfflineIcon.Foreground(o.Busy)
	s.Resource.ErrorIcon = s.Resource.OfflineIcon.Foreground(o.Destructive)
	s.Resource.OnlineIcon = s.Resource.OfflineIcon.Foreground(o.SuccessMostSubtle)
	s.Resource.DisabledIcon = lipgloss.NewStyle().Foreground(o.FgMoreSubtle).SetString("●")
	s.Resource.AdditionalText = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Resource.CapabilityCount = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Resource.RowTitleBase = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Resource.RowDescBase = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Resource.DefaultTitleFg = o.FgMoreSubtle
	s.Resource.DefaultDescFg = o.FgMostSubtle

	// LSP
	s.LSP.ErrorDiagnostic = base.Foreground(o.Error)
	s.LSP.WarningDiagnostic = base.Foreground(o.WarningSubtle)
	s.LSP.HintDiagnostic = base.Foreground(o.FgSubtle)
	s.LSP.InfoDiagnostic = base.Foreground(o.Info)

	// Files
	s.Files.Path = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Files.Additions = lipgloss.NewStyle().Foreground(diffAddText)
	s.Files.Deletions = lipgloss.NewStyle().Foreground(filesDeletions)
	s.Files.SectionTitle = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Files.EmptyMessage = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Files.TruncationHint = lipgloss.NewStyle().Foreground(o.FgMostSubtle)

	// Sidebar
	s.Sidebar.SessionTitle = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Sidebar.WorkingDir = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)

	// ModelInfo
	s.ModelInfo.Icon = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.ModelInfo.Name = lipgloss.NewStyle().Foreground(o.FgBase)
	s.ModelInfo.Provider = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.ModelInfo.ProviderFallback = lipgloss.NewStyle().Foreground(o.FgMoreSubtle).PaddingLeft(2)
	s.ModelInfo.Reasoning = lipgloss.NewStyle().Foreground(o.FgMostSubtle).PaddingLeft(2)
	s.ModelInfo.TokenCount = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.ModelInfo.TokenPercentage = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.ModelInfo.EstimatedUsagePrefix = s.ModelInfo.TokenPercentage
	s.ModelInfo.Cost = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.ModelInfo.HypercreditIcon = lipgloss.NewStyle().Foreground(o.Hypercredit)
	s.ModelInfo.HypercreditText = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)

	// ResourceGroup
	s.Resource.DefaultTitleFg = o.FgMoreSubtle
	s.Resource.DefaultDescFg = o.FgMostSubtle

	// Chat
	messageFocussedBorder := lipgloss.Border{
		Left: "▌",
	}

	s.Messages.NoContent = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Messages.UserBlurred = s.Messages.NoContent.PaddingLeft(1).BorderLeft(true).
		BorderForeground(o.Primary).BorderStyle(lipgloss.NormalBorder())
	s.Messages.UserFocused = s.Messages.NoContent.PaddingLeft(1).BorderLeft(true).
		BorderForeground(o.Primary).BorderStyle(messageFocussedBorder)
	s.Messages.AssistantBlurred = s.Messages.NoContent.PaddingLeft(2)
	s.Messages.AssistantFocused = s.Messages.NoContent.PaddingLeft(1).BorderLeft(true).
		BorderForeground(o.SuccessMostSubtle).BorderStyle(messageFocussedBorder)
	s.Messages.Thinking = lipgloss.NewStyle().MaxHeight(10)
	s.Messages.ErrorTag = lipgloss.NewStyle().Padding(0, 1).
		Background(o.Destructive).Foreground(o.OnPrimary)
	s.Messages.ErrorTitle = lipgloss.NewStyle().Foreground(o.FgSubtle)
	s.Messages.ErrorDetails = lipgloss.NewStyle().Foreground(o.FgMostSubtle)

	// Message item styles
	s.Messages.ToolCallFocused = muted.PaddingLeft(1).
		BorderStyle(messageFocussedBorder).
		BorderLeft(true).
		BorderForeground(o.SuccessMostSubtle)
	s.Messages.ToolCallBlurred = muted.PaddingLeft(2)
	// No padding or border for compact tool calls within messages
	s.Messages.ToolCallCompact = muted
	s.Messages.SectionHeader = base.PaddingLeft(2)
	s.Messages.AssistantInfoIcon = subtle
	s.Messages.AssistantInfoModel = muted
	s.Messages.AssistantInfoProvider = subtle
	s.Messages.AssistantInfoDuration = subtle
	s.Messages.AssistantCanceled = lipgloss.NewStyle().Foreground(o.FgBase).Italic(true)

	// Thinking section styles
	s.Messages.ThinkingBox = subtle.Background(o.BgLeastVisible)
	s.Messages.ThinkingTruncationHint = muted
	s.Messages.ThinkingFooterTitle = muted
	s.Messages.ThinkingFooterDuration = subtle

	// Text selection.
	s.TextSelection = lipgloss.NewStyle().Foreground(o.OnPrimary).Background(o.Primary)

	// Dialog styles
	s.Dialog.Title = base.Padding(0, 1).Foreground(o.Primary)
	s.Dialog.TitleText = base.Foreground(o.Primary)
	s.Dialog.TitleError = base.Foreground(o.Destructive)
	s.Dialog.TitleAccent = base.Foreground(o.Success).Bold(true)
	s.Dialog.TitleLineBase = lipgloss.NewStyle()
	s.Dialog.TitleGradFromColor = o.Primary
	s.Dialog.TitleGradToColor = o.Secondary

	// Dialog.ListItem (commands, reasoning, models)
	s.Dialog.ListItem.InfoBlurred = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Dialog.ListItem.InfoFocused = lipgloss.NewStyle().Foreground(o.FgBase)

	// Dialog.Models
	s.Dialog.Models.ConfiguredText = lipgloss.NewStyle().Foreground(o.FgMostSubtle)

	// Dialog.Permissions
	s.Dialog.Permissions.KeyText = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Dialog.Permissions.ValueText = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Dialog.Permissions.ParamsBg = o.BgLessVisible

	// Dialog.Quit
	s.Dialog.Quit.Content = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Dialog.Quit.Frame = lipgloss.NewStyle().BorderForeground(o.Primary).Border(lipgloss.RoundedBorder()).Padding(1, 2)
	s.Dialog.View = base.Border(lipgloss.RoundedBorder()).BorderForeground(o.Primary)
	s.Dialog.PrimaryText = base.Padding(0, 1).Foreground(o.Primary)
	s.Dialog.SecondaryText = base.Padding(0, 1).Foreground(o.FgMostSubtle)
	s.Dialog.HelpView = base.Padding(0, 1).AlignHorizontal(lipgloss.Left)
	s.Dialog.Help.ShortKey = base.Foreground(o.FgMoreSubtle)
	s.Dialog.Help.ShortDesc = base.Foreground(o.FgMostSubtle)
	s.Dialog.Help.ShortSeparator = base.Foreground(o.Separator)
	s.Dialog.Help.Ellipsis = base.Foreground(o.Separator)
	s.Dialog.Help.FullKey = base.Foreground(o.FgMoreSubtle)
	s.Dialog.Help.FullDesc = base.Foreground(o.FgMostSubtle)
	s.Dialog.Help.FullSeparator = base.Foreground(o.Separator)
	s.Dialog.NormalItem = base.Padding(0, 1).Foreground(o.FgBase)
	s.Dialog.SelectedItem = base.Padding(0, 1).Background(o.Primary).Foreground(o.OnPrimary)
	s.Dialog.InputPrompt = base.Margin(1, 1)

	s.Dialog.List = base.Margin(0, 0, 1, 0)
	s.Dialog.ContentPanel = base.Background(o.BgLessVisible).Foreground(o.FgBase).Padding(1, 2)
	s.Dialog.Spinner = base.Foreground(o.Secondary)
	s.Dialog.ScrollbarThumb = base.Foreground(o.Secondary)
	s.Dialog.ScrollbarTrack = base.Foreground(o.Separator)

	s.Dialog.ImagePreview = lipgloss.NewStyle().Padding(0, 1).Foreground(o.FgMostSubtle)

	// API key input dialog
	s.Dialog.APIKey.Spinner = base.Foreground(o.Success)

	// OAuth dialog
	s.Dialog.OAuth.Spinner = base.Foreground(o.SuccessMoreSubtle)
	s.Dialog.OAuth.Instructions = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Dialog.OAuth.UserCode = lipgloss.NewStyle().Bold(true).Foreground(o.FgBase)
	s.Dialog.OAuth.Success = lipgloss.NewStyle().Foreground(o.SuccessMoreSubtle)
	s.Dialog.OAuth.Link = lipgloss.NewStyle().Foreground(o.SuccessMostSubtle).Underline(true)
	s.Dialog.OAuth.Enter = lipgloss.NewStyle().Foreground(o.Keyword)
	s.Dialog.OAuth.ErrorText = lipgloss.NewStyle().Foreground(o.Error)
	s.Dialog.OAuth.StatusText = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Dialog.OAuth.UserCodeBg = o.BgLeastVisible

	s.Dialog.Arguments.Content = base.Padding(1)
	s.Dialog.Arguments.Description = base.MarginBottom(1).MaxHeight(3)
	s.Dialog.Arguments.InputLabelBlurred = base.Foreground(o.FgMoreSubtle)
	s.Dialog.Arguments.InputLabelFocused = base.Bold(true)
	s.Dialog.Arguments.InputRequiredMarkBlurred = base.Foreground(o.FgMoreSubtle).SetString("*")
	s.Dialog.Arguments.InputRequiredMarkFocused = base.Foreground(o.Primary).Bold(true).SetString("*")

	s.Dialog.Sessions.DeletingTitle = s.Dialog.Title.Foreground(o.Destructive)
	s.Dialog.Sessions.DeletingView = s.Dialog.View.BorderForeground(o.Destructive)
	s.Dialog.Sessions.DeletingMessage = base.Padding(1)
	s.Dialog.Sessions.DeletingTitleGradientFromColor = o.Destructive
	s.Dialog.Sessions.DeletingTitleGradientToColor = o.Primary
	s.Dialog.Sessions.DeletingItemBlurred = s.Dialog.NormalItem.Foreground(o.FgMostSubtle)
	s.Dialog.Sessions.DeletingItemFocused = s.Dialog.SelectedItem.Background(o.Destructive).Foreground(o.OnPrimary)

	s.Dialog.Sessions.RenamingingTitle = s.Dialog.Title.Foreground(o.WarningSubtle)
	s.Dialog.Sessions.RenamingView = s.Dialog.View.BorderForeground(o.WarningSubtle)
	s.Dialog.Sessions.RenamingingMessage = base.Padding(1)
	s.Dialog.Sessions.RenamingTitleGradientFromColor = o.WarningSubtle
	s.Dialog.Sessions.RenamingTitleGradientToColor = o.Accent
	s.Dialog.Sessions.RenamingItemBlurred = s.Dialog.NormalItem.Foreground(o.FgMostSubtle)
	s.Dialog.Sessions.RenamingingItemFocused = s.Dialog.SelectedItem.UnsetBackground().UnsetForeground()
	s.Dialog.Sessions.RenamingPlaceholder = base.Foreground(o.FgMoreSubtle)

	s.Dialog.Sessions.ArchivingTitle = s.Dialog.Title.Foreground(o.FgMoreSubtle)
	s.Dialog.Sessions.ArchivingView = s.Dialog.View.BorderForeground(o.FgMoreSubtle)
	s.Dialog.Sessions.ArchivingMessage = base.Padding(1)
	s.Dialog.Sessions.ArchivingTitleGradientFromColor = o.FgMoreSubtle
	s.Dialog.Sessions.ArchivingTitleGradientToColor = o.Primary
	s.Dialog.Sessions.ArchivingItemBlurred = s.Dialog.NormalItem.Foreground(o.FgMostSubtle)
	s.Dialog.Sessions.ArchivingItemFocused = s.Dialog.SelectedItem.Background(o.FgMoreSubtle).Foreground(o.OnPrimary)

	s.Dialog.Sessions.SeparatorStyle = base.Foreground(o.FgMostSubtle)

	s.Dialog.Sessions.InfoBlurred = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Dialog.Sessions.InfoFocused = lipgloss.NewStyle().Foreground(o.FgBase)

	s.Status.Help = lipgloss.NewStyle().Padding(0, 1)
	s.Status.SuccessIndicator = base.Foreground(o.BgLessVisible).Background(o.Success).Padding(0, 1).Bold(true).SetString("OKAY!")
	s.Status.InfoIndicator = s.Status.SuccessIndicator
	s.Status.UpdateIndicator = s.Status.SuccessIndicator.SetString("HEY!")
	s.Status.WarnIndicator = s.Status.SuccessIndicator.Foreground(o.BgMostVisible).Background(o.Warning).SetString("WARNING")
	s.Status.ErrorIndicator = s.Status.SuccessIndicator.Foreground(o.BgBase).Background(o.Destructive).SetString("ERROR")
	s.Status.SuccessMessage = base.Foreground(o.BgLessVisible).Background(o.SuccessMostSubtle).Padding(0, 1)
	s.Status.InfoMessage = s.Status.SuccessMessage
	s.Status.UpdateMessage = s.Status.SuccessMessage
	s.Status.WarnMessage = s.Status.SuccessMessage.Foreground(o.BgMostVisible).Background(o.WarningSubtle)
	s.Status.ErrorMessage = s.Status.SuccessMessage.Foreground(o.OnPrimary).Background(o.Error)

	// Completions styles
	s.Completions.Normal = base.Background(o.BgLessVisible).Foreground(o.FgBase)
	s.Completions.Focused = base.Background(o.Primary).Foreground(o.OnPrimary)
	s.Completions.Match = base.Underline(true)

	// Attachments styles
	attachmentIconStyle := base.Foreground(o.BgLessVisible).Background(o.Success).Padding(0, 1)
	s.Attachments.Image = attachmentIconStyle.SetString(ImageIcon)
	s.Attachments.Text = attachmentIconStyle.SetString(TextIcon)
	s.Attachments.Skill = attachmentIconStyle.SetString(SkillIcon)
	s.Attachments.Normal = base.Padding(0, 1).MarginRight(1).Background(o.FgMoreSubtle).Foreground(o.FgBase)
	s.Attachments.Deleting = base.Padding(0, 1).Bold(true).Background(o.Destructive).Foreground(o.FgBase)

	// Pills styles
	s.Pills.Base = base.Padding(0, 1)
	s.Pills.Focused = base.Padding(0, 1).BorderStyle(lipgloss.RoundedBorder()).BorderForeground(o.BgMostVisible)
	s.Pills.Blurred = base.Padding(0, 1).BorderStyle(lipgloss.HiddenBorder())
	s.Pills.QueueItemPrefix = lipgloss.NewStyle().Foreground(o.FgMoreSubtle).SetString("  •")
	s.Pills.QueueItemText = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Pills.QueueSteerTag = lipgloss.NewStyle().Foreground(o.Secondary)
	s.Pills.QueueLabel = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Pills.QueueIconBase = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Pills.QueueGradFromColor = o.Error
	s.Pills.QueueGradToColor = o.Secondary
	s.Pills.TodoLabel = lipgloss.NewStyle().Foreground(o.FgBase)
	s.Pills.TodoProgress = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Pills.TodoCurrentTask = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Pills.TodoSpinner = lipgloss.NewStyle().Foreground(o.SuccessMostSubtle)
	s.Pills.HelpKey = lipgloss.NewStyle().Foreground(o.FgMoreSubtle)
	s.Pills.HelpText = lipgloss.NewStyle().Foreground(o.FgMostSubtle)
	s.Pills.Area = base

	return s
}
