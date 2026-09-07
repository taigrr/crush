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
