package model

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
)

// pairWaitMS bounds each long-poll for a pairing offer change.
const pairWaitMS = 25_000

type (
	// pairOfferMsg carries a newly created or changed pairing offer.
	pairOfferMsg struct {
		offer pairing.OfferView
		err   error
	}
	// pairDecisionMsg reports the result of an approve or reject.
	pairDecisionMsg struct {
		offer pairing.OfferView
		err   error
	}
)

// openPairDialog creates a pairing offer on the attached server.
func (m *UI) openPairDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.PairQRID) {
		m.dialog.BringToFront(dialog.PairQRID)
		return nil
	}
	ws := m.com.Workspace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		offer, err := ws.PairCreateOffer(ctx)
		return pairOfferMsg{offer: offer, err: err}
	}
}

func (m *UI) waitPairOffer(offer pairing.OfferView) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), (pairWaitMS+5000)*time.Millisecond)
		defer cancel()
		next, err := ws.PairWaitOffer(ctx, offer.OfferID, offer.State, pairWaitMS)
		return pairOfferMsg{offer: next, err: err}
	}
}

// handlePairOffer opens or updates the pairing dialog and keeps following
// the offer until it ends or the dialog closes.
func (m *UI) handlePairOffer(msg pairOfferMsg) tea.Cmd {
	if msg.err != nil {
		return m.failPair(msg.err)
	}
	d, ok := m.dialog.Dialog(dialog.PairQRID).(*dialog.PairQR)
	switch {
	case !ok && msg.offer.State == pairing.StateOpen:
		d = dialog.NewPairOffer(m.com, msg.offer)
		m.dialog.OpenDialog(d)
	case !ok || d.OfferID() != msg.offer.OfferID:
		// The dialog was closed (which rejects the offer) or already shows
		// another offer; never leave a second open offer behind.
		if msg.offer.State == pairing.StateOpen {
			ws, id := m.com.Workspace, msg.offer.OfferID
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = ws.PairReject(ctx, id)
				return nil
			}
		}
		return nil
	default:
		d.SetOffer(msg.offer)
	}
	return m.pairOutcome(msg.offer)
}

func (m *UI) pairOutcome(offer pairing.OfferView) tea.Cmd {
	switch offer.State {
	case pairing.StateApproved:
		m.dialog.CloseDialog(dialog.PairQRID)
		name := "device"
		if offer.Device != nil {
			name = offer.Device.Name
		}
		return util.ReportInfo(fmt.Sprintf("Paired %s. It is trusted on every node.", name))
	case pairing.StateAborted:
		m.dialog.CloseDialog(dialog.PairQRID)
		if offer.Reason == pairing.ReasonSASMismatch {
			return util.ReportError(fmt.Errorf("the phone reported that the codes did not match (possible interception); pairing aborted"))
		}
		return util.ReportWarn("Pairing aborted.")
	case pairing.StateExpired:
		m.dialog.CloseDialog(dialog.PairQRID)
		return util.ReportWarn("Pairing code expired.")
	case pairing.StateRejected:
		m.dialog.CloseDialog(dialog.PairQRID)
		return util.ReportInfo("Pairing rejected.")
	}
	return m.waitPairOffer(offer)
}

// decidePair approves or rejects (or cancels) the offer.
func (m *UI) decidePair(d dialog.ActionPairDecision) tea.Cmd {
	ws := m.com.Workspace
	if !d.Approve {
		m.dialog.CloseDialog(dialog.PairQRID)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if d.Approve {
			offer, err := ws.PairApprove(ctx, d.OfferID)
			return pairDecisionMsg{offer: offer, err: err}
		}
		offer, err := ws.PairReject(ctx, d.OfferID)
		return pairDecisionMsg{offer: offer, err: err}
	}
}

// failPair closes the dialog after an error and cancels its offer, so a
// phone cannot finish a pairing the user saw fail.
func (m *UI) failPair(err error) tea.Cmd {
	cmds := []tea.Cmd{util.ReportError(fmt.Errorf("pair mobile device: %w", err))}
	if d, ok := m.dialog.Dialog(dialog.PairQRID).(*dialog.PairQR); ok && d.OfferID() != "" {
		ws, id := m.com.Workspace, d.OfferID()
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = ws.PairReject(ctx, id)
			return nil
		})
	}
	m.dialog.CloseDialog(dialog.PairQRID)
	return tea.Batch(cmds...)
}

func (m *UI) handlePairDecision(msg pairDecisionMsg) tea.Cmd {
	if msg.err != nil {
		return m.failPair(msg.err)
	}
	if d, ok := m.dialog.Dialog(dialog.PairQRID).(*dialog.PairQR); ok && d.OfferID() == msg.offer.OfferID {
		d.SetOffer(msg.offer)
		if msg.offer.State == pairing.StateApproved {
			return m.pairOutcome(msg.offer)
		}
		// The wait loop already in flight reports the phone's confirmation.
		return nil
	}
	if msg.offer.State == pairing.StateRejected {
		return util.ReportInfo("Pairing cancelled.")
	}
	return nil
}
