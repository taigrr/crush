package dialog

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dustin/go-humanize"
	"github.com/sahilm/fuzzy"
	"github.com/taigrr/crush/internal/sessionimport"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/ui/util"
)

const SessionImportID = "session_import"

type sessionImportStage uint8

const (
	sessionImportSources sessionImportStage = iota
	sessionImportSessions
)

type sessionImportCandidatesMsg struct {
	source     sessionimport.SourceInfo
	candidates []sessionimport.Candidate
	err        error
}

type sessionImportDoneMsg struct {
	results []sessionimport.Result
	err     error
}

type SessionImport struct {
	com        *common.Common
	help       help.Model
	input      textinput.Model
	list       *list.FilterableList
	stage      sessionImportStage
	sources    []sessionimport.SourceInfo
	source     sessionimport.SourceInfo
	candidates []sessionimport.Candidate
	selection  common.MultiSelect
	items      []*sessionImportItem
	loading    bool

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Toggle   key.Binding
		All      key.Binding
		Import   key.Binding
		Back     key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*SessionImport)(nil)

func NewSessionImport(com *common.Common, sources []sessionimport.SourceInfo) *SessionImport {
	dialog := &SessionImport{com: com, sources: sources}
	dialog.help = help.New()
	dialog.help.Styles = com.Styles.DialogHelpStyles()
	dialog.input = textinput.New()
	dialog.input.SetVirtualCursor(false)
	dialog.input.Placeholder = "Search sessions"
	dialog.input.SetStyles(com.Styles.TextInput)
	dialog.input.Focus()
	dialog.list = list.NewFilterableList()
	dialog.list.Focus()
	dialog.keyMap.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))
	dialog.keyMap.Next = key.NewBinding(key.WithKeys("down", "ctrl+n", "j"), key.WithHelp("down", "next"))
	dialog.keyMap.Previous = key.NewBinding(key.WithKeys("up", "ctrl+p", "k"), key.WithHelp("up", "previous"))
	dialog.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("up/down", "navigate"))
	dialog.keyMap.Toggle = key.NewBinding(key.WithKeys("space", " "), key.WithHelp("space", "toggle"))
	dialog.keyMap.All = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all"))
	dialog.keyMap.Import = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "import"))
	dialog.keyMap.Back = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	dialog.keyMap.Close = CloseKey
	dialog.showSources()
	return dialog
}

func (s *SessionImport) ID() string { return SessionImportID }

func (s *SessionImport) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case sessionImportCandidatesMsg:
		s.loading = false
		if msg.err != nil {
			return ActionCmd{util.ReportError(msg.err)}
		}
		s.source = msg.source
		s.candidates = msg.candidates
		s.selection.Clear()
		s.stage = sessionImportSessions
		s.showCandidates()
		return nil
	case sessionImportDoneMsg:
		s.loading = false
		if msg.err != nil {
			return ActionCmd{util.ReportError(msg.err)}
		}
		return ActionSessionImportComplete{Results: msg.results}
	case tea.KeyPressMsg:
		if s.loading {
			if key.Matches(msg, s.keyMap.Close) {
				return ActionClose{}
			}
			return nil
		}
		if s.stage == sessionImportSessions {
			return s.handleSessionsKey(msg)
		}
		switch {
		case key.Matches(msg, s.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, s.keyMap.Previous):
			s.move(-1)
		case key.Matches(msg, s.keyMap.Next):
			s.move(1)
		case key.Matches(msg, s.keyMap.Select):
			item, ok := s.list.SelectedItem().(*sessionImportItem)
			if !ok {
				return nil
			}
			s.loading = true
			return ActionCmd{s.discoverCmd(item.source)}
		}
	}
	return nil
}

func (s *SessionImport) handleSessionsKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, s.keyMap.Back):
		s.stage = sessionImportSources
		s.loading = false
		s.showSources()
	case key.Matches(msg, s.keyMap.Previous):
		s.move(-1)
	case key.Matches(msg, s.keyMap.Next):
		s.move(1)
	case key.Matches(msg, s.keyMap.Toggle):
		if item, ok := s.list.SelectedItem().(*sessionImportItem); ok {
			s.selection.Toggle(item.ID())
			s.syncMarks()
		}
	case key.Matches(msg, s.keyMap.All):
		ids := make([]string, len(s.candidates))
		for index, candidate := range s.candidates {
			ids[index] = candidate.Path
		}
		if s.selection.Count() == len(ids) {
			s.selection.Clear()
		} else {
			s.selection.SetSelection(ids)
		}
		s.syncMarks()
	case key.Matches(msg, s.keyMap.Import):
		paths := s.selection.IDs()
		if len(paths) == 0 {
			if item, ok := s.list.SelectedItem().(*sessionImportItem); ok {
				paths = []string{item.ID()}
			}
		}
		if len(paths) == 0 {
			return ActionCmd{util.ReportInfo("No sessions available to import")}
		}
		s.loading = true
		return ActionCmd{s.importCmd(paths)}
	default:
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		s.list.SetFilter(s.input.Value())
		s.list.ScrollToTop()
		s.list.SetSelected(0)
		return ActionCmd{cmd}
	}
	return nil
}

func (s *SessionImport) move(delta int) {
	if delta < 0 {
		if s.list.IsSelectedFirst() {
			s.list.SelectLast()
		} else {
			s.list.SelectPrev()
		}
	} else if s.list.IsSelectedLast() {
		s.list.SelectFirst()
	} else {
		s.list.SelectNext()
	}
	s.list.ScrollToSelected()
}

func (s *SessionImport) discoverCmd(source sessionimport.SourceInfo) tea.Cmd {
	return func() tea.Msg {
		candidates, err := s.com.Workspace.DiscoverSessionImports(context.Background(), source.Source)
		return sessionImportCandidatesMsg{source: source, candidates: candidates, err: err}
	}
}

func (s *SessionImport) importCmd(paths []string) tea.Cmd {
	return func() tea.Msg {
		results, err := s.com.Workspace.ImportSessions(context.Background(), paths)
		return sessionImportDoneMsg{results: results, err: err}
	}
}

func (s *SessionImport) showSources() {
	s.input.SetValue("")
	s.list.SetFilter("")
	items := make([]list.FilterableItem, len(s.sources))
	for index, source := range s.sources {
		items[index] = newSessionImportSourceItem(s.com.Styles, source)
	}
	s.items = nil
	s.list.SetItems(items...)
	s.list.SetSelected(0)
	s.list.ScrollToTop()
}

func (s *SessionImport) showCandidates() {
	s.input.SetValue("")
	s.list.SetFilter("")
	items := make([]list.FilterableItem, len(s.candidates))
	s.items = make([]*sessionImportItem, len(s.candidates))
	for index := range s.candidates {
		item := newSessionImportCandidateItem(s.com.Styles, s.candidates[index])
		s.items[index] = item
		items[index] = item
	}
	s.list.SetItems(items...)
	s.list.SetSelected(0)
	s.list.ScrollToTop()
}

func (s *SessionImport) syncMarks() {
	for _, item := range s.items {
		item.SetMarked(s.selection.Selected(item.ID()))
	}
}

func (s *SessionImport) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight + t.Dialog.HelpView.GetVerticalFrameSize() + t.Dialog.View.GetVerticalFrameSize()
	if s.stage == sessionImportSessions {
		heightOffset += t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight
		s.input.SetWidth(max(0, innerWidth-t.Dialog.InputPrompt.GetHorizontalFrameSize()-1))
	}
	s.list.SetSize(innerWidth, height-heightOffset)
	s.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Import Sessions"
	switch {
	case s.loading:
		rc.TitleInfo = "Loading..."
	case s.stage == sessionImportSources:
		rc.TitleInfo = fmt.Sprintf("%d agents", len(s.sources))
	case len(s.candidates) == 0:
		rc.TitleInfo = "No sessions"
	default:
		rc.TitleInfo = fmt.Sprintf("%s · %d selected", s.source.Name, s.selection.Count())
	}
	var cursor *tea.Cursor
	if s.stage == sessionImportSessions {
		rc.AddPart(t.Dialog.InputPrompt.Render(s.input.View()))
		cursor = InputCursor(t, s.input.Cursor())
	}
	rc.AddPart(t.Dialog.List.Height(s.list.Height()).Render(s.list.Render()))
	rc.Help = s.help.View(s)
	DrawCenterCursor(scr, area, rc.Render(), cursor)
	return cursor
}

func (s *SessionImport) ShortHelp() []key.Binding {
	if s.stage == sessionImportSessions {
		return []key.Binding{s.keyMap.UpDown, s.keyMap.Toggle, s.keyMap.All, s.keyMap.Import, s.keyMap.Back}
	}
	return []key.Binding{s.keyMap.UpDown, s.keyMap.Select, s.keyMap.Close}
}

func (s *SessionImport) FullHelp() [][]key.Binding { return [][]key.Binding{s.ShortHelp()} }

type sessionImportItem struct {
	*list.Versioned
	t         *styles.Styles
	source    sessionimport.SourceInfo
	candidate *sessionimport.Candidate
	match     fuzzy.Match
	focused   bool
	marked    bool
}

var _ ListItem = (*sessionImportItem)(nil)

func newSessionImportSourceItem(t *styles.Styles, source sessionimport.SourceInfo) *sessionImportItem {
	return &sessionImportItem{Versioned: list.NewVersioned(), t: t, source: source}
}

func newSessionImportCandidateItem(t *styles.Styles, candidate sessionimport.Candidate) *sessionImportItem {
	return &sessionImportItem{Versioned: list.NewVersioned(), t: t, candidate: &candidate}
}

func (i *sessionImportItem) ID() string {
	if i.candidate != nil {
		return i.candidate.Path
	}
	return string(i.source.Source)
}

func (i *sessionImportItem) Filter() string {
	if i.candidate != nil {
		return i.candidate.Title
	}
	return i.source.Name
}

func (i *sessionImportItem) SetMatch(match fuzzy.Match) { i.match = match; i.Bump() }
func (i *sessionImportItem) SetFocused(focused bool)    { i.focused = focused; i.Bump() }
func (i *sessionImportItem) Finished() bool             { return true }
func (i *sessionImportItem) SetMarked(marked bool)      { i.marked = marked; i.Bump() }

func (i *sessionImportItem) Render(width int) string {
	styles := ListItemStyles{ItemBlurred: i.t.Dialog.NormalItem, ItemFocused: i.t.Dialog.SelectedItem, InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred, InfoTextFocused: i.t.Dialog.ListItem.InfoFocused}
	if i.candidate == nil {
		return renderItem(styles, i.source.Name, "", i.focused, width, nil, &i.match)
	}
	date := time.Unix(i.candidate.UpdatedAt, 0)
	info := date.Format("Jan 2, 2006") + " · " + humanize.Time(date)
	prefix := "  "
	if i.marked {
		prefix = i.t.Tool.IconSuccess.String() + " "
	}
	return renderItemWithPrefix(styles, i.candidate.Title, prefix, info, i.focused, width, nil, &i.match)
}
