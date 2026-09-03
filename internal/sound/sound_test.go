package sound

import (
	"bytes"
	"io"
	"testing"

	"github.com/gopxl/beep/v2/wav"
	"github.com/stretchr/testify/require"
)

// TestBundledSoundsDecode verifies every bundled sound file is a valid,
// non-empty WAV that beep can decode. This runs without an audio device
// (no speaker.Init), so it is safe in headless CI.
func TestBundledSoundsDecode(t *testing.T) {
	t.Parallel()
	for s, name := range bundledFile {
		data, err := bundled.ReadFile(name)
		require.NoErrorf(t, err, "reading bundled sound %q", s)
		require.NotEmptyf(t, data, "bundled sound %q must not be empty", s)

		streamer, format, err := wav.Decode(io.NopCloser(bytes.NewReader(data)))
		require.NoErrorf(t, err, "decoding bundled sound %q", s)
		streamer.Close()
		require.Positivef(t, int(format.SampleRate), "sound %q sample rate", s)
	}
}
