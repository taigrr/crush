package dialog

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// ModelGroup represents a group of model items.
type ModelGroup struct {
	Title      string
	Items      []*ModelItem
	configured bool
	t          *styles.Styles
}

// NewModelGroup creates a new ModelGroup.
func NewModelGroup(t *styles.Styles, title string, configured bool, items ...*ModelItem) ModelGroup {
	return ModelGroup{
		Title: title,
		Items: items,
		t:     t,
	}
}

// AppendItems appends [ModelItem]s to the group.
func (m *ModelGroup) AppendItems(items ...*ModelItem) {
	m.Items = append(m.Items, items...)
}

// Render implements [list.Item].
func (m *ModelGroup) Render(width int) string {
	var configured string
	if m.configured {
		configuredIcon := m.t.ToolCallSuccess.Render()
		configuredText := m.t.Subtle.Render("Configured")
		configured = configuredIcon + " " + configuredText
	}

	title := " " + m.Title + " "
	title = ansi.Truncate(title, max(0, width-lipgloss.Width(configured)-1), "…")

	return common.Section(m.t, title, width, configured)
}

// ModelItem represents a list item for a model type.
type ModelItem struct {
	prov  catwalk.Provider
	model catwalk.Model

	cache   map[int]string
	t       *styles.Styles
	m       fuzzy.Match
	focused bool
}

var _ ListItem = &ModelItem{}

// NewModelItem creates a new ModelItem.
func NewModelItem(t *styles.Styles, prov catwalk.Provider, model catwalk.Model) *ModelItem {
	return &ModelItem{
		prov:  prov,
		model: model,
		t:     t,
		cache: make(map[int]string),
	}
}

// Filter implements ListItem.
func (m *ModelItem) Filter() string {
	return m.model.Name
}

// ID implements ListItem.
func (m *ModelItem) ID() string {
	return modelKey(string(m.prov.ID), m.model.ID)
}

// Render implements ListItem.
func (m *ModelItem) Render(width int) string {
	return renderItem(m.t, m.model.Name, "", m.focused, width, m.cache, &m.m)
}

// SetFocused implements ListItem.
func (m *ModelItem) SetFocused(focused bool) {
	m.focused = focused
}

// SetMatch implements ListItem.
func (m *ModelItem) SetMatch(fm fuzzy.Match) {
	m.m = fm
}
