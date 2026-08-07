package dialog

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
)

// SearchResultItem renders one per-session search hit in the palette: a
// title row (swarm color square + session title, with a right-aligned
// score/match badge) above a dimmed snippet line. It is focus-aware so
// the selected row is highlighted.
type SearchResultItem struct {
	*list.Versioned
	com     *common.Common
	hit     proto.SessionHit
	focused bool
	cache   map[int]string
}

var (
	_ list.Item      = (*SearchResultItem)(nil)
	_ list.Focusable = (*SearchResultItem)(nil)
)

// NewSearchResultItem creates a search result row for a session hit.
func NewSearchResultItem(com *common.Common, hit proto.SessionHit) *SearchResultItem {
	return &SearchResultItem{
		Versioned: list.NewVersioned(),
		com:       com,
		hit:       hit,
		cache:     make(map[int]string),
	}
}

// Finished implements list.Item. Rows are render-stable outside of an
// explicit SetFocused, which bumps the version and invalidates the cache.
func (r *SearchResultItem) Finished() bool {
	return true
}

// SetFocused implements list.Focusable.
func (r *SearchResultItem) SetFocused(focused bool) {
	if r.focused == focused {
		return
	}
	r.focused = focused
	r.cache = nil
	r.Bump()
}

// Render implements list.Item.
func (r *SearchResultItem) Render(width int) string {
	if r.cache == nil {
		r.cache = make(map[int]string)
	}
	if cached, ok := r.cache[width]; ok {
		return cached
	}

	t := r.com.Styles
	titleStyle := t.Dialog.NormalItem
	if r.focused {
		titleStyle = t.Dialog.SelectedItem
	}

	title := r.hit.SessionTitle
	if title == "" {
		title = "(untitled)"
	}

	// The badge shows the match kind (exact/semantic/both). The raw fused
	// RRF score is intentionally not shown: it is a tiny number (~0.01–0.03)
	// that reads as noise to users; rank/order already conveys relevance.
	badge := fmt.Sprintf(" %s ", r.hit.Match)
	badgeView := t.Dialog.Sessions.InfoBlurred.Render(badge)
	if r.focused {
		badgeView = t.Dialog.Sessions.InfoFocused.Render(badge)
	}
	badgeWidth := lipgloss.Width(badgeView)

	// Two-space pad keeps titles column-aligned with dialogs that show a
	// leading swarm square; the search hit does not carry the session
	// color, so no square is drawn here.
	prefix := "  "
	prefixWidth := lipgloss.Width(prefix)

	innerWidth := max(0, width-titleStyle.GetHorizontalFrameSize())
	titleCap := max(0, innerWidth-badgeWidth-prefixWidth)
	titleText := ansi.Truncate(title, titleCap, "…")
	pad := max(0, innerWidth-prefixWidth-lipgloss.Width(titleText)-badgeWidth)
	titleLine := titleStyle.Render(prefix + titleText + strings.Repeat(" ", pad) + badgeView)

	snippet := strings.ReplaceAll(r.hit.Snippet, "\n", " ")
	snippetStyle := t.Dialog.Sessions.InfoBlurred
	snippetText := snippetStyle.Render(ansi.Truncate(snippet, max(0, innerWidth), "…"))
	snippetLine := titleStyle.Render(snippetText)

	view := lipgloss.JoinVertical(lipgloss.Left, titleLine, snippetLine)
	r.cache[width] = view
	return view
}
