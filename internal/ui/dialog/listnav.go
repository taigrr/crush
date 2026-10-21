package dialog

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
)

// wrapSelectable is the subset of list behavior needed for wrap-around
// keyboard navigation in dialog lists.
type wrapSelectable interface {
	IsSelectedFirst() bool
	IsSelectedLast() bool
	SelectFirst() bool
	SelectLast() bool
	SelectNext() bool
	SelectPrev() bool
	ScrollToSelected()
}

// selectPrevWrap moves the selection up one item, wrapping to the last
// item from the first, and scrolls it into view.
func selectPrevWrap(l wrapSelectable) {
	if l.IsSelectedFirst() {
		l.SelectLast()
	} else {
		l.SelectPrev()
	}
	l.ScrollToSelected()
}

// selectNextWrap moves the selection down one item, wrapping to the first
// item from the last, and scrolls it into view.
func selectNextWrap(l wrapSelectable) {
	if l.IsSelectedLast() {
		l.SelectFirst()
	} else {
		l.SelectNext()
	}
	l.ScrollToSelected()
}

// filterOnInput forwards msg to the filter input, re-filters the list with
// the new value, and resets the selection to the first match.
func filterOnInput(input *textinput.Model, l *list.FilterableList, msg tea.Msg) Action {
	var cmd tea.Cmd
	*input, cmd = input.Update(msg)
	l.SetFilter(input.Value())
	l.ScrollToTop()
	l.SetSelected(0)
	return ActionCmd{cmd}
}

// listDialogChromeHeight is the vertical space taken by a list dialog's
// title, help, and frame, i.e. everything except the list itself.
func listDialogChromeHeight(t *styles.Styles) int {
	return t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
}

// filterDialogChromeHeight is listDialogChromeHeight plus the filter input.
func filterDialogChromeHeight(t *styles.Styles) int {
	return listDialogChromeHeight(t) +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight
}
