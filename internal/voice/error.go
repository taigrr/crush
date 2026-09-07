package voice

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is a typed failure from config, auth, capture, or STT.
type Error struct {
	Kind ErrorKind
	Msg  string
	// HTTPStatus is the rejected handshake's status code, or 0 when the
	// failure happened before an HTTP response was received.
	HTTPStatus int
}

// ErrorKind classifies a [Error].
type ErrorKind int

const (
	ErrConfig ErrorKind = iota
	ErrAuth
	ErrSTT
	ErrWebSocket
	ErrCapture
)

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	prefix := "voice"
	switch e.Kind {
	case ErrConfig:
		prefix = "configuration"
	case ErrAuth:
		prefix = "auth"
	case ErrSTT:
		prefix = "STT"
	case ErrWebSocket:
		prefix = "WebSocket"
	case ErrCapture:
		prefix = "capture"
	}
	return fmt.Sprintf("%s: %s", prefix, e.Msg)
}

// isAuthRejection reports whether err is a handshake refused with 401 or
// 403, i.e. the bearer was stale or invalid.
func isAuthRejection(err error) bool {
	var ve *Error
	if !errors.As(err, &ve) {
		return false
	}
	return ve.HTTPStatus == http.StatusUnauthorized || ve.HTTPStatus == http.StatusForbidden
}

func configErr(msg string) *Error  { return &Error{Kind: ErrConfig, Msg: msg} }
func authErr(msg string) *Error    { return &Error{Kind: ErrAuth, Msg: msg} }
func sttErr(msg string) *Error     { return &Error{Kind: ErrSTT, Msg: msg} }
func wsErr(msg string) *Error      { return &Error{Kind: ErrWebSocket, Msg: msg} }
func captureErr(msg string) *Error { return &Error{Kind: ErrCapture, Msg: msg} }
