package dialog

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
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
