package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
)

func newTestArchiveConfirm(t *testing.T) *ArchiveConfirm {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	return NewArchiveConfirm(com)
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

// TestArchiveConfirm_CtrlYConfirms verifies ctrl+y confirms the archive.
func TestArchiveConfirm_CtrlYConfirms(t *testing.T) {
	t.Parallel()
	a := newTestArchiveConfirm(t)
	action := a.HandleMsg(ctrlKey('y'))
	_, ok := action.(ActionArchiveSession)
	require.True(t, ok, "ctrl+y should confirm archive")
}

// TestArchiveConfirm_SecondCtrlXConfirms verifies pressing the open-key
// (ctrl+x) again confirms, mirroring ctrl+c-twice in the quit dialog.
func TestArchiveConfirm_SecondCtrlXConfirms(t *testing.T) {
	t.Parallel()
	a := newTestArchiveConfirm(t)
	action := a.HandleMsg(ctrlKey('x'))
	_, ok := action.(ActionArchiveSession)
	require.True(t, ok, "second ctrl+x should confirm archive")
}

// TestArchiveConfirm_YConfirms verifies the plain y key confirms.
func TestArchiveConfirm_YConfirms(t *testing.T) {
	t.Parallel()
	a := newTestArchiveConfirm(t)
	action := a.HandleMsg(keyMsg('y'))
	_, ok := action.(ActionArchiveSession)
	require.True(t, ok)
}

// TestArchiveConfirm_EscCancels verifies esc dismisses without archiving.
func TestArchiveConfirm_EscCancels(t *testing.T) {
	t.Parallel()
	a := newTestArchiveConfirm(t)
	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, ok := action.(ActionClose)
	require.True(t, ok, "esc should close without archiving")
}

// TestArchiveConfirm_NoCancels verifies the "n" key cancels.
func TestArchiveConfirm_NoCancels(t *testing.T) {
	t.Parallel()
	a := newTestArchiveConfirm(t)
	action := a.HandleMsg(keyMsg('n'))
	_, ok := action.(ActionClose)
	require.True(t, ok)
}

// TestArchiveConfirm_EnterOnDefaultCancels verifies that with the default
// selection ("No"), enter cancels rather than archiving.
func TestArchiveConfirm_EnterOnDefaultCancels(t *testing.T) {
	t.Parallel()
	a := newTestArchiveConfirm(t)
	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, ok := action.(ActionClose)
	require.True(t, ok, "enter on default No selection should cancel")
}

// TestArchiveConfirm_EnterAfterSwitchConfirms verifies switching to "Yes"
// (via arrow/tab) then pressing enter confirms the archive.
func TestArchiveConfirm_EnterAfterSwitchConfirms(t *testing.T) {
	t.Parallel()
	a := newTestArchiveConfirm(t)
	a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab}) // No -> Yes
	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, ok := action.(ActionArchiveSession)
	require.True(t, ok, "enter after selecting Yes should archive")
}
