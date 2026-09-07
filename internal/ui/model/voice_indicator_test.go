package model

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/voice"
)

func TestVoiceIndicatorHiddenWhenIdle(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	require.Empty(t, u.renderVoiceIndicator(80))
	require.Empty(t, u.joinVoiceIndicatorRow(80))
}

func TestVoiceIndicatorShowsRecordingWithPulse(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.voice.dict.begin(false)
	u.voice.pulseOn = true

	on := u.renderVoiceIndicator(80)
	plain := ansi.Strip(on)
	require.Contains(t, plain, styles.VoiceRecordingIcon+" Recording")
	require.Contains(t, plain, "esc/enter to stop")
	require.LessOrEqual(t, ansi.StringWidth(on), 80)

	u.voice.pulseOn = false
	off := u.renderVoiceIndicator(80)
	require.Equal(t, plain, ansi.Strip(off), "pulse only changes color, not text")
	require.NotEqual(t, on, off, "pulse phases must render differently")

	narrow := u.renderVoiceIndicator(12)
	require.NotContains(t, ansi.Strip(narrow), "esc/enter", "hint dropped when it does not fit")
	require.LessOrEqual(t, ansi.StringWidth(narrow), 12)
}

func TestVoiceIndicatorFinishingWhileStopping(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.voice.dict.begin(false)
	u.voice.dict.release()
	require.Contains(t, ansi.Strip(u.renderVoiceIndicator(80)), "Finishing")
}

func TestVoicePulseTickIgnoresStaleGeneration(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.voice.dict.begin(false)
	u.voice.pulseGen = 2
	u.voice.pulseOn = true

	require.Nil(t, u.handleVoicePulseTick(voicePulseTickMsg{gen: 1}))
	require.True(t, u.voice.pulseOn)

	require.NotNil(t, u.handleVoicePulseTick(voicePulseTickMsg{gen: 2}))
	require.False(t, u.voice.pulseOn)

	u.voice.dict.reset()
	require.Nil(t, u.handleVoicePulseTick(voicePulseTickMsg{gen: 2}))
}

func TestVoiceStoppedCommitsInterimAndIdles(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	turn := u.voice.dict.begin(false)
	u.handleVoiceEvent(voice.Event{Kind: voice.EventInterim, Turn: turn, Text: "trailing words"})
	u.voice.dict.release()
	u.handleVoiceEvent(voice.Event{Kind: voice.EventStopped, Turn: turn})
	require.Equal(t, dictationIdle, u.voice.dict.state)
	require.Empty(t, u.voice.dict.phrases)
	require.Equal(t, "trailing words", u.textarea.Value())
	require.Empty(t, u.renderVoiceIndicator(80))
}
