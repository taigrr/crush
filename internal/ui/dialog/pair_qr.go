package dialog

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/qr"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
)

// PairQRID is the identifier for the mobile pairing QR dialog.
const PairQRID = "pair_qr"

// PairQR shows a pairing offer: its QR code while open, then the SAS to
// compare with the phone and Approve / Reject.
type PairQR struct {
	com     *common.Common
	title   string
	caption string
	qrText  string
	err     error
	offer   pairing.OfferView
	keyMap  struct{ Approve, Reject, Close key.Binding }
}

var _ Dialog = (*PairQR)(nil)

// NewPairQR creates a dialog that renders payload as a QR code.
func NewPairQR(com *common.Common, title, payload, caption string) *PairQR {
	d := &PairQR{com: com, title: title, caption: caption}
	d.keyMap.Approve = key.NewBinding(key.WithKeys("y", "Y"), key.WithHelp("y", "codes match: approve"))
	d.keyMap.Reject = key.NewBinding(key.WithKeys("n", "N"), key.WithHelp("n", "codes differ: reject"))
	d.keyMap.Close = CloseKey
	d.SetPayload(payload)
	return d
}

// NewPairOffer creates the dialog for a pairing offer.
func NewPairOffer(com *common.Common, offer pairing.OfferView) *PairQR {
	d := NewPairQR(com, "Pair Mobile Device", offer.Payload, "")
	d.SetOffer(offer)
	return d
}

// SetPayload replaces the encoded payload.
func (d *PairQR) SetPayload(payload string) {
	if payload == "" {
		d.qrText, d.err = "", nil
		return
	}
	code, err := qr.Encode([]byte(payload), qr.Medium)
	if err != nil {
		d.qrText, d.err = "", err
		return
	}
	d.qrText, d.err = code.HalfBlocks(false), nil
}

// SetCaption replaces the text under the code.
func (d *PairQR) SetCaption(caption string) { d.caption = caption }

// SetOffer updates the dialog from the latest offer state.
func (d *PairQR) SetOffer(offer pairing.OfferView) {
	d.offer = offer
	if offer.State != pairing.StateOpen {
		d.qrText = ""
	}
	switch offer.State {
	case pairing.StateOpen:
		d.caption = fmt.Sprintf("Scan with rush. Expires at %s.", offer.ExpiresAt.Local().Format(time.Kitchen))
	case pairing.StateClaimed:
		if offer.OperatorOK {
			d.caption = "Approved here; waiting for the phone to confirm..."
		} else {
			d.caption = "Approve only if the phone shows exactly this code."
		}
	default:
		d.caption = "Pairing " + string(offer.State) + "."
	}
}

// OfferID returns the offer this dialog follows.
func (d *PairQR) OfferID() string { return d.offer.OfferID }

func (d *PairQR) awaitingDecision() bool {
	return d.offer.State == pairing.StateClaimed && !d.offer.OperatorOK
}

// ID implements [Model].
func (*PairQR) ID() string { return PairQRID }

// HandleMsg implements [Model].
func (d *PairQR) HandleMsg(msg tea.Msg) Action {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case d.awaitingDecision() && key.Matches(kp, d.keyMap.Approve):
		return ActionPairDecision{OfferID: d.offer.OfferID, Approve: true}
	case d.awaitingDecision() && key.Matches(kp, d.keyMap.Reject):
		return ActionPairDecision{OfferID: d.offer.OfferID}
	case key.Matches(kp, d.keyMap.Close):
		if d.offer.OfferID != "" && !d.offer.State.Terminal() {
			// Closing an unfinished pairing cancels it.
			return ActionPairDecision{OfferID: d.offer.OfferID}
		}
		return ActionClose{}
	}
	return nil
}

// Draw implements [Dialog].
func (d *PairQR) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	parts := []string{d.title, ""}
	if d.qrText != "" {
		parts = append(parts, styles.QRCode().Render(d.qrText), "")
	}
	if dev := d.offer.Device; dev != nil {
		parts = append(parts, fmt.Sprintf("Claimed by %q (%s)", dev.Name, dev.DeviceID))
	}
	if sas := d.offer.SAS; sas != nil {
		parts = append(parts, "",
			strings.Join(sas.Emoji, "  ")+"    "+sas.Digits,
			strings.Join(sas.Names, ", "), "")
	}
	body := d.caption
	if d.err != nil {
		body = d.err.Error()
	}
	parts = append(parts, body)
	if d.awaitingDecision() {
		parts = append(parts, "", "y approve · n reject")
	}
	content := d.com.Styles.Dialog.Quit.Content.Render(lipgloss.JoinVertical(lipgloss.Center, parts...))
	DrawCenter(scr, area, d.com.Styles.Dialog.Quit.Frame.Render(content))
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *PairQR) ShortHelp() []key.Binding {
	if d.awaitingDecision() {
		return []key.Binding{d.keyMap.Approve, d.keyMap.Reject, d.keyMap.Close}
	}
	return []key.Binding{d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *PairQR) FullHelp() [][]key.Binding { return [][]key.Binding{d.ShortHelp()} }
