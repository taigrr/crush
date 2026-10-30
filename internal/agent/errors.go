package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
)

var (
	ErrRequestCancelled = errors.New("request canceled by user")
	ErrSessionBusy      = errors.New("session is currently processing another request")
	ErrEmptyPrompt      = errors.New("prompt is empty")
	ErrSessionMissing   = errors.New("session id is missing")
)

// emptyResponseError reports whether a finished step produced no visible
// output and returns a user-facing explanation. Anthropic's streaming
// safety classifiers end the turn with stop_reason "refusal" (mapped to
// FinishReasonContentFilter) and a blank reasoning block; without this the
// assistant appears to silently reply with nothing. This typically happens
// when secrets such as API keys or credentials are present in the
// conversation context.
func emptyResponseError(reason fantasy.FinishReason, msg message.Message) (title, details string, ok bool) {
	if reason == fantasy.FinishReasonContentFilter {
		return "Response blocked",
			"The provider's safety filter refused to continue this conversation. " +
				"This usually happens when secrets (API keys, credentials, tokens) are present in the context. " +
				"Start a new session or remove the sensitive content and try again.",
			true
	}
	switch reason {
	case fantasy.FinishReasonUnknown, fantasy.FinishReasonOther, fantasy.FinishReasonError:
	default:
		return "", "", false
	}
	if strings.TrimSpace(msg.Content().Text) != "" || len(msg.ToolCalls()) > 0 {
		return "", "", false
	}
	return "Empty response",
		fmt.Sprintf("The model returned no content (finish reason: %q). Try sending your message again or starting a new session.", reason),
		true
}

// maxProviderErrorBody caps how much of a provider's raw error body is
// surfaced to the user.
const maxProviderErrorBody = 2000

// providerErrorDetails builds a user-facing description of a provider
// failure: the HTTP status and the provider's error message, falling back
// to the raw response body when the provider supplied no parsed message.
// Response headers and the request are deliberately left out because they
// can carry credentials.
func providerErrorDetails(err *fantasy.ProviderError) string {
	msg := strings.TrimSpace(err.Message)
	if msg == "" {
		msg = providerErrorBody(err.ResponseBody)
	}
	if err.StatusCode == 0 {
		return msg
	}
	status := fmt.Sprintf("HTTP %d", err.StatusCode)
	if msg == "" {
		return status
	}
	return status + ": " + msg
}

// providerErrorBody extracts the body from an HTTP response dump, dropping
// the status line and headers, and truncates it.
func providerErrorBody(dump []byte) string {
	body := string(dump)
	if strings.HasPrefix(body, "HTTP/") {
		_, after, found := strings.Cut(body, "\r\n\r\n")
		if !found {
			_, after, found = strings.Cut(body, "\n\n")
		}
		if !found {
			return ""
		}
		body = after
	}
	body = strings.TrimSpace(body)
	if len(body) > maxProviderErrorBody {
		body = strings.ToValidUTF8(body[:maxProviderErrorBody], "") + "..."
	}
	return body
}

// describeRunError renders a run failure for headless consumers, keeping
// the provider's status code and error message instead of only the status
// text.
func describeRunError(err error) string {
	if providerErr, ok := errors.AsType[*fantasy.ProviderError](err); ok {
		details := providerErrorDetails(providerErr)
		title := strings.TrimSpace(providerErr.Title)
		switch {
		case title == "":
			return details
		case details == "":
			return title
		default:
			return title + " (" + details + ")"
		}
	}
	return err.Error()
}
