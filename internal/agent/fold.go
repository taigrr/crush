package agent

import (
	"slices"
	"strings"

	"github.com/taigrr/fantasy"
)

// foldedAside is a user message folded into a running turn at a step
// boundary, remembered with the offset in the step input at which it was
// inserted so later steps can reproduce the same interleaving.
type foldedAside struct {
	at       int
	messages []fantasy.Message
}

// insertFoldedAsides returns base with every aside spliced in at its
// recorded offset. asides must be in insertion order with non-decreasing
// offsets, which is what the PrepareStep loop guarantees: each fold is
// recorded at the current step-input length and the step input only ever
// grows. base is not modified.
func insertFoldedAsides(base []fantasy.Message, asides []foldedAside) []fantasy.Message {
	if len(asides) == 0 {
		return slices.Clone(base)
	}
	total := len(base)
	for _, a := range asides {
		total += len(a.messages)
	}
	out := make([]fantasy.Message, 0, total)
	next := 0
	for _, a := range asides {
		at := min(max(a.at, next), len(base))
		out = append(out, base[next:at]...)
		out = append(out, a.messages...)
		next = at
	}
	return append(out, base[next:]...)
}

// steerPreamble frames a mid-turn steer for the model. The user's text is
// persisted verbatim; only the copy handed to the model in the running
// turn is wrapped, so the model knows this arrived while it was working
// and should be acted on now rather than treated as history.
const steerPreamble = "The user sent the following message while you were working. Read it now and adjust what you are doing accordingly before continuing; if it changes or cancels the current task, follow the new instruction instead of finishing the old one.\n\n"

// wrapSteer returns msgs with every user text part framed by the steer
// preamble. A user message with no non-blank text (attachments only)
// gets the preamble as a leading text part so the model still learns
// that what follows arrived mid-turn. Non-user messages pass through
// untouched.
func wrapSteer(msgs []fantasy.Message) []fantasy.Message {
	out := make([]fantasy.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		if msg.Role != fantasy.MessageRoleUser {
			continue
		}
		parts := make([]fantasy.MessagePart, 0, len(msg.Content)+1)
		framed := false
		for _, part := range msg.Content {
			if tp, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				text := strings.TrimSpace(tp.Text)
				if text == "" {
					continue
				}
				tp.Text = steerPreamble + text
				parts = append(parts, tp)
				framed = true
				continue
			}
			parts = append(parts, part)
		}
		if !framed {
			parts = append([]fantasy.MessagePart{fantasy.TextPart{Text: strings.TrimSpace(steerPreamble)}}, parts...)
		}
		out[i].Content = parts
	}
	return out
}
