// Package sound plays short notification sounds on the server, such as
// the end-of-turn chime. Playback is best-effort: any failure (no audio
// device, decode error, headless CI) is logged and swallowed so it never
// disrupts an agent run.
package sound

import (
	"bytes"
	"embed"
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

// Sound identifies a bundled notification sound. Each value maps to an
// embedded WAV file and to a configurable event in the user's config.
type Sound string

const (
	// EndOfTurn plays when an agent turn finishes successfully.
	EndOfTurn Sound = "end_of_turn"
	// Swarm plays when a swarm message is dispatched to another session.
	Swarm Sound = "swarm"
	// Blocked plays when a session becomes blocked awaiting the user
	// (permission prompt or question) — the red-border state.
	Blocked Sound = "blocked"
	// ToolError plays when a tool call fails.
	ToolError Sound = "tool_error"
	// Queued plays when a message is queued behind an active turn.
	Queued Sound = "queued"
)

// bundled holds the embedded default audio for each Sound.
//
//go:embed end_of_turn.wav swarm.wav blocked.wav tool_error.wav queued.wav
var bundled embed.FS

// bundledFile maps a Sound to its embedded file name.
var bundledFile = map[Sound]string{
	EndOfTurn: "end_of_turn.wav",
	Swarm:     "swarm.wav",
	Blocked:   "blocked.wav",
	ToolError: "tool_error.wav",
	Queued:    "queued.wav",
}

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

// open returns a reader for the sound to play. When path is non-empty the
// file at that path is used; otherwise the bundled default for s is
// returned. The returned name carries the correct extension so decode can
// pick the right decoder.
func open(s Sound, path string) (io.ReadCloser, string, error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, "", err
		}
		return f, path, nil
	}
	name := bundledFile[s]
	data, err := bundled.ReadFile(name)
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), name, nil
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

// PlayAsync plays a sound on a background goroutine and returns
// immediately, logging any error. Use this when the caller must not
// block on playback (e.g. an end-of-turn chime fired from a run's
// completion path).
func PlayAsync(s Sound, path string) {
	go func() {
		if err := Play(s, path); err != nil {
			slog.Debug("Failed to play notification sound", "sound", string(s), "path", path, "error", err)
		}
	}()
}
