package voice

import (
	"context"
	"crypto/tls"
	"strings"
	"time"
)

const (
	backlogMaxChunks = 1024
	noSpeechTimeout  = 10 * time.Second
	// drainTimeout bounds how long a released session waits for the
	// server's final transcript after `audio.done` is sent.
	drainTimeout = 5 * time.Second
)

// Command is a control signal from the TUI event loop.
type Command struct {
	Kind CommandKind
	// Turn identifies the dictation turn a Press starts. It is echoed on
	// every [Event] the turn produces so the UI can tell a draining
	// turn's late final apart from the current turn's events.
	Turn int
}

// CommandKind is the type of a [Command].
type CommandKind int

const (
	CmdPress CommandKind = iota
	CmdRelease
	CmdShutdown
)

// Press starts dictation turn id.
func Press(id int) Command { return Command{Kind: CmdPress, Turn: id} }

// Release ends the current turn, keeping its final transcript.
func Release() Command { return Command{Kind: CmdRelease} }

// Shutdown stops the pipeline.
func Shutdown() Command { return Command{Kind: CmdShutdown} }

// captureFunc opens a microphone; see [SpawnPCMCapture].
type captureFunc func(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error)

// deps are the pipeline's external effects. Production uses
// defaultDeps; tests substitute a fake microphone and a dialer that
// trusts a local test server.
type deps struct {
	capture captureFunc
	tls     *tls.Config
}

var defaultDeps = deps{capture: SpawnPCMCapture}

// turn is one in-flight dictation session.
type turn struct {
	id      int
	cancel  context.CancelFunc
	release chan struct{}
	done    chan struct{}
}

func (t *turn) released() bool {
	select {
	case <-t.release:
		return true
	default:
		return false
	}
}

func (t *turn) markReleased() {
	if !t.released() {
		close(t.release)
	}
}

// RunPipeline consumes commands until CmdShutdown. Events are delivered
// on eventCh. bearerFn is resolved at each connect so rotating OAuth
// tokens stay valid; a 401/403 handshake triggers one forced refresh.
//
// A press that arrives while the previous turn is still draining its
// final transcript is held until that turn finishes (bounded by
// drainTimeout) so the two turns' events never interleave; the loop keeps
// servicing commands meanwhile, so a shutdown or an early release of the
// pending press is honoured immediately.
func RunPipeline(ctx context.Context, cfg Config, bearerFn BearerFunc, cmdCh <-chan Command, eventCh chan<- Event) {
	runPipeline(ctx, cfg, bearerFn, cmdCh, eventCh, defaultDeps)
}

func runPipeline(ctx context.Context, cfg Config, bearerFn BearerFunc, cmdCh <-chan Command, eventCh chan<- Event, d deps) {
	cfg = cfg.Normalize()
	var active *turn
	pendingPress := -1

	emit := func(ev Event) {
		select {
		case eventCh <- ev:
		case <-ctx.Done():
		}
	}
	cancelActive := func() {
		if active != nil {
			active.cancel()
			active = nil
		}
	}
	defer cancelActive()

	startTurn := func(id int) {
		cancelActive()
		sessionCtx, cancel := context.WithCancel(ctx)
		t := &turn{id: id, cancel: cancel, release: make(chan struct{}), done: make(chan struct{})}
		active = t
		go func() {
			defer close(t.done)
			defer cancel()
			err := runCaptureSession(sessionCtx, cfg, bearerFn, id, t.release, eventCh, d)
			out := Event{Kind: EventStopped, Turn: id}
			if err != nil && sessionCtx.Err() == nil {
				out = Event{Kind: EventError, Turn: id, Message: err.Error()}
			}
			// A turn cancelled by a newer press still reports Stopped so
			// the UI releases its interim; use the parent ctx since the
			// session ctx is already done in that case.
			select {
			case eventCh <- out:
			case <-ctx.Done():
			}
		}()
	}

	for {
		var draining <-chan struct{}
		if pendingPress >= 0 && active != nil {
			draining = active.done
		}
		select {
		case <-ctx.Done():
			return
		case <-draining:
			id := pendingPress
			pendingPress = -1
			startTurn(id)
		case cmd, ok := <-cmdCh:
			if !ok {
				return
			}
			switch cmd.Kind {
			case CmdShutdown:
				return
			case CmdPress:
				if active != nil && active.released() {
					pendingPress = cmd.Turn
					continue
				}
				startTurn(cmd.Turn)
			case CmdRelease:
				if pendingPress >= 0 {
					// Released before it could start: report the turn as
					// over so the UI does not wait on it.
					emit(Event{Kind: EventStopped, Turn: pendingPress})
					pendingPress = -1
					continue
				}
				if active != nil {
					active.markReleased()
				}
			}
		}
	}
}

// runCaptureSession streams one dictation turn. Closing release stops the
// microphone; once its stream drains the bridge closes the audio channel,
// which makes writeLoop send `audio.done`, and the session lingers up to
// drainTimeout for the server's final transcript before returning.
func runCaptureSession(ctx context.Context, cfg Config, bearerFn BearerFunc, turnID int, release <-chan struct{}, eventCh chan<- Event, d deps) error {
	capture, mic, err := d.capture(cfg.SampleRate, cfg.InputDevice)
	if err != nil {
		return err
	}
	defer capture.Stop()

	audioReady := make(chan chan<- []byte, 1)
	go bridgePCM(ctx, mic, audioReady)

	stt, err := connectWithAuth(ctx, cfg, bearerFn, d.tls)
	if err != nil {
		return err
	}
	defer stt.close()
	audioReady <- stt.audioSender()

	emit := func(ev Event) bool {
		ev.Turn = turnID
		select {
		case eventCh <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	noSpeech := time.NewTimer(noSpeechTimeout)
	defer noSpeech.Stop()
	drain := time.NewTimer(drainTimeout)
	drain.Stop()
	defer drain.Stop()
	awaitingSpeech := true
	lockedPrefix := ""

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-release:
			release = nil
			noSpeech.Stop()
			awaitingSpeech = false
			drain.Reset(drainTimeout)
			capture.Stop()
		case <-drain.C:
			return nil
		case <-noSpeech.C:
			if !awaitingSpeech {
				continue
			}
			capture.Stop()
			emit(Event{Kind: EventError, Message: "No speech was detected. Voice stopped.", Hint: MicFixHelp()})
			return nil
		case ev, ok := <-stt.eventCh:
			if !ok {
				if release != nil {
					return wsErr("connection closed while recording")
				}
				return nil
			}
			switch ev.Kind {
			case sttPartial:
				text := strings.TrimSpace(ev.Text)
				if text == "" {
					continue
				}
				awaitingSpeech = false
				noSpeech.Stop()
				var out Event
				if ev.SpeechFinal {
					lockedPrefix = ""
					out = Event{Kind: EventFinal, Text: ev.Text}
				} else if ev.IsFinal {
					if lockedPrefix != "" {
						lockedPrefix += " "
					}
					lockedPrefix += text
					out = Event{Kind: EventInterim, Text: lockedPrefix}
				} else if lockedPrefix == "" {
					out = Event{Kind: EventInterim, Text: text}
				} else {
					out = Event{Kind: EventInterim, Text: lockedPrefix + " " + text}
				}
				if !emit(out) {
					return nil
				}
			case sttDone:
				lockedPrefix = ""
				if strings.TrimSpace(ev.Text) != "" {
					awaitingSpeech = false
					if !emit(Event{Kind: EventFinal, Text: ev.Text}) {
						return nil
					}
				}
				if release == nil {
					return nil
				}
			case sttError:
				emit(Event{Kind: EventError, Message: ev.Message})
				return nil
			case sttReady:
				return nil
			}
		}
	}
}

// connectWithAuth resolves a bearer and opens the STT socket. A 401/403
// on the handshake means the token went stale between the pre-check and
// the dial (or was revoked), so the bearer is force-refreshed and the
// dial retried once.
func connectWithAuth(ctx context.Context, cfg Config, bearerFn BearerFunc, tlsCfg *tls.Config) (*streamingSession, error) {
	var lastErr error
	for attempt, force := range []bool{false, true} {
		bearer, err := bearerFn(ctx, force)
		if err != nil {
			if ve, ok := lastErr.(*Error); ok {
				ve.Msg += "; token refresh failed: " + err.Error()
				return nil, ve
			}
			return nil, err
		}
		if strings.TrimSpace(bearer) == "" {
			return nil, authErr(notSignedInMsg)
		}
		stt, err := connectSTT(ctx, cfg, bearer, tlsCfg)
		if err == nil {
			return stt, nil
		}
		lastErr = err
		if attempt > 0 || !isAuthRejection(err) || ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// bridgePCM forwards microphone PCM to the STT audio channel. Audio that
// arrives before the socket is up is held in a bounded backlog and
// flushed once the sender is handed over. The bridge is the sole closer
// of the audio channel: it closes it when the microphone stream ends
// (the capture was stopped) after delivering everything it received, and
// that close is what makes writeLoop send `audio.done`. Sends can never
// block indefinitely because writeLoop always drains the channel; only
// ctx cancellation abandons audio.
func bridgePCM(ctx context.Context, mic <-chan []byte, audioReady <-chan chan<- []byte) {
	var tx chan<- []byte
	defer func() {
		if tx != nil {
			close(tx)
		}
	}()
	send := func(chunk []byte) bool {
		select {
		case tx <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	var backlog [][]byte
	flush := func() bool {
		for _, chunk := range backlog {
			if !send(chunk) {
				return false
			}
		}
		backlog = nil
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-audioReady:
			audioReady = nil
			if !ok {
				continue
			}
			tx = t
			if !flush() {
				return
			}
		case chunk, ok := <-mic:
			if !ok {
				if tx == nil && audioReady != nil {
					// The sender may be queued but not yet received.
					select {
					case t, ok := <-audioReady:
						if ok {
							tx = t
						}
					default:
					}
				}
				if tx != nil {
					flush()
				}
				return
			}
			if tx == nil {
				if len(backlog) == backlogMaxChunks {
					backlog = backlog[1:]
				}
				backlog = append(backlog, chunk)
				continue
			}
			if !send(chunk) {
				return
			}
		}
	}
}
