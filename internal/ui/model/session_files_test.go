package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/session"
)

// TestSessionFilesUpdate_StaleSessionIgnored verifies that a session
// file reload that completes after the user has switched sessions does
// not clobber the now-current session's file list. This guards against
// per-session state leaking across a session switch.
func TestSessionFilesUpdate_StaleSessionIgnored(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.session = &session.Session{ID: "session-B"}
	current := []SessionFile{{LatestVersion: history.File{Path: "current-B.go"}}}
	u.sessionFiles = current

	// A reload tagged for the previously-viewed session-A arrives late.
	u.applySessionFilesUpdate(sessionFilesUpdatesMsg{
		sessionID:    "session-A",
		sessionFiles: []SessionFile{{LatestVersion: history.File{Path: "stale-A.go"}}},
	})

	require.Len(t, u.sessionFiles, 1)
	require.Equal(t, "current-B.go", u.sessionFiles[0].LatestVersion.Path,
		"stale session-A files must not overwrite current session-B files")
}

// TestSessionFilesUpdate_CurrentSessionApplied verifies the matching
// case is still applied.
func TestSessionFilesUpdate_CurrentSessionApplied(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.session = &session.Session{ID: "session-B"}

	u.applySessionFilesUpdate(sessionFilesUpdatesMsg{
		sessionID:    "session-B",
		sessionFiles: []SessionFile{{LatestVersion: history.File{Path: "fresh-B.go"}}},
	})

	require.Len(t, u.sessionFiles, 1)
	require.Equal(t, "fresh-B.go", u.sessionFiles[0].LatestVersion.Path)
}
