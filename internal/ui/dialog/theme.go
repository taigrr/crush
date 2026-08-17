package dialog

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
)

const (
	// ThemeID is the identifier for the theme picker dialog.
	ThemeID              = "theme"
	themeDialogMaxWidth  = 50
	themeDialogMaxHeight = 14
)

// Theme is a dialog for selecting the UI theme with a live preview.
type Theme struct {
	com    *common.Common
	help   help.Model
	list   *list.FilterableList
	input  textinput.Model
	isDark bool

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// ThemeItem is a theme list item.
type ThemeItem struct {
	*list.Versioned
	name      string
	isDark    bool
	styles    styles.Styles
	isCurrent bool
	t         *styles.Styles
	m         fuzzy.Match
	cache     map[int]string
	focused   bool
}

// Finished implements list.Item. Theme items are render-stable outside of
// explicit SetFocused / SetMatch.
func (i *ThemeItem) Finished() bool { return true }

var (
	_ Dialog   = (*Theme)(nil)
	_ ListItem = (*ThemeItem)(nil)
)

// NewTheme creates a new theme picker dialog.
func NewTheme(com *common.Common, isDark ...bool) (*Theme, error) {
	dark := true
	if len(isDark) > 0 {
		dark = isDark[0]
	}
	d := &Theme{com: com, isDark: dark}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	d.help = h

	d.list = list.NewFilterableList()
	d.list.Focus()

	d.input = textinput.New()
	d.input.SetVirtualCursor(false)
	d.input.Placeholder = "Type to filter"
	d.input.SetStyles(com.Styles.TextInput)
	d.input.Focus()

	d.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	d.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	d.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	d.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "preview"),
	)
	d.keyMap.Close = CloseKey

	d.setThemeItems("")
	return d, nil
}

// ID implements Dialog.
func (d *Theme) ID() string { return ThemeID }

// currentThemeName returns the configured theme name, or the default.
func (d *Theme) currentThemeName() string {
	cfg := d.com.Config()
	if cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil && cfg.Options.TUI.Theme != "" {
		return cfg.Options.TUI.Theme
	}
	return styles.DefaultThemeName
}

func (d *Theme) setThemeItems(preferred string) {
	current := d.currentThemeName()
	if preferred == "" {
		preferred = current
	}

	var items []list.FilterableItem
	selectedIndex := 0
	idx := 0

	add := func(name string, isDark bool, s styles.Styles) {
		isCurrent := styles.NormalizeThemeName(name) == styles.NormalizeThemeName(current)
		items = append(items, &ThemeItem{
			Versioned: list.NewVersioned(),
			name:      name,
			isDark:    isDark,
			styles:    s,
			isCurrent: isCurrent,
			t:         d.com.Styles,
		})
		if styles.NormalizeThemeName(name) == styles.NormalizeThemeName(preferred) {
			selectedIndex = idx
		}
		idx++
	}

	for _, info := range styles.BuiltinThemeInfos() {
		s, _ := styles.BuiltinThemeByName(info.Name, d.isDark)
		add(info.Name, d.isDark, s)
	}
	userThemes, _ := styles.LoadUserThemes(config.GlobalThemesDir())
	for _, ut := range userThemes {
		add(ut.Name, ut.IsDark, ut.Styles)
	}

	d.list.SetItems(items...)
	d.list.SetFilter(d.input.Value())
	for index, item := range d.list.FilteredItems() {
		theme, ok := item.(*ThemeItem)
		if ok && styles.NormalizeThemeName(theme.name) == styles.NormalizeThemeName(preferred) {
			selectedIndex = index
			break
		}
	}
	d.list.SetSelected(selectedIndex)
	d.list.ScrollToSelected()
}

func (d *Theme) SetDarkBackground(isDark bool) styles.Styles {
	selectedName := ""
	if item, ok := d.list.SelectedItem().(*ThemeItem); ok {
		selectedName = item.name
	}
	if d.isDark != isDark {
		d.isDark = isDark
		d.setThemeItems(selectedName)
	}
	if item, ok := d.list.SelectedItem().(*ThemeItem); ok {
		return item.styles
	}
	return *d.com.Styles
}

// previewAction returns the preview action for the currently selected theme,
// or nil if there is no selection.
func (d *Theme) previewAction() Action {
	selected := d.list.SelectedItem()
	if selected == nil {
		return nil
	}
	item, ok := selected.(*ThemeItem)
	if !ok {
		return nil
	}
	return ActionPreviewTheme{Styles: item.styles}
}

// HandleMsg implements [Dialog].
func (d *Theme) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Previous):
			d.list.Focus()
			if d.list.IsSelectedFirst() {
				d.list.SelectLast()
				d.list.ScrollToBottom()
			} else {
				d.list.SelectPrev()
				d.list.ScrollToSelected()
			}
			return d.previewAction()
		case key.Matches(msg, d.keyMap.Next):
			d.list.Focus()
			if d.list.IsSelectedLast() {
				d.list.SelectFirst()
				d.list.ScrollToTop()
			} else {
				d.list.SelectNext()
				d.list.ScrollToSelected()
			}
			return d.previewAction()
		case key.Matches(msg, d.keyMap.Select):
			selectedItem := d.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			item, ok := selectedItem.(*ThemeItem)
			if !ok {
				break
			}
			return ActionSelectTheme{Name: item.name, Styles: item.styles}
		default:
			var cmd tea.Cmd
			d.input, cmd = d.input.Update(msg)
			value := d.input.Value()
			d.list.SetFilter(value)
			d.list.ScrollToTop()
			d.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (d *Theme) Cursor() *tea.Cursor {
	return InputCursor(d.com.Styles, d.input.Cursor())
}

// Draw implements [Dialog].
func (d *Theme) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := max(0, min(themeDialogMaxWidth, area.Dx()))
	height := max(0, min(themeDialogMaxHeight, area.Dy()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	d.input.SetWidth(innerWidth - t.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	d.list.SetSize(innerWidth, height-heightOffset)
	d.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Select Theme"
	inputView := t.Dialog.InputPrompt.Render(d.input.View())
	rc.AddPart(inputView)

	visibleCount := len(d.list.FilteredItems())
	if d.list.Height() >= visibleCount {
		d.list.ScrollToTop()
	} else {
		d.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(d.list.Height()).Render(d.list.Render())
	rc.AddPart(listView)
	rc.Help = d.help.View(d)

	view := rc.Render()

	cur := d.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (d *Theme) ShortHelp() []key.Binding {
	return []key.Binding{
		d.keyMap.UpDown,
		d.keyMap.Select,
		d.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (d *Theme) FullHelp() [][]key.Binding {
	return [][]key.Binding{{
		d.keyMap.Select,
		d.keyMap.Next,
		d.keyMap.Previous,
		d.keyMap.Close,
	}}
}

// Filter returns the filter value for the theme item.
func (i *ThemeItem) Filter() string { return i.name }

// ID returns the unique identifier for the theme item.
func (i *ThemeItem) ID() string { return i.name }

// SetFocused sets the focus state of the theme item.
func (i *ThemeItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch sets the fuzzy match for the theme item.
func (i *ThemeItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render returns the string representation of the theme item.
func (i *ThemeItem) Render(width int) string {
	info := "dark"
	if !i.isDark {
		info = "light"
	}
	if i.isCurrent {
		info += " · current"
	}
	st := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(st, i.name, info, i.focused, width, i.cache, &i.m)
}
