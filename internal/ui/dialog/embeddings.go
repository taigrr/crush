package dialog

import (
	"fmt"

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
	// EmbeddingsID is the identifier for the embedding model picker dialog.
	EmbeddingsID              = "embeddings"
	embeddingsDialogMaxWidth  = 64
	embeddingsDialogMaxHeight = 16
)

// EmbeddingChoice describes a selectable embedding option. A zero
// Provider/Model means the "disable embeddings" entry.
type EmbeddingChoice struct {
	Provider   string
	Model      string
	Name       string
	Dimensions int64
	Configured bool // provider has resolvable credentials
}

// disabled reports whether this is the "turn embeddings off" entry.
func (c EmbeddingChoice) disabled() bool { return c.Provider == "" && c.Model == "" }

// Embeddings is a dialog for selecting the global embedding model.
type Embeddings struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// EmbeddingItem is an embedding choice list item.
type EmbeddingItem struct {
	*list.Versioned
	choice    EmbeddingChoice
	isCurrent bool
	t         *styles.Styles
	m         fuzzy.Match
	cache     map[int]string
	focused   bool
}

// Finished implements list.Item. Items are render-stable outside of
// explicit SetFocused / SetMatch.
func (i *EmbeddingItem) Finished() bool { return true }

var (
	_ Dialog   = (*Embeddings)(nil)
	_ ListItem = (*EmbeddingItem)(nil)
)

// NewEmbeddings creates a new embedding model picker dialog.
func NewEmbeddings(com *common.Common) *Embeddings {
	d := &Embeddings{com: com}

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
		key.WithHelp("↑/↓", "choose"),
	)
	d.keyMap.Close = CloseKey

	d.setItems()
	return d
}

// ID implements Dialog.
func (d *Embeddings) ID() string { return EmbeddingsID }

// embeddingChoices enumerates the "disable" entry plus every embedding
// model advertised by the known providers, marking which providers are
// configured (have resolvable credentials in this workspace).
func (d *Embeddings) embeddingChoices() []EmbeddingChoice {
	choices := []EmbeddingChoice{{}} // index 0 = disable

	cfg := d.com.Config()
	providers, err := config.Providers(cfg)
	if err != nil {
		return choices
	}
	for _, p := range providers {
		_, configured := configuredProvider(cfg, string(p.ID))
		for _, m := range p.EmbeddingModels {
			choices = append(choices, EmbeddingChoice{
				Provider:   string(p.ID),
				Model:      m.ID,
				Name:       m.Name,
				Dimensions: m.Dimensions,
				Configured: configured,
			})
		}
	}
	return choices
}

// configuredProvider reports whether the provider id has an entry in the
// user's providers config (i.e. is usable).
func configuredProvider(cfg *config.Config, id string) (config.ProviderConfig, bool) {
	if cfg == nil || cfg.Providers == nil {
		return config.ProviderConfig{}, false
	}
	return cfg.Providers.Get(id)
}

func (d *Embeddings) currentSignature() string {
	cfg := d.com.Config()
	if cfg == nil {
		return ""
	}
	return cfg.Embedding.Signature()
}

func (d *Embeddings) setItems() {
	current := d.currentSignature()

	choices := d.embeddingChoices()
	items := make([]list.FilterableItem, 0, len(choices))
	selectedIndex := 0
	for i, c := range choices {
		isCurrent := false
		if c.disabled() {
			isCurrent = current == ""
		} else {
			sig := (&config.EmbeddingConfig{
				Provider:   c.Provider,
				Model:      c.Model,
				Dimensions: c.Dimensions,
				Normalize:  true,
			}).Signature()
			isCurrent = sig == current
		}
		items = append(items, &EmbeddingItem{
			Versioned: list.NewVersioned(),
			choice:    c,
			isCurrent: isCurrent,
			t:         d.com.Styles,
		})
		if isCurrent {
			selectedIndex = i
		}
	}

	d.list.SetItems(items...)
	d.list.SetSelected(selectedIndex)
	d.list.ScrollToSelected()
}

// HandleMsg implements [Dialog].
func (d *Embeddings) HandleMsg(msg tea.Msg) Action {
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
				break
			}
			d.list.SelectPrev()
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Next):
			d.list.Focus()
			if d.list.IsSelectedLast() {
				d.list.SelectFirst()
				d.list.ScrollToTop()
				break
			}
			d.list.SelectNext()
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Select):
			selectedItem := d.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			item, ok := selectedItem.(*EmbeddingItem)
			if !ok {
				break
			}
			return ActionSelectEmbedding{Choice: item.choice}
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
func (d *Embeddings) Cursor() *tea.Cursor {
	return InputCursor(d.com.Styles, d.input.Cursor())
}

// Draw implements [Dialog].
func (d *Embeddings) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := max(0, min(embeddingsDialogMaxWidth, area.Dx()))
	height := max(0, min(embeddingsDialogMaxHeight, area.Dy()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	d.input.SetWidth(innerWidth - t.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	d.list.SetSize(innerWidth, height-heightOffset)
	d.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Embedding Model"
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
func (d *Embeddings) ShortHelp() []key.Binding {
	return []key.Binding{
		d.keyMap.UpDown,
		d.keyMap.Select,
		d.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (d *Embeddings) FullHelp() [][]key.Binding {
	return [][]key.Binding{{
		d.keyMap.Select,
		d.keyMap.Next,
		d.keyMap.Previous,
		d.keyMap.Close,
	}}
}

// Filter returns the filter value for the embedding item.
func (i *EmbeddingItem) Filter() string {
	if i.choice.disabled() {
		return "disabled off none"
	}
	return i.choice.Provider + " " + i.choice.Model + " " + i.choice.Name
}

// ID returns the unique identifier for the embedding item.
func (i *EmbeddingItem) ID() string {
	if i.choice.disabled() {
		return "__disabled__"
	}
	return i.choice.Provider + "/" + i.choice.Model
}

// SetFocused sets the focus state of the embedding item.
func (i *EmbeddingItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch sets the fuzzy match for the embedding item.
func (i *EmbeddingItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render returns the string representation of the embedding item.
func (i *EmbeddingItem) Render(width int) string {
	var title, info string
	if i.choice.disabled() {
		title = "Disabled (substring search only)"
	} else {
		title = i.choice.Provider + " · " + i.choice.Model
		if i.choice.Dimensions > 0 {
			info = fmt.Sprintf("%d dims", i.choice.Dimensions)
		}
		if !i.choice.Configured {
			if info != "" {
				info += " · "
			}
			info += "not configured"
		}
	}
	if i.isCurrent {
		if info != "" {
			info += " · "
		}
		info += "current"
	}
	st := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(st, title, info, i.focused, width, i.cache, &i.m)
}
