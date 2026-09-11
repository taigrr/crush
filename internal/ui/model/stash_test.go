package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/completions"
)

func TestToggleStash_ParkRestoreSwap(t *testing.T) {
	t.Parallel()
	m := newTestUI()
	m.keyMap = DefaultKeyMap()
	m.completions = completions.New(m.com.Styles.Completions.Normal, m.com.Styles.Completions.Focused, m.com.Styles.Completions.Match)

	// Nothing to stash.
	_ = m.toggleStash()
	require.Nil(t, m.stash)

	// Park a draft with an attachment.
	m.textarea.SetValue("first draft")
	m.attachments.Set([]message.Attachment{{FilePath: "a.txt", MimeType: "text/plain", Content: []byte("x")}})
	_ = m.toggleStash()
	require.NotNil(t, m.stash)
	require.Equal(t, "first draft", m.stash.text)
	require.Len(t, m.stash.attachments, 1)
	require.Empty(t, m.textarea.Value())
	require.Empty(t, m.attachments.List())

	// Type something else, then swap.
	m.textarea.SetValue("urgent")
	_ = m.toggleStash()
	require.Equal(t, "first draft", m.textarea.Value())
	require.Len(t, m.attachments.List(), 1)
	require.Equal(t, "urgent", m.stash.text)
	require.Empty(t, m.stash.attachments)

	// Clear the editor and restore the swapped-out draft.
	m.textarea.Reset()
	m.attachments.Reset()
	_ = m.toggleStash()
	require.Nil(t, m.stash)
	require.Equal(t, "urgent", m.textarea.Value())
}

// Pills belong to the committed session and must not follow a preview.
func TestPillsAreaHidden_WhilePreviewing(t *testing.T) {
	t.Parallel()
	m := newTestUI()
	m.session = &session.Session{ID: "s1"}
	m.promptQueue = 2
	require.Positive(t, m.pillsAreaHeight())
	m.previewSessionID = "other"
	require.Zero(t, m.pillsAreaHeight())
}
