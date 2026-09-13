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
