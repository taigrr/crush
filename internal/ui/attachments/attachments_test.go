package attachments

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// newTestRenderer builds a Renderer with the real theme attachment styles so
// hit-testing math is exercised against the same styles used in production.
func newTestRenderer() *Renderer {
	s := themes.CharmtonePantera()
	return NewRenderer(
		s.Attachments.Normal,
		s.Attachments.Deleting,
		s.Attachments.Image,
		s.Attachments.Text,
		s.Attachments.Skill,
	)
}

func img(name string) message.Attachment {
	return message.Attachment{FileName: name, MimeType: "image/png"}
}

func txt(name string) message.Attachment {
	return message.Attachment{FileName: name, MimeType: "text/plain"}
}

func skill(name string) message.Attachment {
	return message.Attachment{FileName: name, MimeType: "text/markdown"}
}

// chipSpans returns, for a non-deleting render at the given width, the
// [start,end) display-column span of every chip that AttachmentAt should be
// able to hit. It is derived independently from Render's output so a desync
// between Render and AttachmentAt would surface as a failing assertion.
func chipSpans(r *Renderer, atts []message.Attachment, width int) [][2]int {
	maxItemWidth := r.maxItemWidth(false)
	fits := int(float64(width)/float64(maxItemWidth)) - 1

	var spans [][2]int
	col := 0
	for i, att := range atts {
		w := lipgloss.Width(r.icon(att).String()) +
			lipgloss.Width(r.normalStyle.Render(r.filename(att))) +
			lipgloss.Width(r.deletingStyle.Render(closeGlyph))
		spans = append(spans, [2]int{col, col + w})
		col += w
		if i == fits && len(atts) > i {
			break
		}
	}
	return spans
}

func TestAttachmentAtHitsEveryVisibleChip(t *testing.T) {
	t.Parallel()
	r := newTestRenderer()

	cases := []struct {
		name  string
		atts  []message.Attachment
		width int
	}{
		{
			name:  "single chip",
			atts:  []message.Attachment{img("a.png")},
			width: 200,
		},
		{
			name:  "several chips wide",
			atts:  []message.Attachment{img("a.png"), img("b.png"), img("c.png")},
			width: 200,
		},
		{
			name:  "truncated long filename",
			atts:  []message.Attachment{img("a-really-long-filename-that-truncates.png"), img("b.png")},
			width: 200,
		},
		{
			name:  "mixed image text skill icons",
			atts:  []message.Attachment{img("a.png"), txt("b.txt"), skill("c.md")},
			width: 200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spans := chipSpans(r, tc.atts, tc.width)
			require.Len(t, spans, len(tc.atts), "all chips should fit at this width")

			// Tie the model to reality: for the all-fit case the total chip
			// span must equal the width of what Render actually draws, so a
			// future Render change that desyncs the layout fails here.
			require.Equal(t, lipgloss.Width(r.Render(tc.atts, false, tc.width)), spans[len(spans)-1][1])

			for i, span := range spans {
				// Per-chip start column must match where Render actually
				// draws chip i: the width of the render of just the first i
				// chips equals chip i's start column. This catches an
				// offset desync that preserves total width.
				require.Equalf(t, span[0], lipgloss.Width(r.Render(tc.atts[:i], false, tc.width)),
					"chip %d start column should match Render of the prefix", i)

				// Left edge, an interior column, and the last column of the
				// chip must all resolve to that chip's index.
				for _, x := range []int{span[0], (span[0] + span[1]) / 2, span[1] - 1} {
					require.Equalf(t, i, r.AttachmentAt(tc.atts, tc.width, x),
						"column %d should map to chip %d (span %v)", x, i, span)
				}
			}

			// A negative column and a column past the last chip resolve to -1.
			require.Equal(t, -1, r.AttachmentAt(tc.atts, tc.width, -1))
			last := spans[len(spans)-1][1]
			require.Equal(t, -1, r.AttachmentAt(tc.atts, tc.width, last))
		})
	}
}

func TestAttachmentAtMoreIndicatorBoundaryReturnsMinusOne(t *testing.T) {
	t.Parallel()
	r := newTestRenderer()

	// Choose a width that only fits a couple of chips so the trailing
	// "N more…" indicator is rendered and the overflow chips are dropped.
	maxItemWidth := r.maxItemWidth(false)
	width := maxItemWidth*3 - 1 // fits = floor(width/maxItemWidth)-1 == 1
	atts := []message.Attachment{
		img("a.png"), img("b.png"), img("c.png"), img("d.png"), img("e.png"),
	}

	spans := chipSpans(r, atts, width)
	require.Less(t, len(spans), len(atts), "some chips should be truncated behind 'N more…'")

	// Every hittable chip still resolves correctly.
	for i, span := range spans {
		require.Equal(t, i, r.AttachmentAt(atts, width, span[0]))
		require.Equal(t, i, r.AttachmentAt(atts, width, span[1]-1))
	}

	// The "N more…" region (past the last hittable chip) is not clickable.
	past := spans[len(spans)-1][1]
	require.Equal(t, -1, r.AttachmentAt(atts, width, past))
	require.Equal(t, -1, r.AttachmentAt(atts, width, past+1))
}
