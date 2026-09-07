package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	sttConnectTimeout = 15 * time.Second
	sttReadyTimeout   = 10 * time.Second
	audioDoneJSON     = `{"type":"audio.done"}`
)

// sttServerEvent is a parsed server-to-client STT WebSocket event.
type sttServerEvent struct {
	Type        string  `json:"type"`
	Text        string  `json:"text"`
	IsFinal     bool    `json:"is_final"`
	SpeechFinal bool    `json:"speech_final"`
	Message     string  `json:"message"`
	Duration    float32 `json:"duration"`
}

const (
	sttTypeCreated = "transcript.created"
	sttTypePartial = "transcript.partial"
	sttTypeDone    = "transcript.done"
	sttTypeError   = "error"
)

type sttEventKind int

const (
	sttReady sttEventKind = iota
	sttPartial
	sttDone
	sttError
)

type sttEvent struct {
	Kind        sttEventKind
	Text        string
	IsFinal     bool
	SpeechFinal bool
	Message     string
}

// streamingSession is a live `wss://…/v1/stt` connection.
type streamingSession struct {
	audioCh chan []byte
	eventCh chan sttEvent
	done    chan struct{}
	once    sync.Once
}

func connectSTT(ctx context.Context, cfg Config, bearer string) (*streamingSession, error) {
	wsURL, err := buildSTTWSURL(cfg)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+bearer)
	insertOptionalHeader(header, "x-grok-client-identifier", cfg.ClientIdentifier)
	insertOptionalHeader(header, "User-Agent", cfg.UserAgent)

	dialer := websocket.Dialer{
		HandshakeTimeout: sttConnectTimeout,
	}
	dialCtx, cancel := context.WithTimeout(ctx, sttConnectTimeout)
	defer cancel()
	conn, resp, err := dialer.DialContext(dialCtx, wsURL, header)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, wsErr(fmt.Sprintf("connect: %v", err))
	}

	s := &streamingSession{
		audioCh: make(chan []byte, 64),
		eventCh: make(chan sttEvent, 64),
		done:    make(chan struct{}),
	}
	go s.writeLoop(conn)
	go s.readLoop(conn)
	if err := s.waitReady(); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *streamingSession) waitReady() error {
	select {
	case ev, ok := <-s.eventCh:
		if !ok {
			return sttErr("connection closed before ready")
		}
		switch ev.Kind {
		case sttReady:
			return nil
		case sttError:
			return sttErr(ev.Message)
		default:
			return sttErr("unexpected event before ready")
		}
	case <-time.After(sttReadyTimeout):
		return sttErr("timed out waiting for transcript.created")
	}
}

func (s *streamingSession) recv(ctx context.Context) (sttEvent, bool) {
	select {
	case ev, ok := <-s.eventCh:
		return ev, ok
	case <-ctx.Done():
		return sttEvent{}, false
	}
}

func (s *streamingSession) audioSender() chan<- []byte {
	return s.audioCh
}

func (s *streamingSession) finishAudio() {
	s.once.Do(func() {
		close(s.audioCh)
	})
}

func (s *streamingSession) close() {
	s.finishAudio()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

func (s *streamingSession) writeLoop(conn *websocket.Conn) {
	defer conn.Close()
	for {
		select {
		case <-s.done:
			return
		case chunk, ok := <-s.audioCh:
			if !ok {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(audioDoneJSON))
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				return
			}
		}
	}
}

func (s *streamingSession) readLoop(conn *websocket.Conn) {
	defer close(s.eventCh)
	for {
		select {
		case <-s.done:
			return
		default:
		}
		typ, data, err := conn.ReadMessage()
		if err != nil {
			if !isBenignDisconnect(err) {
				select {
				case s.eventCh <- sttEvent{Kind: sttError, Message: "connection lost: " + err.Error()}:
				case <-s.done:
				}
			}
			return
		}
		if typ != websocket.TextMessage {
			continue
		}
		ev, ok := parseSTTEvent(data)
		if !ok {
			continue
		}
		select {
		case s.eventCh <- ev:
		case <-s.done:
			return
		}
	}
}

func parseSTTEvent(data []byte) (sttEvent, bool) {
	var raw sttServerEvent
	if err := json.Unmarshal(data, &raw); err != nil {
		return sttEvent{Kind: sttError, Message: "parse error: " + err.Error()}, true
	}
	switch raw.Type {
	case sttTypeCreated:
		return sttEvent{Kind: sttReady}, true
	case sttTypePartial:
		return sttEvent{
			Kind:        sttPartial,
			Text:        raw.Text,
			IsFinal:     raw.IsFinal,
			SpeechFinal: raw.SpeechFinal,
		}, true
	case sttTypeDone:
		return sttEvent{Kind: sttDone, Text: raw.Text}, true
	case sttTypeError:
		return sttEvent{Kind: sttError, Message: raw.Message}, true
	default:
		return sttEvent{}, false
	}
}

func isBenignDisconnect(err error) bool {
	if err == nil {
		return true
	}
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
		return true
	}
	if websocket.IsUnexpectedCloseError(err) {
		msg := err.Error()
		return strings.Contains(msg, "reset") || strings.Contains(msg, "EOF")
	}
	return err == io.EOF || strings.Contains(err.Error(), "use of closed network connection")
}

func insertOptionalHeader(h http.Header, name, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if strings.ContainsAny(value, "\r\n") {
		return
	}
	h.Set(name, value)
}

func buildSTTWSURL(cfg Config) (string, error) {
	base, err := cfg.STTWSURL()
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", sttErr(fmt.Sprintf("bad STT URL: %v", err))
	}
	q := u.Query()
	q.Set("sample_rate", strconv.FormatUint(uint64(cfg.SampleRate), 10))
	q.Set("encoding", "pcm")
	q.Set("interim_results", strconv.FormatBool(cfg.InterimResults))
	q.Set("language", LanguageForAPI(cfg.Language))
	q.Set("endpointing", strconv.FormatUint(uint64(cfg.EndpointingMS), 10))
	u.RawQuery = q.Encode()
	return u.String(), nil
}
