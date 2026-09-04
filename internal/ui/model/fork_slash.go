package model

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/completions"
	"github.com/taigrr/crush/internal/ui/util"
)

// forkIDWidth is how many characters of a message id the /fork completion
// inserts; enough to be unique within one session and short enough to
// read.
const forkIDWidth = 8

// forkPreviewWidth caps the message preview shown beside each fork point.
const forkPreviewWidth = 70

// handleForkSlash implements /fork [<message>]: open the fork dialog at a
// user message of the open session. With no argument the fork point is
// the most recent user message, so `/fork` alone forks the whole
// conversation. <message> may be an id prefix (what the completion
// inserts), `#N` / `N` (the N-th user message, 1-based from the top), or
// `last`.
func (m *UI) handleForkSlash(args string) tea.Cmd {
	if m.previewing() {
		return util.ReportError(fmt.Errorf("/fork: commit the previewed session first (enter in the sidebar)"))
	}
	msgs := m.chat.UserMessages()
	if len(msgs) == 0 {
		return util.ReportError(fmt.Errorf("/fork: no user messages to fork from"))
	}
	target, err := resolveForkTarget(msgs, args)
	if err != nil {
		return util.ReportError(err)
	}
	return m.openForkDialog(m.session.ID, target.ID)
}

// resolveForkTarget picks the user message named by arg from msgs (oldest
// first). See handleForkSlash for the accepted forms.
func resolveForkTarget(msgs []*message.Message, arg string) (*message.Message, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" || strings.EqualFold(arg, "last") {
		return msgs[len(msgs)-1], nil
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(arg, "#")); err == nil {
		if n < 1 || n > len(msgs) {
			return nil, fmt.Errorf("/fork: message #%d does not exist (1-%d)", n, len(msgs))
		}
		return msgs[n-1], nil
	}
	var hit *message.Message
	for _, msg := range msgs {
		if strings.HasPrefix(msg.ID, arg) {
			if hit != nil {
				return nil, fmt.Errorf("/fork: %q matches more than one message", arg)
			}
			hit = msg
		}
	}
	if hit == nil {
		return nil, fmt.Errorf("/fork: no user message matches %q", arg)
	}
	return hit, nil
}

// forkArgCompletions lists the session's user messages, newest first, as
// fork points. Each inserts an id prefix and shows its ordinal and a
// preview of the text so the user can pick a point by content.
func (m *UI) forkArgCompletions(args string) []completions.ArgCompletionValue {
	if strings.TrimSpace(args) != "" || !m.hasSession() || m.previewing() {
		return nil
	}
	msgs := m.chat.UserMessages()
	out := make([]completions.ArgCompletionValue, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		label := fmt.Sprintf("#%d", i+1)
		if i == len(msgs)-1 {
			label += " (latest)"
		}
		out = append(out, completions.ArgCompletionValue{
			Text:        shortForkID(msg.ID),
			Description: label + "  " + forkPreview(msg),
		})
	}
	return out
}

func shortForkID(id string) string {
	if len(id) <= forkIDWidth {
		return id
	}
	return id[:forkIDWidth]
}

// forkPreview renders the first line of a message's text, truncated.
func forkPreview(msg *message.Message) string {
	text := msg.Content().Text
	for _, part := range msg.Parts {
		if sm, ok := part.(message.SwarmMessage); ok {
			text = "⇄ " + sm.Body
			break
		}
	}
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		if n := len(msg.BinaryContent()); n > 0 {
			return fmt.Sprintf("(%d attachment(s))", n)
		}
		return "(empty)"
	}
	r := []rune(line)
	if len(r) > forkPreviewWidth {
		return string(r[:forkPreviewWidth]) + "…"
	}
	return line
}
