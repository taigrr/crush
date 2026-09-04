package dialog

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dustin/go-humanize"
	"github.com/rivo/uniseg"
	"github.com/sahilm/fuzzy"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
)

// sameFuzzyMatch reports whether two fuzzy.Match values are
// observably equal. Because Match contains a slice (MatchedIndexes)
// it is not directly comparable with ==; we compare the scalar
// fields and then walk the indexes. The dialog list items use this
// to skip gratuitous version bumps when SetMatch reapplies the same
// match.
func sameFuzzyMatch(a, b fuzzy.Match) bool {
	return a.Str == b.Str &&
		a.Index == b.Index &&
		a.Score == b.Score &&
		slices.Equal(a.MatchedIndexes, b.MatchedIndexes)
}

// ListItem represents a selectable and searchable item in a dialog list.
type ListItem interface {
	list.FilterableItem
	list.Focusable
	list.MatchSettable

	// ID returns the unique identifier of the item.
	ID() string
}

// SessionItem wraps a [session.Session] to implement the [ListItem] interface.
type SessionItem struct {
	*list.Versioned
	session.Session
	t                *styles.Styles
	sessionsMode     sessionsMode
	m                fuzzy.Match
	cache            map[int]string
	updateTitleInput textinput.Model
	focused          bool
	// marked reports whether this session is in the popup's multi-select
	// set; when true the row renders a ✓ prefix instead of the swarm square.
	marked bool
	// spawnerLabel is the "by color-animal" note shown in the info column
	// when the session was spawned by another session (swarm lineage) that
	// is present in the picker. Empty otherwise.
	spawnerLabel string
}

// Finished implements list.Item. Session items are render-stable
// outside of explicit SetFocused / SetMatch calls, both of which
// bump Version() and therefore invalidate the F6 frozen entry.
func (s *SessionItem) Finished() bool {
	return true
}

var _ ListItem = &SessionItem{}

// Filter returns the filterable value of the session.
func (s *SessionItem) Filter() string {
	return s.Title
}

// ID returns the unique identifier of the session.
func (s *SessionItem) ID() string {
	return s.Session.ID
}

// SetMatch sets the fuzzy match for the session item.
func (s *SessionItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(s.m, m) {
		return
	}
	s.cache = nil
	s.m = m
	if s.Versioned != nil {
		s.Bump()
	}
}

// InputValue returns the updated title value
func (s *SessionItem) InputValue() string {
	return s.updateTitleInput.Value()
}

// HandleInput forwards input message to the update title input
func (s *SessionItem) HandleInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	s.updateTitleInput, cmd = s.updateTitleInput.Update(msg)
	if s.Versioned != nil {
		s.Bump()
	}
	return cmd
}

// Cursor returns the cursor of the update title input
func (s *SessionItem) Cursor() *tea.Cursor {
	return s.updateTitleInput.Cursor()
}

// Render returns the string representation of the session item.
func (s *SessionItem) Render(width int) string {
	info := humanize.Time(time.Unix(s.UpdatedAt, 0))
	if s.spawnerLabel != "" {
		info = s.spawnerLabel + " · " + info
	}
	styles := ListItemStyles{
		ItemBlurred:     s.t.Dialog.NormalItem,
		ItemFocused:     s.t.Dialog.SelectedItem,
		InfoTextBlurred: s.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: s.t.Dialog.Sessions.InfoFocused,
	}

	switch s.sessionsMode {
	case sessionsModeDeleting:
		styles.ItemBlurred = s.t.Dialog.Sessions.DeletingItemBlurred
		styles.ItemFocused = s.t.Dialog.Sessions.DeletingItemFocused
	case sessionsModeArchiving:
		styles.ItemBlurred = s.t.Dialog.Sessions.ArchivingItemBlurred
		styles.ItemFocused = s.t.Dialog.Sessions.ArchivingItemFocused
	case sessionsModeUpdating:
		styles.ItemBlurred = s.t.Dialog.Sessions.RenamingItemBlurred
		styles.ItemFocused = s.t.Dialog.Sessions.RenamingingItemFocused
		if s.focused {
			const cursorPadding = 1
			inputWidth := max(0, width-styles.ItemFocused.GetHorizontalFrameSize()-cursorPadding)
			s.updateTitleInput.SetWidth(inputWidth)
			s.updateTitleInput.Placeholder = ansi.Truncate(s.Title, width, "…")
			return styles.ItemFocused.Render(s.updateTitleInput.View())
		}
	}

	// Single-line row matching the left session sidebar: the swarm
	// color square prefixes the title. The square is passed as a
	// separate rendered prefix (not concatenated into the title) so
	// the fuzzy-match highlight byte offsets — computed against the
	// bare s.Title — stay correctly anchored. When the row is
	// multi-selected, a ✓ replaces the square for a clear, consistent
	// selection treatment (matching the sidebar).
	prefix := sessionTitlePrefix(s.Color)
	if s.marked {
		prefix = s.t.Tool.IconSuccess.String() + " "
	}
	return renderItemWithPrefix(styles, s.Title, prefix, info, s.focused, width, s.cache, &s.m)
}

// SetMarked sets whether this session is in the multi-select set, clearing
// the render cache and bumping the version when it changes.
func (s *SessionItem) SetMarked(marked bool) {
	if s.marked == marked {
		return
	}
	s.cache = nil
	s.marked = marked
	if s.Versioned != nil {
		s.Bump()
	}
}

// sessionTitlePrefix returns the inline prefix rendered before a
// session title in the picker: the swarm color square + space,
// matching the left session sidebar. Sessions without an assigned
// color get a two-space pad so titles stay column-aligned.
func sessionTitlePrefix(color string) string {
	sq := common.SwarmSquare(color)
	if sq == "" {
		return "  "
	}
	return sq + " "
}

type ListItemStyles struct {
	ItemBlurred     lipgloss.Style
	ItemFocused     lipgloss.Style
	InfoTextBlurred lipgloss.Style
	InfoTextFocused lipgloss.Style
}

func renderItem(t ListItemStyles, title string, info string, focused bool, width int, cache map[int]string, m *fuzzy.Match) string {
	return renderItemWithPrefix(t, title, "", info, focused, width, cache, m)
}

// renderItemWithPrefix is renderItem with an optional inline prefix
// (e.g. a swarm color square) rendered before the title. The prefix
// is NOT part of the fuzzy-match highlight computation, so match byte
// offsets stay anchored to the bare title; the prefix's display width
// is deducted from the space available for the (truncated) title.
func renderItemWithPrefix(t ListItemStyles, title, prefix string, info string, focused bool, width int, cache map[int]string, m *fuzzy.Match) string {
	if cache == nil {
		cache = make(map[int]string)
	}

	cached, ok := cache[width]
	if ok {
		return cached
	}

	style := t.ItemBlurred
	if focused {
		style = t.ItemFocused
	}

	var infoText string
	var infoWidth int
	lineWidth := width
	if len(info) > 0 {
		infoText = fmt.Sprintf(" %s ", info)
		if focused {
			infoText = t.InfoTextFocused.Render(infoText)
		} else {
			infoText = t.InfoTextBlurred.Render(infoText)
		}

		infoWidth = lipgloss.Width(infoText)
	}

	prefixWidth := lipgloss.Width(prefix)
	title = ansi.Truncate(title, max(0, lineWidth-infoWidth-prefixWidth), "…")
	titleWidth := lipgloss.Width(title)
	gap := strings.Repeat(" ", max(0, lineWidth-titleWidth-infoWidth-prefixWidth))
	content := title
	if m != nil && len(m.MatchedIndexes) > 0 {
		var lastPos int
		parts := make([]string, 0)
		ranges := matchedRanges(m.MatchedIndexes)
		for _, rng := range ranges {
			start, stop := bytePosToVisibleCharPos(title, rng)
			if start > lastPos {
				parts = append(parts, ansi.Cut(title, lastPos, start))
			}
			// NOTE: We're using [ansi.Style] here instead of [lipglosStyle]
			// because we can control the underline start and stop more
			// precisely via [ansi.AttrUnderline] and [ansi.AttrNoUnderline]
			// which only affect the underline attribute without interfering
			// with other style attributes.
			parts = append(
				parts,
				ansi.NewStyle().Underline(true).String(),
				ansi.Cut(title, start, stop+1),
				ansi.NewStyle().Underline(false).String(),
			)
			lastPos = stop + 1
		}
		if lastPos < ansi.StringWidth(title) {
			parts = append(parts, ansi.Cut(title, lastPos, ansi.StringWidth(title)))
		}

		content = strings.Join(parts, "")
	}

	content = style.Render(prefix + content + gap + infoText)
	cache[width] = content
	return content
}

// SetFocused sets the focus state of the session item.
func (s *SessionItem) SetFocused(focused bool) {
	if s.focused == focused {
		return
	}
	s.cache = nil
	s.focused = focused
	if s.Versioned != nil {
		s.Bump()
	}
}

// SeparatorItem is a non-selectable separator in the sessions list.
type SeparatorItem struct {
	*list.Versioned
	t     *styles.Styles
	label string
}

var _ list.FilterableItem = &SeparatorItem{Versioned: list.NewVersioned()}

// NewSeparatorItem creates a new separator item with the given label.
func NewSeparatorItem(t *styles.Styles, label string) *SeparatorItem {
	return &SeparatorItem{Versioned: list.NewVersioned(), t: t, label: label}
}

// Filter returns empty string so separator is always visible.
func (s *SeparatorItem) Filter() string {
	return ""
}

// Render renders the separator.
func (s *SeparatorItem) Render(width int) string {
	label := s.label
	if label == "" {
		label = "Archived"
	}
	// Create a centered label with dashes on both sides
	labelLen := len(label) + 2 // space on each side
	if width <= labelLen {
		return s.t.Dialog.Sessions.SeparatorStyle.Render(label)
	}
	dashCount := (width - labelLen) / 2
	left := strings.Repeat("─", dashCount)
	right := strings.Repeat("─", width-dashCount-labelLen)
	return s.t.Dialog.Sessions.SeparatorStyle.Render(left + " " + label + " " + right)
}

// sessionItems takes a slice of [session.Session]s and convert them to a slice
// of [ListItem]s. spawners maps a session id to the swarm address label of
// the session that spawned it (see [spawnerLabels]); nil disables the note.
func sessionItems(t *styles.Styles, mode sessionsMode, spawners map[string]string, sessions ...session.Session) []list.FilterableItem {
	items := make([]list.FilterableItem, len(sessions))
	for i, s := range sessions {
		item := &SessionItem{Versioned: list.NewVersioned(), Session: s, t: t, sessionsMode: mode, spawnerLabel: spawners[s.ID]}
		if mode == sessionsModeUpdating {
			item.updateTitleInput = textinput.New()
			item.updateTitleInput.SetVirtualCursor(false)
			item.updateTitleInput.Prompt = ""
			inputStyle := t.TextInput
			inputStyle.Focused.Placeholder = t.Dialog.Sessions.RenamingPlaceholder
			item.updateTitleInput.SetStyles(inputStyle)
			item.updateTitleInput.Focus()
		}
		items[i] = item
	}
	return items
}

// spawnerLabels resolves swarm lineage into display notes: for every
// session whose SpawnedBySessionID names another session in the given
// lists, the result maps the spawned session's id to "by <color-animal>"
// of its spawner. Sessions whose spawner is unknown here (another
// workspace, deleted) get no note rather than a bare id.
func spawnerLabels(lists ...[]session.Session) map[string]string {
	byID := make(map[string]session.Session)
	for _, l := range lists {
		for _, s := range l {
			byID[s.ID] = s
		}
	}
	out := make(map[string]string)
	for id, s := range byID {
		if s.SpawnedBySessionID == "" || s.SpawnedBySessionID == id {
			continue
		}
		spawner, ok := byID[s.SpawnedBySessionID]
		if !ok || spawner.Color == "" || spawner.Animal == "" {
			continue
		}
		out[id] = "by " + swarm.Identity{Color: spawner.Color, Animal: spawner.Animal}.String()
	}
	return out
}

func matchedRanges(in []int) [][2]int {
	if len(in) == 0 {
		return [][2]int{}
	}
	current := [2]int{in[0], in[0]}
	if len(in) == 1 {
		return [][2]int{current}
	}
	var out [][2]int
	for i := 1; i < len(in); i++ {
		if in[i] == current[1]+1 {
			current[1] = in[i]
		} else {
			out = append(out, current)
			current = [2]int{in[i], in[i]}
		}
	}
	out = append(out, current)
	return out
}

func bytePosToVisibleCharPos(str string, rng [2]int) (int, int) {
	bytePos, byteStart, byteStop := 0, rng[0], rng[1]
	pos, start, stop := 0, 0, 0
	gr := uniseg.NewGraphemes(str)
	for byteStart > bytePos {
		if !gr.Next() {
			break
		}
		bytePos += len(gr.Str())
		pos += max(1, gr.Width())
	}
	start = pos
	for byteStop > bytePos {
		if !gr.Next() {
			break
		}
		bytePos += len(gr.Str())
		pos += max(1, gr.Width())
	}
	stop = pos
	return start, stop
}

// Finished implements list.Item.
func (s *SeparatorItem) Finished() bool { return true }
