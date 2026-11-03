package dialog

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

func TestPairQR(t *testing.T) {
	t.Parallel()
	s := themes.CharmtonePantera()
	d := NewPairQR(&common.Common{Styles: &s}, "Pair Mobile Device", "rushpair1:test", "Scan with rush")
	require.NoError(t, d.err)
	require.Contains(t, d.qrText, "▀")

	scr := uv.NewScreenBuffer(120, 60)
	d.Draw(scr, uv.Rect(0, 0, 120, 60))
	require.Contains(t, scr.Render(), "Scan with rush")

	_, ok := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}).(ActionClose)
	require.True(t, ok)

	d.SetPayload(strings.Repeat("x", 4000))
	require.Error(t, d.err)
	require.Empty(t, d.qrText)
}

func TestPairOfferDecisions(t *testing.T) {
	t.Parallel()
	s := themes.CharmtonePantera()
	com := &common.Common{Styles: &s}
	offer := pairing.OfferView{OfferID: "abc", State: pairing.StateOpen, Payload: "rushpair1:x", ExpiresAt: time.Now().Add(time.Minute)}
	d := NewPairOffer(com, offer)
	require.NotEmpty(t, d.qrText)

	// Approve keys do nothing until the offer is claimed; closing cancels.
	require.Nil(t, d.HandleMsg(tea.KeyPressMsg{Code: 'y', Text: "y"}))
	act, ok := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}).(ActionPairDecision)
	require.True(t, ok)
	require.False(t, act.Approve)

	offer.State = pairing.StateClaimed
	offer.Device = &pairing.DeviceMeta{DeviceID: "dev_x", Name: "Phone"}
	offer.SAS = &pairing.SASView{Emoji: []string{"🐶", "🐱", "🦁", "🐎"}, Names: []string{"Dog", "Cat", "Lion", "Horse"}, Digits: "123 456"}
	d.SetOffer(offer)
	require.Empty(t, d.qrText, "the QR is hidden once claimed")
	scr := uv.NewScreenBuffer(120, 40)
	d.Draw(scr, uv.Rect(0, 0, 120, 40))
	require.Contains(t, scr.Render(), "123 456")

	act, ok = d.HandleMsg(tea.KeyPressMsg{Code: 'y', Text: "y"}).(ActionPairDecision)
	require.True(t, ok)
	require.True(t, act.Approve)
	act, ok = d.HandleMsg(tea.KeyPressMsg{Code: 'n', Text: "n"}).(ActionPairDecision)
	require.True(t, ok)
	require.False(t, act.Approve)

	offer.State = pairing.StateApproved
	d.SetOffer(offer)
	_, ok = d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}).(ActionClose)
	require.True(t, ok)
}
