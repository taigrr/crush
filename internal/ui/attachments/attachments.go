package attachments

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/message"
)

const maxFilename = 15

// closeGlyph is the click-to-remove affordance rendered after each
// attachment chip's filename (non-deleting mode only).
const closeGlyph = "×"

type Keymap struct {
	DeleteMode,
	DeleteAll,
	Escape key.Binding
}

func New(renderer *Renderer, keyMap Keymap) *Attachments {
	return &Attachments{
		keyMap:   keyMap,
		renderer: renderer,
	}
}

type Attachments struct {
	renderer *Renderer
	keyMap   Keymap
	list     []message.Attachment
	deleting bool
}

func (m *Attachments) List() []message.Attachment { return m.list }
func (m *Attachments) Reset()                     { m.list = nil }

// HandleMouseClick handles a click at display column x (relative to the
// start of the rendered attachment row) within a row rendered at the
// given width. It removes the attachment chip under the click, if any,
// and reports whether the click was handled. This is additive to the
// existing keyboard delete-mode flow: clicks are only honored outside of
// delete-mode so the two removal paths never fight over the same input.
func (m *Attachments) HandleMouseClick(x, width int) bool {
	if m.deleting || len(m.list) == 0 {
		return false
	}
	idx := m.renderer.AttachmentAt(m.list, width, x)
	if idx < 0 {
		return false
	}
	m.list = slices.Delete(m.list, idx, idx+1)
	return true
}

func (m *Attachments) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case message.Attachment:
		m.list = append(m.list, msg)
		return true
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.DeleteMode):
			if len(m.list) > 0 {
				m.deleting = true
			}
			return true
		case m.deleting && key.Matches(msg, m.keyMap.Escape):
			m.deleting = false
			return true
		case m.deleting && key.Matches(msg, m.keyMap.DeleteAll):
			m.deleting = false
			m.list = nil
			return true
		case m.deleting:
			// Handle digit keys for individual attachment deletion.
			r := msg.Code
			if r >= '0' && r <= '9' {
				num := int(r - '0')
				if num < len(m.list) {
					m.list = slices.Delete(m.list, num, num+1)
				}
				m.deleting = false
			}
			return true
		}
	}
	return false
}

func (m *Attachments) Render(width int) string {
	return m.renderer.Render(m.list, m.deleting, width)
}

// Renderer returns the attachment renderer so callers can update its
// styles in place.
func (m *Attachments) Renderer() *Renderer { return m.renderer }

func NewRenderer(normalStyle, deletingStyle, imageStyle, textStyle, skillStyle lipgloss.Style) *Renderer {
	return &Renderer{
		normalStyle:   normalStyle,
		textStyle:     textStyle,
		imageStyle:    imageStyle,
		skillStyle:    skillStyle,
		deletingStyle: deletingStyle,
	}
}

// SetStyles updates the renderer styles in place.
func (r *Renderer) SetStyles(normalStyle, deletingStyle, imageStyle, textStyle, skillStyle lipgloss.Style) {
	r.normalStyle = normalStyle
	r.textStyle = textStyle
	r.imageStyle = imageStyle
	r.skillStyle = skillStyle
	r.deletingStyle = deletingStyle
}

type Renderer struct {
	normalStyle, textStyle, imageStyle, skillStyle, deletingStyle lipgloss.Style
}

func (r *Renderer) Render(attachments []message.Attachment, deleting bool, width int) string {
	var chips []string

	maxItemWidth := r.maxItemWidth(deleting)
	fits := int(math.Floor(float64(width)/float64(maxItemWidth))) - 1

	for i, att := range attachments {
		filename := r.filename(att)

		if deleting {
			chips = append(
				chips,
				r.deletingStyle.Render(fmt.Sprintf("%d", i)),
				r.normalStyle.Render(filename),
			)
		} else {
			chips = append(
				chips,
				r.icon(att).String(),
				r.normalStyle.Render(filename),
				r.deletingStyle.Render(closeGlyph),
			)
		}

		if i == fits && len(attachments) > i {
			chips = append(chips, lipgloss.NewStyle().Width(maxItemWidth).Render(fmt.Sprintf("%d more…", len(attachments)-fits)))
			break
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Left, chips...)
}

// maxItemWidth is the per-chip width budget used to decide how many chips
// fit before the "N more…" indicator. The close glyph is only rendered in
// non-deleting mode, so it only counts toward the budget there — otherwise
// delete-mode would under-count how many chips fit.
func (r *Renderer) maxItemWidth(deleting bool) int {
	w := lipgloss.Width(r.imageStyle.String() + r.normalStyle.Render(strings.Repeat("x", maxFilename)))
	if !deleting {
		w += lipgloss.Width(r.deletingStyle.Render(closeGlyph))
	}
	return w
}

// filename returns the (possibly truncated) display filename for an
// attachment, shared by Render and AttachmentAt so hit-testing always
// matches what was actually rendered.
func (r *Renderer) filename(a message.Attachment) string {
	filename := filepath.Base(a.FileName)
	if ansi.StringWidth(filename) > maxFilename {
		filename = ansi.Truncate(filename, maxFilename, "…")
	}
	return filename
}

// AttachmentAt returns the index of the attachment chip rendered at
// display column x for a non-deleting-mode row rendered at the given
// width, or -1 if x doesn't land on any chip (e.g. it's over padding or
// the trailing "N more…" indicator). It mirrors the layout logic in
// Render exactly so mouse hit-testing never disagrees with what's drawn.
func (r *Renderer) AttachmentAt(attachments []message.Attachment, width, x int) int {
	if x < 0 {
		return -1
	}

	maxItemWidth := r.maxItemWidth(false)
	fits := int(math.Floor(float64(width)/float64(maxItemWidth))) - 1

	col := 0
	for i, att := range attachments {
		filename := r.filename(att)
		chipWidth := lipgloss.Width(r.icon(att).String()) +
			lipgloss.Width(r.normalStyle.Render(filename)) +
			lipgloss.Width(r.deletingStyle.Render(closeGlyph))

		if x >= col && x < col+chipWidth {
			return i
		}
		col += chipWidth

		if i == fits && len(attachments) > i {
			break
		}
	}
	return -1
}

func (r *Renderer) icon(a message.Attachment) lipgloss.Style {
	if a.IsImage() {
		return r.imageStyle
	}
	if a.IsMarkdown() {
		return r.skillStyle
	}
	return r.textStyle
}
