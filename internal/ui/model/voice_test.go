package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/voice"
)

func TestWrapVoiceInterim(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"the quick", "brown fox"}, wrapVoiceInterim("the quick brown fox", 10, 5))
	require.Empty(t, wrapVoiceInterim("hello", 0, 3))
	require.Empty(t, wrapVoiceInterim("hello", 10, 0))
}

func TestCombinePromptWithVoiceText(t *testing.T) {
	t.Parallel()
	require.Equal(t, "hello", voice.CombinePromptWithVoiceText("", "hello"))
	require.Equal(t, "hi there", voice.CombinePromptWithVoiceText("hi", "there"))
}

func TestIsVoiceChordPress(t *testing.T) {
	t.Parallel()
	require.True(t, isVoiceChordPress(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: ' '}))
	require.True(t, isVoiceChordPress(tea.KeyPressMsg{Code: tea.KeyF8}))
	require.False(t, isVoiceChordPress(tea.KeyPressMsg{Code: ' '}))
}

func TestVoiceInterimIgnoredAfterStop(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.voice = newVoiceSession()
	u.voice.state = voiceIdle
	cmd := u.handleVoiceEvent(voice.Event{Kind: voice.EventInterim, Text: "stale"})
	_ = cmd
	require.Empty(t, u.voice.interim)
}

func TestVoiceFinalAppendsAndClearsInterim(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.voice = newVoiceSession()
	u.voice.state = voiceListening
	u.voice.interim = "partial"
	u.handleVoiceEvent(voice.Event{Kind: voice.EventFinal, Text: "hello"})
	require.Empty(t, u.voice.interim)
	require.Equal(t, "hello", u.textarea.Value())
}

func TestVoiceErrorResetsState(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.voice = newVoiceSession()
	u.voice.state = voiceListening
	u.voice.interim = "partial"
	u.handleVoiceEvent(voice.Event{Kind: voice.EventError, Message: "boom"})
	require.Equal(t, voiceIdle, u.voice.state)
	require.Empty(t, u.voice.interim)
}

func TestCommitInterimIntoPrompt(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.voice = newVoiceSession()
	u.voice.state = voiceListening
	u.voice.interim = "world"
	u.textarea.SetValue("hello")
	got := u.commitVoiceInterim()
	require.Equal(t, "world", got)
	require.Equal(t, "hello world", u.textarea.Value())
	require.Empty(t, u.voice.interim)
}
