//go:build darwin || windows || js || cgo

package sound

// The beep/oto playback backend. oto is pure Go on darwin, windows and js;
// on linux, the BSDs and android it links ALSA/oboe through cgo. Release
// binaries are cross-compiled with CGO_ENABLED=0, so on those platforms this
// file is excluded and sound_stub.go provides a silent Play. Local/dev
// builds (cgo on) keep full playback everywhere.

import (
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/wav"
)

// speakerSampleRate is the sample rate the shared speaker is initialized
// with. The bundled sounds are authored at this rate; custom files with a
// different rate are resampled to match.
const speakerSampleRate beep.SampleRate = 44100

var (
	initOnce sync.Once
	initErr  error
)

// initSpeaker initializes the process-global beep speaker exactly once.
// speaker.Init may only be called once per process, so all playback
// shares a single device opened at speakerSampleRate.
func initSpeaker() error {
	initOnce.Do(func() {
		initErr = speaker.Init(speakerSampleRate, speakerSampleRate.N(time.Second/10))
	})
	return initErr
}

// decode picks a decoder based on the file extension. Custom paths may
// be WAV or MP3; the bundled defaults are WAV.
func decode(name string, r io.ReadCloser) (beep.StreamSeekCloser, beep.Format, error) {
	if strings.EqualFold(filepath.Ext(name), ".mp3") {
		return mp3.Decode(r)
	}
	return wav.Decode(r)
}

// Play plays the given sound and blocks until it finishes. If path is
// non-empty the file at that path is played; otherwise the bundled
// default for s is used. Any error is returned (and callers typically
// just log it) — playback failures are never fatal.
func Play(s Sound, path string) error {
	if err := initSpeaker(); err != nil {
		return err
	}

	rc, name, err := open(s, path)
	if err != nil {
		return err
	}

	streamer, format, err := decode(name, rc)
	if err != nil {
		rc.Close()
		return err
	}
	defer streamer.Close()

	// Resample custom files whose rate differs from the speaker's so
	// they play at the correct pitch and speed.
	var src beep.Streamer = streamer
	if format.SampleRate != speakerSampleRate {
		src = beep.Resample(4, format.SampleRate, speakerSampleRate, streamer)
	}

	done := make(chan struct{})
	speaker.Play(beep.Seq(src, beep.Callback(func() { close(done) })))
	<-done
	return nil
}
