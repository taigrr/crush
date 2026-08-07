package dialog

import (
	"fmt"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
)

// SearchPaletteID is the identifier for the semantic search palette dialog.
const SearchPaletteID = "search_palette"

// SearchPalette is a global command-palette for semantic history search.
// The user types a query, the UI debounces keystrokes and runs a
// (hybrid substring + semantic) search across the workspace, and the
// results collapse to one row per session. Moving the selection
// previews the session; enter commits it. The dialog owns no IO: it
// emits actions the UI turns into debounced search commands and session
// loads, and the UI feeds results back via SetResults.
type SearchPalette struct {
	com   *common.Common
	help  help.Model
	list  *list.List
	input textinput.Model

	// semantic mirrors the requested semantic mode: nil = follow the
	// hybrid_search config default, non-nil = force on/off for the
	// current query. Toggled with the ToggleSemantic key.
	semantic *bool
	// semanticUsed reports whether the last returned result actually
	// used the semantic signal (false when disabled, no embedder, or it
	// errored), so the footer can show the effective mode.
	semanticUsed bool
	// loading reports whether a search is in flight, for the footer.
	loading bool
	query   string

	keyMap struct {
		Select         key.Binding
		Next           key.Binding
		Previous       key.Binding
		ToggleSemantic key.Binding
		Close          key.Binding
	}
}

var _ Dialog = (*SearchPalette)(nil)

// NewSearchPalette creates a new semantic search palette dialog.
func NewSearchPalette(com *common.Common) *SearchPalette {
	s := &SearchPalette{com: com}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	s.help = h

	s.list = list.NewList()
	s.list.RegisterRenderCallback(list.FocusedRenderCallback(s.list))
	s.list.Focus()

	s.input = textinput.New()
	s.input.SetVirtualCursor(false)
	s.input.Placeholder = "Search all sessions…"
	s.input.SetStyles(com.Styles.TextInput)
	s.input.Focus()

	s.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "tab", "ctrl+y"),
		key.WithHelp("enter", "open"),
	)
	s.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n", "ctrl+j"),
		key.WithHelp("↓", "next"),
	)
	s.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p", "ctrl+k"),
		key.WithHelp("↑", "previous"),
	)
	s.keyMap.ToggleSemantic = key.NewBinding(
		key.WithKeys("ctrl+o"),
		key.WithHelp("ctrl+o", "toggle semantic"),
	)
	s.keyMap.Close = CloseKey

	return s
}

// ID implements Dialog.
func (s *SearchPalette) ID() string {
	return SearchPaletteID
}

// HandleMsg implements Dialog. Navigation and selection are handled
// locally; query edits and the semantic toggle emit actions so the UI
// can drive the debounced search off the Update loop.
func (s *SearchPalette) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		return s.updateInput(msg)
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, s.keyMap.ToggleSemantic):
			s.toggleSemantic()
			return ActionSearchQueryChanged{Query: s.query, Semantic: s.semantic}
		case key.Matches(msg, s.keyMap.Previous):
			if s.list.IsSelectedFirst() {
				s.list.SelectLast()
			} else {
				s.list.SelectPrev()
			}
			s.list.ScrollToSelected()
			return s.previewAction()
		case key.Matches(msg, s.keyMap.Next):
			if s.list.IsSelectedLast() {
				s.list.SelectFirst()
			} else {
				s.list.SelectNext()
			}
			s.list.ScrollToSelected()
			return s.previewAction()
		case key.Matches(msg, s.keyMap.Select):
			if hit, ok := s.selectedHit(); ok {
				return ActionSelectSearchResult{Hit: hit}
			}
			return nil
		default:
			return s.updateInput(msg)
		}
	}
	return nil
}

// updateInput feeds a key/paste message to the query input and, only when
// the query text actually changed, emits ActionSearchQueryChanged so the
// UI schedules a debounced search. Cursor-only keys (arrows, home/end)
// therefore don't trigger redundant searches. Any input cmd is carried
// through so the input stays responsive.
func (s *SearchPalette) updateInput(msg tea.Msg) Action {
	prev := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	s.query = s.input.Value()
	if s.query == prev {
		if cmd != nil {
			return ActionCmd{Cmd: cmd}
		}
		return nil
	}
	return ActionSearchQueryChanged{Query: s.query, Semantic: s.semantic, InputCmd: cmd}
}

// toggleSemantic cycles the semantic override: config-default → off →
// on → config-default. This lets the user both force it on when the
// config leaves it off and force it off to compare.
func (s *SearchPalette) toggleSemantic() {
	switch {
	case s.semantic == nil:
		off := false
		s.semantic = &off
	case !*s.semantic:
		on := true
		s.semantic = &on
	default:
		s.semantic = nil
	}
}

// previewAction returns a preview action for the currently selected hit,
// or nil if there is no selection.
func (s *SearchPalette) previewAction() Action {
	if hit, ok := s.selectedHit(); ok {
		return ActionPreviewSearchResult{Hit: hit}
	}
	return nil
}

// selectedHit returns the hit backing the selected row.
func (s *SearchPalette) selectedHit() (proto.SessionHit, bool) {
	item := s.list.SelectedItem()
	if item == nil {
		return proto.SessionHit{}, false
	}
	if ri, ok := item.(*SearchResultItem); ok {
		return ri.hit, true
	}
	return proto.SessionHit{}, false
}

// SetLoading marks a search as in flight (or done) for the footer.
func (s *SearchPalette) SetLoading(loading bool) {
	s.loading = loading
}

// SetResults replaces the result list with per-session hits and resets
// the selection to the top. semanticUsed reflects whether the semantic
// signal participated in this result.
func (s *SearchPalette) SetResults(hits []proto.SessionHit, semanticUsed bool) {
	s.loading = false
	s.semanticUsed = semanticUsed
	items := make([]list.Item, 0, len(hits))
	for _, h := range hits {
		items = append(items, NewSearchResultItem(s.com, h))
	}
	s.list.SetItems(items...)
	if len(items) > 0 {
		s.list.SetSelected(0)
		s.list.ScrollToTop()
	} else {
		s.list.SetSelected(-1)
	}
}

// SelectedHit returns the currently selected hit, if any. The UI uses
// this to preview the initial selection after results arrive.
func (s *SearchPalette) SelectedHit() (proto.SessionHit, bool) {
	return s.selectedHit()
}

// Cursor returns the cursor position relative to the dialog.
func (s *SearchPalette) Cursor() *tea.Cursor {
	return InputCursor(s.com.Styles, s.input.Cursor())
}

// Draw implements Dialog.
func (s *SearchPalette) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
	s.input.SetWidth(max(0, innerWidth-t.Dialog.InputPrompt.GetHorizontalFrameSize()-1))
	listHeight := height - heightOffset
	listTotalHeight := s.list.TotalHeight()
	listWidth := max(0, innerWidth-3) // reserve space for scrollbar
	s.list.SetSize(listWidth, listHeight)
	s.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Search"
	rc.TitleInfo = s.titleInfo()

	inputView := t.Dialog.InputPrompt.Render(s.input.View())
	cur := s.Cursor()
	rc.AddPart(inputView)

	listView := t.Dialog.List.Height(s.list.Height()).Render(s.list.Render())
	scrollbar := common.Scrollbar(t, listHeight, listTotalHeight, listHeight, s.list.Offset())
	if scrollbar != "" {
		listView = lipgloss.JoinHorizontal(lipgloss.Top, listView, scrollbar)
	}
	rc.AddPart(listView)
	rc.Help = s.help.View(s)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// titleInfo renders the effective search mode next to the title so the
// user can see whether semantic matching is active and whether a search
// is running.
func (s *SearchPalette) titleInfo() string {
	t := s.com.Styles
	mode := "substring"
	if s.semanticUsed {
		mode = "hybrid"
	}
	if s.semantic != nil {
		if *s.semantic {
			mode = "semantic:on"
		} else {
			mode = "semantic:off"
		}
	}
	if s.loading {
		mode = "searching…"
	}
	return t.Dialog.Sessions.InfoBlurred.Render(fmt.Sprintf(" %s ", mode))
}

// ShortHelp implements help.KeyMap.
func (s *SearchPalette) ShortHelp() []key.Binding {
	return []key.Binding{
		s.keyMap.Select,
		s.keyMap.Next,
		s.keyMap.Previous,
		s.keyMap.ToggleSemantic,
		s.keyMap.Close,
	}
}

// FullHelp implements help.KeyMap.
func (s *SearchPalette) FullHelp() [][]key.Binding {
	return [][]key.Binding{s.ShortHelp()}
}
