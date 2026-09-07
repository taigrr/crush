package voice

import (
	"context"
	"crypto/tls"
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
	sttWriteTimeout   = 5 * time.Second
	audioDoneJSON     = `{"type":"audio.done"}`
)

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

// audioCh is closed by its producer, never by the session; that close
// triggers `audio.done`.
type streamingSession struct {
	conn    *websocket.Conn
	audioCh chan []byte
	eventCh chan sttEvent
	done    chan struct{}
	once    sync.Once
}

func connectSTT(ctx context.Context, cfg Config, bearer string, tlsCfg *tls.Config) (*streamingSession, error) {
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
		TLSClientConfig:  tlsCfg,
	}
	dialCtx, cancel := context.WithTimeout(ctx, sttConnectTimeout)
	defer cancel()
	conn, resp, err := dialer.DialContext(dialCtx, wsURL, header)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, handshakeError(wsURL, resp, err)
	}

	s := &streamingSession{
		conn:    conn,
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

func (s *streamingSession) audioSender() chan<- []byte {
	return s.audioCh
}

func (s *streamingSession) close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
}

// After a write failure writeLoop keeps draining audioCh (discarding) so
// the producer can never block.
func (s *streamingSession) writeLoop(conn *websocket.Conn) {
	defer func() {
		for range s.audioCh {
		}
	}()
	writable := true
	for {
		select {
		case <-s.done:
			return
		case chunk, ok := <-s.audioCh:
			if !ok {
				if writable {
					_ = conn.SetWriteDeadline(time.Now().Add(sttWriteTimeout))
					_ = conn.WriteMessage(websocket.TextMessage, []byte(audioDoneJSON))
				}
				return
			}
			if !writable {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(sttWriteTimeout))
			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				writable = false
				_ = conn.Close()
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

// gorilla reports every non-101 response as the opaque "bad handshake";
// fold in the status and server body.
func handshakeError(wsURL string, resp *http.Response, err error) *Error {
	if resp == nil {
		return wsErr(fmt.Sprintf("connect: %v", err))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, handshakeBodyLimit))
	detail := serverErrorDetail(body)
	msg := fmt.Sprintf("connect to %s: HTTP %d", redactQuery(wsURL), resp.StatusCode)
	if detail != "" {
		msg += ": " + detail
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &Error{Kind: ErrAuth, Msg: msg, HTTPStatus: resp.StatusCode}
	default:
		return &Error{Kind: ErrWebSocket, Msg: msg, HTTPStatus: resp.StatusCode}
	}
}

const handshakeBodyLimit = 4096

func serverErrorDetail(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		for _, key := range []string{"error", "message", "code", "detail"} {
			switch v := obj[key].(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			case map[string]any:
				if s, ok := v["message"].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	if strings.HasPrefix(trimmed, "<") {
		return ""
	}
	line, _, _ := strings.Cut(trimmed, "\n")
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}

func redactQuery(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = ""
	return u.String()
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
