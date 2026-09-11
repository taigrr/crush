package voice

import (
	"errors"
	"fmt"
	"net/http"
)

type Error struct {
	Kind       ErrorKind
	Msg        string
	HTTPStatus int
}

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
