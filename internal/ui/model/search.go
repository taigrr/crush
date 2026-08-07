package model

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
)

// searchDebounce is how long keystrokes must be quiet before the search
// palette fires a search. Keeps typing responsive while avoiding a query
// per keystroke.
const searchDebounce = 250 * time.Millisecond

// searchDebounceMsg fires after the debounce interval for a given
// generation. If the generation is stale (the user kept typing) it is
// dropped without running a search.
type searchDebounceMsg struct {
	gen           int
	query         string
	semantic      *bool
	allWorkspaces bool
}

// searchResultMsg carries the outcome of a history search back to the
// palette. gen ties it to the query generation that requested it so
// out-of-order responses are dropped.
type searchResultMsg struct {
	gen    int
	result proto.SearchHistoryResult
	err    error
}

// openSearchPaletteDialog opens the semantic search palette, or brings it
// to the front if it is already open. Opening bumps the debounce
// generation so any search still in flight from a previous palette
// session is treated as stale and cannot populate the fresh dialog.
func (m *UI) openSearchPaletteDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SearchPaletteID) {
		m.dialog.BringToFront(dialog.SearchPaletteID)
		return nil
	}
	m.searchGen++
	m.dialog.OpenDialog(dialog.NewSearchPalette(m.com))
	return nil
}

// handleSearchQueryChanged debounces a query change from the palette: it
// bumps the generation counter and schedules a debounce tick carrying
// that generation. Any input cmd (cursor blink) is preserved.
func (m *UI) handleSearchQueryChanged(a dialog.ActionSearchQueryChanged) tea.Cmd {
	m.searchGen++
	gen := m.searchGen

	var cmds []tea.Cmd
	if a.InputCmd != nil {
		cmds = append(cmds, a.InputCmd)
	}
	cmds = append(cmds, tea.Tick(searchDebounce, func(time.Time) tea.Msg {
		return searchDebounceMsg{gen: gen, query: a.Query, semantic: a.Semantic, allWorkspaces: a.AllWorkspaces}
	}))
	return tea.Batch(cmds...)
}

// handleSearchDebounce runs the search for a debounce tick if it is still
// the latest generation and the query is non-empty. It marks the palette
// as loading and issues the RPC off the Update loop.
func (m *UI) handleSearchDebounce(msg searchDebounceMsg) tea.Cmd {
	if msg.gen != m.searchGen {
		return nil
	}
	palette := m.searchPalette()
	if palette == nil {
		return nil
	}
	if msg.query == "" {
		palette.SetResults(nil, false)
		// No selection to preview: restore the committed view.
		return m.cancelPreview()
	}
	palette.SetLoading(true)
	return m.runSearchCmd(msg.gen, msg.query, msg.semantic, msg.allWorkspaces)
}

// runSearchCmd returns a tea.Cmd that performs the history search RPC and
// wraps the outcome in a searchResultMsg tagged with the generation. The
// palette searches all message roles (Scope "all") so a query can match
// assistant replies, not just what the user typed.
func (m *UI) runSearchCmd(gen int, query string, semantic *bool, allWorkspaces bool) tea.Cmd {
	return func() tea.Msg {
		res, err := m.com.Workspace.SearchHistory(context.Background(), proto.SearchHistoryParams{
			Query:         query,
			Scope:         "all",
			Semantic:      semantic,
			AllWorkspaces: allWorkspaces,
		})
		return searchResultMsg{gen: gen, result: res, err: err}
	}
}

// handleSearchResult feeds a search result back into the palette, dropping
// stale responses. On error it clears results and surfaces the error.
func (m *UI) handleSearchResult(msg searchResultMsg) tea.Cmd {
	if msg.gen != m.searchGen {
		return nil
	}
	palette := m.searchPalette()
	if palette == nil {
		return nil
	}
	if msg.err != nil {
		palette.SetResults(nil, false)
		return tea.Batch(m.cancelPreview(), util.ReportError(msg.err))
	}
	palette.SetResults(msg.result.Hits, msg.result.SemanticUsed)
	// Preview the top result as it becomes selected; when there are no
	// hits, cancel any active preview so the committed view is restored
	// instead of leaving a stale session shown behind the palette.
	if hit, ok := palette.SelectedHit(); ok {
		return m.previewSearchResult(hit)
	}
	return m.cancelPreview()
}

// previewSearchResult is the preview seam: as the palette selection moves,
// the highlighted session hot-loads (read-only, debounced) in the main
// view via the shared live-preview mechanism. Only current-workspace hits
// preview; a foreign-workspace hit (cross-workspace step 2) cancels any
// active preview and loads only on commit, matching the sidebar's
// foreign-workspace gating.
func (m *UI) previewSearchResult(hit proto.SessionHit) tea.Cmd {
	return m.schedulePreview(hit.SessionID, m.isCurrentWorkspace(hit.WorkspaceRoot))
}

// commitSearchResult closes the palette and opens the chosen session. For
// a current-workspace hit it loads directly; for a foreign-workspace hit
// (cross-workspace search) it switches this client to that workspace first
// and then loads the session, reusing the same switch-then-open path the
// sidebar uses. Any active live preview is discarded by the loadSessionMsg
// / workspaceSwitchedMsg handlers when the committed session lands.
func (m *UI) commitSearchResult(hit proto.SessionHit) tea.Cmd {
	m.dialog.CloseDialog(dialog.SearchPaletteID)
	// A current-workspace hit (or one with no root — defensive: the
	// backend always stamps a root, but never route an empty root into
	// SwitchWorkspace, which would error) loads directly.
	if hit.WorkspaceRoot == "" || m.isCurrentWorkspace(hit.WorkspaceRoot) {
		return m.loadSession(hit.SessionID)
	}
	return m.switchWorkspaceAndLoad(hit.WorkspaceRoot, hit.SessionID)
}

// searchPalette returns the open search palette dialog, or nil.
func (m *UI) searchPalette() *dialog.SearchPalette {
	d := m.dialog.Dialog(dialog.SearchPaletteID)
	if d == nil {
		return nil
	}
	palette, _ := d.(*dialog.SearchPalette)
	return palette
}
