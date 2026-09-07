package voice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePartialEvent(t *testing.T) {
	t.Parallel()
	ev, ok := parseSTTEvent([]byte(`{"type":"transcript.partial","text":"hello","is_final":false,"speech_final":false}`))
	require.True(t, ok)
	require.Equal(t, sttPartial, ev.Kind)
	require.Equal(t, "hello", ev.Text)
	require.False(t, ev.SpeechFinal)
}

func TestParseSpeechFinal(t *testing.T) {
	t.Parallel()
	ev, ok := parseSTTEvent([]byte(`{"type":"transcript.partial","text":"done","is_final":true,"speech_final":true}`))
	require.True(t, ok)
	require.True(t, ev.SpeechFinal)
	require.True(t, ev.IsFinal)
}

func TestParseCreatedAndDoneAndError(t *testing.T) {
	t.Parallel()
	ev, ok := parseSTTEvent([]byte(`{"type":"transcript.created"}`))
	require.True(t, ok)
	require.Equal(t, sttReady, ev.Kind)

	ev, ok = parseSTTEvent([]byte(`{"type":"transcript.done","text":"hello world"}`))
	require.True(t, ok)
	require.Equal(t, sttDone, ev.Kind)
	require.Equal(t, "hello world", ev.Text)

	ev, ok = parseSTTEvent([]byte(`{"type":"error","message":"boom"}`))
	require.True(t, ok)
	require.Equal(t, sttError, ev.Kind)
	require.Equal(t, "boom", ev.Message)

	_, ok = parseSTTEvent([]byte(`{"type":"unknown"}`))
	require.False(t, ok)
}

func TestCombinePromptWithVoiceText(t *testing.T) {
	t.Parallel()
	require.Equal(t, "hello", CombinePromptWithVoiceText("", "hello"))
	require.Equal(t, "hello", CombinePromptWithVoiceText("   ", "hello"))
	require.Equal(t, "hi there", CombinePromptWithVoiceText("hi", "there"))
	require.Equal(t, "hi\nthere", CombinePromptWithVoiceText("hi\n", "there"))
}

func TestIsCaptureSubcommand(t *testing.T) {
	t.Parallel()
	require.True(t, IsCaptureSubcommand([]string{"crush", "__mic-capture"}))
	require.True(t, IsCaptureSubcommand([]string{"crush", "__mic-capture", "--rate", "16000"}))
	require.False(t, IsCaptureSubcommand([]string{"crush"}))
	require.False(t, IsCaptureSubcommand([]string{"crush", "chat"}))
	require.False(t, IsCaptureSubcommand([]string{"crush", "chat", "__mic-capture"}))
}

func TestResampleIdentityAndDownmix(t *testing.T) {
	t.Parallel()
	in := []int16{0, 100, -100, 200}
	got := resampleMonoI16(in, 16000, 16000)
	require.Equal(t, in, got)
	stereo := []int16{10, 30, 20, 40}
	mono := framesToMonoI16(stereo, 2)
	require.Equal(t, []int16{20, 30}, mono)
}
