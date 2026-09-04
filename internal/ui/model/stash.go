package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/util"
)

// promptStash is a parked editor draft: the text and any attachments the
// user had assembled but wants to hold back while sending something else.
type promptStash struct {
	text        string
	attachments []message.Attachment
}

// toggleStash implements the stash keybinding:
//
//   - editor has a draft, stash empty  -> park the draft, clear the editor
//   - editor empty, stash present      -> restore the draft
//   - both present                     -> swap them
//   - both empty                       -> nothing to do
//
// The draft includes attachments, and restoring puts the cursor at the end
// so the user can keep typing. Completions are closed because the restored
// text may not correspond to the open popup.
func (m *UI) toggleStash() tea.Cmd {
	text := m.textarea.Value()
	attachments := m.attachments.List()
	hasDraft := strings.TrimSpace(text) != "" || len(attachments) > 0

	switch {
	case !hasDraft && m.stash == nil:
		return util.ReportInfo("Nothing to stash")
	case !hasDraft:
		m.restoreStash()
		return util.ReportInfo("Prompt restored")
	case m.stash == nil:
		m.stash = &promptStash{text: text, attachments: attachments}
		m.clearEditor()
		return util.ReportInfo("Prompt stashed; press again to restore")
	default:
		incoming := m.stash
		m.stash = &promptStash{text: text, attachments: attachments}
		m.applyStash(incoming)
		return util.ReportInfo("Prompt swapped with stash")
	}
}

func (m *UI) restoreStash() {
	st := m.stash
	m.stash = nil
	m.applyStash(st)
}

func (m *UI) applyStash(st *promptStash) {
	m.closeCompletions()
	m.textarea.SetValue(st.text)
	m.textarea.MoveToEnd()
	m.attachments.Set(st.attachments)
	m.updateLayoutAndSize()
}

func (m *UI) clearEditor() {
	m.closeCompletions()
	m.textarea.Reset()
	m.attachments.Reset()
	m.updateLayoutAndSize()
}

// HasStash reports whether a prompt is parked.
func (m *UI) HasStash() bool { return m.stash != nil }
