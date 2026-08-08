package model

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/proto"
)

// attentionState is the window-chrome attention signal derived from the
// state of BACKGROUND sessions — every session OTHER than the one the
// user is currently viewing. The session in view never contributes: the
// user can already see its state, so the border only surfaces things
// happening off-screen.
type attentionState uint8

const (
	// attentionNone: no background session wants attention.
	attentionNone attentionState = iota
	// attentionReady: at least one background session is ready for
	// review (Unread && !IsBusy) and none is blocked on a prompt.
	attentionReady
	// attentionPending: at least one background session is blocked on a
	// permission/question prompt. Outranks ready — a blocked session
	// needs action now, which is more urgent than one merely waiting for
	// review.
	attentionPending
)

// sessionReady reports whether a session is waiting for review: it has
// unread activity and is not currently busy. This is the SINGLE source
// of truth for the "ready"/green predicate, shared by SessionCounts (the
// sidebar ready tally and the green row dot) and the window attention
// border, so the count, the dot, and the border can never disagree.
func sessionReady(sess proto.SessionOverview) bool {
	return sess.Unread && !sess.IsBusy
}

// BackgroundAttention computes the window attention signal, excluding
// currentSessionID. Pending (red) outranks ready (green): any background
// session blocked on a prompt wins regardless of any ready sessions.
//
// Pending is detected directly from the pendingSessions set (not gated
// on the session appearing in overviews yet), so a freshly-dispatched
// background session that hit a prompt before the next overview refresh
// still raises the red border. Ready is derived from overviews via the
// shared sessionReady predicate.
func (s *SessionsSidebar) BackgroundAttention(currentSessionID string) attentionState {
	for id := range s.pendingSessions {
		if id != currentSessionID {
			return attentionPending
		}
	}
	for _, ws := range s.overviews {
		for _, sess := range ws.Sessions {
			if sess.ID == currentSessionID {
				continue
			}
			if sessionReady(sess) {
				return attentionReady
			}
		}
	}
	return attentionNone
}

// attentionBorderColor returns the themed color for an attention state,
// or nil when no border should be drawn. Red reuses the destructive
// token (t.Resource.ErrorIcon) and green the ready/success token
// (t.Resource.OnlineIcon) — the SAME theme colors as the red and green
// row indicators, so the border adapts to the active theme rather than
// hardcoding hex.
func (m *UI) attentionBorderColor(state attentionState) color.Color {
	if m.com == nil || m.com.Styles == nil {
		return nil
	}
	t := m.com.Styles
	switch state {
	case attentionPending:
		return t.Resource.ErrorIcon.GetForeground()
	case attentionReady:
		return t.Resource.OnlineIcon.GetForeground()
	default:
		return nil
	}
}

// drawAttentionBorder tints the app's outer frame when a background
// session wants attention: red when one is blocked on a prompt, green
// when one is ready for review (red wins). It draws only the perimeter
// cells of the full screen area, which fall in the 1-cell margin the
// layout already reserves around the app content — so it recolors the
// existing (normally empty) frame without consuming any layout space and
// without a border in the no-attention state.
//
// Only the top edge, the two top corners, and the left/right vertical
// runs are drawn — the bottom edge and bottom corners are blanked. That
// bottom row is the full-width help/status band (drawn afterward), which
// does not span the reserved side margins, so a drawn bottom corner would
// leave a stray colored glyph on the last row. An open-bottom frame
// avoids that entirely.
func (m *UI) drawAttentionBorder(scr uv.Screen, area uv.Rectangle) {
	if m.leftSidebar == nil {
		return
	}
	// Guard degenerate sizes (early init / tiny resize): a border needs
	// at least a 2x2 area to have a distinct perimeter.
	if area.Dx() < 2 || area.Dy() < 2 {
		return
	}
	cur := ""
	if m.session != nil {
		cur = m.session.ID
	}
	col := m.attentionBorderColor(m.leftSidebar.BackgroundAttention(cur))
	if col == nil {
		return
	}
	border := uv.RoundedBorder().Style(uv.Style{Fg: col})
	// Blank the bottom edge + bottom corners so no stray colored glyphs
	// land on the help row.
	border.Bottom = uv.Side{}
	border.BottomLeft = uv.Side{}
	border.BottomRight = uv.Side{}
	border.Draw(scr, area)
}
