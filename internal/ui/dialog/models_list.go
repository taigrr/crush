package dialog

import (
	"slices"

	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/sahilm/fuzzy"
)

// ModelsList is a list specifically for model items and groups.
type ModelsList struct {
	*list.List
	groups []ModelGroup
	items  []list.Item
	query  string
	t      *styles.Styles
}

// NewModelsList creates a new list suitable for model items and groups.
func NewModelsList(sty *styles.Styles, groups ...ModelGroup) *ModelsList {
	f := &ModelsList{
		List:   list.NewList(),
		groups: groups,
		t:      sty,
	}
	return f
}

// SetGroups sets the model groups and updates the list items.
func (f *ModelsList) SetGroups(groups ...ModelGroup) {
	f.groups = groups
}

// SetFilter sets the filter query and updates the list items.
func (f *ModelsList) SetFilter(q string) {
	f.query = q
}

// SetSelectedItem sets the selected item in the list by item ID.
func (f *ModelsList) SetSelectedItem(itemID string) {
	count := 0
	for _, g := range f.groups {
		for _, item := range g.Items {
			if item.ID() == itemID {
				f.List.SetSelected(count)
				return
			}
			count++
		}
	}
}

// SelectNext selects the next selectable item in the list.
func (f *ModelsList) SelectNext() bool {
	for f.List.SelectNext() {
		if _, ok := f.List.SelectedItem().(*ModelItem); ok {
			return true
		}
	}
	return false
}

// SelectPrev selects the previous selectable item in the list.
func (f *ModelsList) SelectPrev() bool {
	for f.List.SelectPrev() {
		if _, ok := f.List.SelectedItem().(*ModelItem); ok {
			return true
		}
	}
	return false
}

// VisibleItems returns the visible items after filtering.
func (f *ModelsList) VisibleItems() []list.Item {
	if len(f.query) == 0 {
		// No filter, return all items with group headers
		items := []list.Item{}
		for _, g := range f.groups {
			items = append(items, &g)
			for _, item := range g.Items {
				items = append(items, item)
			}
			// Add a space separator after each provider section
			items = append(items, list.NewSpacerItem(1))
		}
		return items
	}

	groupItems := map[int][]*ModelItem{}
	filterableItems := []list.FilterableItem{}
	for i, g := range f.groups {
		for _, item := range g.Items {
			filterableItems = append(filterableItems, item)
			groupItems[i] = append(groupItems[i], item)
		}
	}

	matches := fuzzy.FindFrom(f.query, list.FilterableItemsSource(filterableItems))
	for _, match := range matches {
		item := filterableItems[match.Index]
		if ms, ok := item.(list.MatchSettable); ok {
			ms.SetMatch(match)
			item = ms.(list.FilterableItem)
		}
		filterableItems = append(filterableItems, item)
	}

	items := []list.Item{}
	visitedGroups := map[int]bool{}

	// Reconstruct groups with matched items
	for _, match := range matches {
		item := filterableItems[match.Index]
		// Find which group this item belongs to
		for gi, g := range f.groups {
			if slices.Contains(groupItems[gi], item.(*ModelItem)) {
				if !visitedGroups[gi] {
					// Add section header
					items = append(items, &g)
					visitedGroups[gi] = true
				}
				// Add the matched item
				if ms, ok := item.(list.MatchSettable); ok {
					ms.SetMatch(match)
					item = ms.(list.FilterableItem)
				}
				// Add a space separator after each provider section
				items = append(items, item, list.NewSpacerItem(1))
				break
			}
		}
	}

	return items
}

// Render renders the filterable list.
func (f *ModelsList) Render() string {
	f.List.SetItems(f.VisibleItems()...)
	return f.List.Render()
}

type modelGroups []ModelGroup

func (m modelGroups) Len() int {
	n := 0
	for _, g := range m {
		n += len(g.Items)
	}
	return n
}

func (m modelGroups) String(i int) string {
	count := 0
	for _, g := range m {
		if i < count+len(g.Items) {
			return g.Items[i-count].Filter()
		}
		count += len(g.Items)
	}
	return ""
}
