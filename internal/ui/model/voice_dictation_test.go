package model

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/voice"
)

func newDictationFixture(value string) (*dictation, *textarea.Model) {
	ta := textarea.New()
	ta.SetWidth(40)
	ta.SetHeight(5)
	ta.ShowLineNumbers = false
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.SetValue(value)
	d := newDictation(&ta, lipgloss.NewStyle().Italic(true))
	return d, &ta
}

func setCaret(ta *textarea.Model, row, col int) {
	ta.MoveToBegin()
	for ta.Line() < row {
		ta.CursorDown()
	}
	ta.SetCursorColumn(col)
}

func interim(turn int, text string) voice.Event {
	return voice.Event{Kind: voice.EventInterim, Turn: turn, Text: text}
}

func final(turn int, text string) voice.Event {
	return voice.Event{Kind: voice.EventFinal, Turn: turn, Text: text}
}

func stopped(turn int) voice.Event { return voice.Event{Kind: voice.EventStopped, Turn: turn} }

func TestDictationInsertsAtCaretAndFinalReplaces(t *testing.T) {
	t.Parallel()
	d, ta := newDictationFixture("alpha omega")
	setCaret(ta, 0, 5)
	turn := d.begin(false)

	d.apply(interim(turn, "bet"))
	require.Equal(t, "alpha bet omega", ta.Value())
	require.Equal(t, 9, ta.Column(), "caret sits after the phrase")

	d.apply(interim(turn, "beta gam"))
	require.Equal(t, "alpha beta gam omega", ta.Value(), "interim is replaced, not appended")

	d.apply(final(turn, "beta gamma"))
	require.Equal(t, "alpha beta gamma omega", ta.Value())
	require.Empty(t, d.phrases, "final text is released to normal styling")
	require.Equal(t, 16, ta.Column())

	d.apply(interim(turn, "delta"))
	require.Equal(t, "alpha beta gamma delta omega", ta.Value(), "next phrase continues after the final")
}

func TestDictationPhraseTracksUserTyping(t *testing.T) {
	t.Parallel()
	d, ta := newDictationFixture("ab")
	turn := d.begin(false)
	d.apply(interim(turn, "spoken"))
	require.Equal(t, "ab spoken", ta.Value())

	ta.MoveToBegin()
	for _, r := range "XY " {
		*ta, _ = ta.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, "XY ab spoken", ta.Value())

	d.apply(interim(turn, "spoken words"))
	require.Equal(t, "XY ab spoken words", ta.Value(), "relocated, not duplicated")
	require.Equal(t, 3, ta.Column(), "caret left where the user put it")

	setCaret(ta, 0, 5)
	*ta, _ = ta.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	require.Equal(t, "XY ab! spoken words", ta.Value())
	d.sync()
	require.Equal(t, phrase{start: 6, end: 19}, d.phrases[turn])

	ta.MoveToBegin()
	ta.InsertString("first\n")
	d.apply(final(turn, "spoken words done"))
	require.Equal(t, "first\nXY ab! spoken words done", ta.Value())
	require.Empty(t, d.phrases)
}

func TestDictationLateFinalFromDrainingTurn(t *testing.T) {
	t.Parallel()
	d, ta := newDictationFixture("")
	t1 := d.begin(false)
	d.apply(interim(t1, "first phra"))
	d.release()
	t2 := d.begin(false)
	d.apply(interim(t2, "second"))
	require.Equal(t, "first phra second", ta.Value())

	d.apply(final(t1, "first phrase"))
	require.Equal(t, "first phrase second", ta.Value(), "turn 1 final replaces its own phrase")
	require.True(t, d.listening(), "turn 2 keeps recording")
	d.apply(stopped(t1))

	d.release()
	d.apply(final(t2, "second one"))
	require.Equal(t, "first phrase second one", ta.Value())
	require.Equal(t, dictationIdle, d.state)
}

func TestDictationStaleEventsAfterResetIgnored(t *testing.T) {
	t.Parallel()
	d, ta := newDictationFixture("")
	t1 := d.begin(false)
	d.apply(interim(t1, "draft"))
	d.commit()
	d.reset()
	ta.Reset()
	t2 := d.begin(false)
	d.apply(final(t1, "draft done"))
	require.Empty(t, ta.Value(), "reset turns are stale regardless of state")
	d.apply(interim(t2, "new"))
	require.Equal(t, "new", ta.Value())
}
