package dialog

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// InputCursor adjusts the cursor position for an input field within a dialog.
func InputCursor(t *styles.Styles, cur *tea.Cursor) *tea.Cursor {
	if cur != nil {
		titleStyle := t.Dialog.Title
		dialogStyle := t.Dialog.View
		inputStyle := t.Dialog.InputPrompt
		// Adjust cursor position to account for dialog layout
		cur.X += inputStyle.GetBorderLeftSize() +
			inputStyle.GetMarginLeft() +
			inputStyle.GetPaddingLeft() +
			dialogStyle.GetBorderLeftSize() +
			dialogStyle.GetPaddingLeft() +
			dialogStyle.GetMarginLeft()
		cur.Y += titleStyle.GetVerticalFrameSize() +
			inputStyle.GetBorderTopSize() +
			inputStyle.GetMarginTop() +
			inputStyle.GetPaddingTop() +
			inputStyle.GetBorderBottomSize() +
			inputStyle.GetMarginBottom() +
			inputStyle.GetPaddingBottom() +
			dialogStyle.GetPaddingTop() +
			dialogStyle.GetMarginTop() +
			dialogStyle.GetBorderTopSize()
	}
	return cur
}

// RenderContext is a dialog rendering context that can be used to render
// common dialog layouts.
type RenderContext struct {
	// Styles is the styles to use for rendering.
	Styles *styles.Styles
	// Width is the total width of the dialog including any margins, borders,
	// and paddings.
	Width int
	// Title is the title of the dialog. This will be styled using the default
	// dialog title style and prepended to the content parts slice.
	Title string
	// Parts are the rendered parts of the dialog.
	Parts []string
	// Help is the help view content. This will be appended to the content parts
	// slice using the default dialog help style.
	Help string
}

// NewRenderContext creates a new RenderContext with the provided styles and width.
func NewRenderContext(t *styles.Styles, width int) *RenderContext {
	return &RenderContext{
		Styles: t,
		Width:  width,
		Parts:  []string{},
	}
}

// AddPart adds a rendered part to the dialog.
func (rc *RenderContext) AddPart(part string) {
	if len(part) > 0 {
		rc.Parts = append(rc.Parts, part)
	}
}

// Render renders the dialog using the provided context.
func (rc *RenderContext) Render() string {
	titleStyle := rc.Styles.Dialog.Title
	dialogStyle := rc.Styles.Dialog.View.Width(rc.Width)

	parts := []string{}
	if len(rc.Title) > 0 {
		title := common.DialogTitle(rc.Styles, rc.Title,
			max(0, rc.Width-dialogStyle.GetHorizontalFrameSize()-
				titleStyle.GetHorizontalFrameSize()))
		parts = append(parts, titleStyle.Render(title), "")
	}

	for i, p := range rc.Parts {
		if len(p) > 0 {
			parts = append(parts, p)
		}
		if i < len(rc.Parts)-1 {
			parts = append(parts, "")
		}
	}

	if len(rc.Help) > 0 {
		parts = append(parts, "")
		helpStyle := rc.Styles.Dialog.HelpView
		helpStyle = helpStyle.Width(rc.Width - dialogStyle.GetHorizontalFrameSize())
		parts = append(parts, helpStyle.Render(rc.Help))
	}

	content := strings.Join(parts, "\n")

	return dialogStyle.Render(content)
}

// HeaderInputListHelpView generates a view for dialogs with a header, input,
// list, and help sections.
func HeaderInputListHelpView(t *styles.Styles, width, listHeight int, header, input, list, help string) string {
	rc := NewRenderContext(t, width)

	titleStyle := t.Dialog.Title
	inputStyle := t.Dialog.InputPrompt
	listStyle := t.Dialog.List.Height(listHeight)
	listContent := listStyle.Render(list)

	if len(header) > 0 {
		rc.AddPart(titleStyle.Render(header))
	}
	if len(input) > 0 {
		rc.AddPart(inputStyle.Render(input))
	}
	if len(list) > 0 {
		rc.AddPart(listContent)
	}
	if len(help) > 0 {
		rc.Help = help
	}

	return rc.Render()
}
