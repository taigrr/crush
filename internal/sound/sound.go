// Package sound plays short notification sounds on the server, such as
// the end-of-turn chime. Playback is best-effort: any failure (no audio
// device, decode error, headless CI) is logged and swallowed so it never
// disrupts an agent run.
package sound

import (
	"bytes"
	_ "embed"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/wav"
)

// defaultChime is the bundled end-of-turn sound, played when the user
// has not configured a custom sound file.
//
//go:embed default.wav
var defaultChime []byte

// speakerSampleRate is the sample rate the shared speaker is initialized
// with. The bundled chime is authored at this rate; custom files with a
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
// be WAV or MP3; the bundled default is WAV.
func decode(name string, r io.ReadCloser) (beep.StreamSeekCloser, beep.Format, error) {
	if strings.EqualFold(filepath.Ext(name), ".mp3") {
		return mp3.Decode(r)
	}
	return wav.Decode(r)
}

// Play plays a notification sound and blocks until it finishes. If path
// is non-empty the file at that path is played; otherwise the bundled
// default chime is used. Any error is returned (and callers typically
// just log it) — playback failures are never fatal.
func Play(path string) error {
	if err := initSpeaker(); err != nil {
		return err
	}

	var (
		rc   io.ReadCloser
		name string
	)
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		rc, name = f, path
	} else {
		rc, name = io.NopCloser(bytes.NewReader(defaultChime)), "default.wav"
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

// PlayAsync plays a sound on a background goroutine and returns
// immediately, logging any error. Use this when the caller must not
// block on playback (e.g. an end-of-turn chime fired from a run's
// completion path).
func PlayAsync(path string) {
	go func() {
		if err := Play(path); err != nil {
			slog.Debug("Failed to play notification sound", "path", path, "error", err)
		}
	}()
}
