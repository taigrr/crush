package dialog

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/qr"
	"github.com/taigrr/crush/internal/ui/common"
)

// PairQRID is the identifier for the mobile pairing QR dialog.
const PairQRID = "pair_qr"

// pairQRStyle pins explicit colors so the code scans the same on dark and
// light terminals: glyphs mark light modules.
var pairQRStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#FFFFFF")).
	Background(lipgloss.Color("#000000"))

// PairQR shows a pairing payload as a QR code with a caption.
type PairQR struct {
	com     *common.Common
	title   string
	caption string
	qrText  string
	err     error
	keyMap  struct{ Close key.Binding }
}

var _ Dialog = (*PairQR)(nil)

// NewPairQR creates a dialog that renders payload as a QR code.
func NewPairQR(com *common.Common, title, payload, caption string) *PairQR {
	d := &PairQR{com: com, title: title, caption: caption}
	d.keyMap.Close = CloseKey
	d.SetPayload(payload)
	return d
}

// SetPayload replaces the encoded payload.
func (d *PairQR) SetPayload(payload string) {
	code, err := qr.Encode([]byte(payload), qr.Medium)
	if err != nil {
		d.qrText, d.err = "", err
		return
	}
	d.qrText, d.err = code.HalfBlocks(false), nil
}

// SetCaption replaces the text under the code (e.g. a countdown or SAS).
func (d *PairQR) SetCaption(caption string) { d.caption = caption }

// ID implements [Model].
func (*PairQR) ID() string { return PairQRID }

// HandleMsg implements [Model].
func (d *PairQR) HandleMsg(msg tea.Msg) Action {
	if kp, ok := msg.(tea.KeyPressMsg); ok && key.Matches(kp, d.keyMap.Close) {
		return ActionClose{}
	}
	return nil
}

// Draw implements [Dialog].
func (d *PairQR) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	body := d.caption
	if d.err != nil {
		body = d.err.Error()
	}
	parts := []string{d.title, ""}
	if d.qrText != "" {
		parts = append(parts, pairQRStyle.Render(d.qrText), "")
	}
	parts = append(parts, body)
	content := d.com.Styles.Dialog.Quit.Content.Render(lipgloss.JoinVertical(lipgloss.Center, parts...))
	DrawCenter(scr, area, d.com.Styles.Dialog.Quit.Frame.Render(content))
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *PairQR) ShortHelp() []key.Binding { return []key.Binding{d.keyMap.Close} }

// FullHelp implements [help.KeyMap].
func (d *PairQR) FullHelp() [][]key.Binding { return [][]key.Binding{{d.keyMap.Close}} }
