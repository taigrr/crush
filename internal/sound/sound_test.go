package sound

import (
	"bytes"
	"io"
	"testing"

	"github.com/gopxl/beep/v2/wav"
	"github.com/stretchr/testify/require"
)

// TestDefaultChimeDecodes verifies the bundled sound file is a valid,
// non-empty WAV that beep can decode. This runs without an audio device
// (no speaker.Init), so it is safe in headless CI.
func TestDefaultChimeDecodes(t *testing.T) {
	t.Parallel()
	require.NotEmpty(t, defaultChime, "bundled default chime must not be empty")

	streamer, format, err := wav.Decode(io.NopCloser(bytes.NewReader(defaultChime)))
	require.NoError(t, err)
	defer streamer.Close()
	require.Positive(t, int(format.SampleRate))
}
