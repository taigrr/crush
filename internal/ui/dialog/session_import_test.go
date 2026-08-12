package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/sessionimport"
	"github.com/taigrr/crush/internal/ui/common"
)

func TestSessionImportSelectsOneMultipleOrAll(t *testing.T) {
	dialog := NewSessionImport(common.DefaultCommon(nil), []sessionimport.SourceInfo{{Source: sessionimport.SourcePi, Name: "Pi"}})
	dialog.HandleMsg(sessionImportCandidatesMsg{
		source: sessionimport.SourceInfo{Source: sessionimport.SourcePi, Name: "Pi"},
		candidates: []sessionimport.Candidate{
			{Path: "/one", Title: "One", UpdatedAt: 200},
			{Path: "/two", Title: "Two", UpdatedAt: 100},
		},
	})

	dialog.HandleMsg(tea.KeyPressMsg{Code: ' '})
	require.Equal(t, []string{"/one"}, dialog.selection.IDs())
	dialog.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	dialog.HandleMsg(tea.KeyPressMsg{Code: ' '})
	require.Equal(t, []string{"/one", "/two"}, dialog.selection.IDs())

	dialog.HandleMsg(tea.KeyPressMsg{Code: 'a'})
	require.Empty(t, dialog.selection.IDs())
	dialog.HandleMsg(tea.KeyPressMsg{Code: 'a'})
	require.Equal(t, []string{"/one", "/two"}, dialog.selection.IDs())
}

func TestSessionImportBackReturnsToAgents(t *testing.T) {
	dialog := NewSessionImport(common.DefaultCommon(nil), []sessionimport.SourceInfo{{Source: sessionimport.SourcePi, Name: "Pi"}})
	dialog.HandleMsg(sessionImportCandidatesMsg{source: sessionimport.SourceInfo{Source: sessionimport.SourcePi, Name: "Pi"}})
	require.Equal(t, sessionImportSessions, dialog.stage)

	dialog.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, sessionImportSources, dialog.stage)
}
