package voice

import (
	"context"
	"strings"
	"time"
)

const (
	backlogMaxChunks = 1024
	noSpeechTimeout  = 10 * time.Second
	pcmChannelSize   = 64
)

// Command is a control signal from the TUI event loop.
type Command int

const (
	CmdPress Command = iota
	CmdRelease
	CmdShutdown
)

type activePTT struct {
	cancel context.CancelFunc
}

// RunPipeline consumes commands until CmdShutdown. Events are delivered
// on eventCh. bearerFn is resolved at each connect so rotating OAuth
// tokens stay valid.
func RunPipeline(ctx context.Context, cfg Config, bearerFn func() (string, error), cmdCh <-chan Command, eventCh chan<- Event) {
	cfg = cfg.Normalize()
	var active *activePTT
	stopActive := func() {
		if active != nil {
			active.cancel()
			active = nil
		}
	}
	defer stopActive()

	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-cmdCh:
			if !ok {
				return
			}
			switch cmd {
			case CmdShutdown:
				return
			case CmdPress:
				stopActive()
				sessionCtx, cancel := context.WithCancel(ctx)
				active = &activePTT{cancel: cancel}
				go func() {
					if err := runCaptureSession(sessionCtx, cfg, bearerFn, eventCh); err != nil {
						select {
						case eventCh <- Event{Kind: EventError, Message: err.Error()}:
						case <-sessionCtx.Done():
						}
					}
				}()
			case CmdRelease:
				stopActive()
			}
		}
	}
}

func runCaptureSession(ctx context.Context, cfg Config, bearerFn func() (string, error), eventCh chan<- Event) error {
	micCh := make(chan []byte, pcmChannelSize)
	capture, err := SpawnPCMCapture(cfg.SampleRate, micCh)
	if err != nil {
		return err
	}
	defer capture.Stop()

	audioReady := make(chan chan<- []byte, 1)
	go forwardPCMToSTT(ctx, micCh, audioReady)

	bearer, err := bearerFn()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bearer) == "" {
		return authErr("not signed in — run `crush login grok`, set XAI_API_KEY, or set a grok provider api_key")
	}
	stt, err := connectSTT(ctx, cfg, bearer)
	if err != nil {
		return err
	}
	defer stt.close()

	select {
	case audioReady <- stt.audioSender():
	case <-ctx.Done():
		return ctx.Err()
	}

	noSpeech := time.NewTimer(noSpeechTimeout)
	defer noSpeech.Stop()
	awaitingSpeech := true
	lockedPrefix := ""

	for {
		select {
		case <-ctx.Done():
			capture.Stop()
			stt.finishAudio()
			return nil
		case <-noSpeech.C:
			if !awaitingSpeech {
				continue
			}
			capture.Stop()
			stt.finishAudio()
			select {
			case eventCh <- Event{Kind: EventError, Message: "No speech was detected. Voice stopped.", Hint: MicFixHelp()}:
			case <-ctx.Done():
			}
			return nil
		case ev, ok := <-stt.eventCh:
			if !ok {
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
				select {
				case eventCh <- out:
				case <-ctx.Done():
					return nil
				}
			case sttDone:
				lockedPrefix = ""
				if strings.TrimSpace(ev.Text) != "" {
					awaitingSpeech = false
					select {
					case eventCh <- Event{Kind: EventFinal, Text: ev.Text}:
					case <-ctx.Done():
						return nil
					}
				}
			case sttError:
				select {
				case eventCh <- Event{Kind: EventError, Message: ev.Message}:
				case <-ctx.Done():
				}
				return nil
			case sttReady:
				return nil
			}
		}
	}
}

func forwardPCMToSTT(ctx context.Context, micCh <-chan []byte, audioReady <-chan chan<- []byte) {
	backlog := make([][]byte, 0, 16)
	var audioTx chan<- []byte
	for audioTx == nil {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-micCh:
			if !ok {
				return
			}
			if len(backlog) == backlogMaxChunks {
				backlog = backlog[1:]
			}
			backlog = append(backlog, chunk)
		case tx, ok := <-audioReady:
			if !ok {
				return
			}
			audioTx = tx
		}
	}
	for _, chunk := range backlog {
		select {
		case audioTx <- chunk:
		case <-ctx.Done():
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-micCh:
			if !ok {
				return
			}
			select {
			case audioTx <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}
}

// CombinePromptWithVoiceText joins committed prompt text and a voice
// fragment with a space, skipped when the prompt is empty or already
// ends in whitespace.
func CombinePromptWithVoiceText(existing, text string) string {
	if strings.TrimSpace(existing) == "" {
		return text
	}
	if len(existing) > 0 && isWhitespace(existing[len(existing)-1]) {
		return existing + text
	}
	return existing + " " + text
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
