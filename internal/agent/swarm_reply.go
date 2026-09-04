package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/swarm"
)

// swarmReplyMaxNudges bounds how many continuation turns a session
// gets to satisfy a require_reply obligation on its own before the
// coordinator replies on its behalf. Two is enough for a model that
// merely forgot; more than that is a model that is stuck, and the
// waiting parent is better served by a forwarded final message than
// by an endless nudge loop.
const swarmReplyMaxNudges = 2

// swarmReplyFallbackTimeout bounds the detached send used when the
// coordinator replies on the agent's behalf.
const swarmReplyFallbackTimeout = 30 * time.Second

// registerReplyObligations records, for every incoming swarm part that
// set require_reply, that sessionID owes its sender a reply.
func (c *coordinator) registerReplyObligations(sessionID string, parts []message.SwarmMessage) {
	for _, p := range parts {
		if !p.RequireReply || p.SenderSessionID == "" {
			continue
		}
		ident := swarm.Identity{Color: p.SenderColor, Animal: p.SenderAnimal}
		c.swarmReplies.Require(sessionID, swarm.ReplyObligation{
			SenderSessionID: p.SenderSessionID,
			SenderAddress:   swarm.FormatAddress(ident, p.SenderSessionID),
			Body:            p.Body,
		})
	}
}

// advanceReplyObligations runs after a turn ends normally. If the
// session still owes replies, it either returns a continuation prompt
// nudging the agent to send them (ok=true), or — once the nudge budget
// is spent — forwards the agent's final message to each waiting sender
// itself and lets the turn end (ok=false).
func (c *coordinator) advanceReplyObligations(ctx context.Context, sessionID string, result *fantasy.AgentResult) (prompt string, ok bool) {
	if len(c.swarmReplies.Pending(sessionID)) == 0 {
		return "", false
	}
	due, exhausted := c.swarmReplies.Nudge(sessionID, swarmReplyMaxNudges)
	if len(exhausted) > 0 {
		body := strings.TrimSpace(c.finalAssistantText(ctx, sessionID, result))
		if body == "" {
			body = "(the session ended its turn without producing a final message)"
		}
		text := "[auto-forwarded: this session finished its turn without replying to you; here is its final message]\n\n" + body
		for _, ob := range exhausted {
			c.replyOnBehalf(sessionID, ob, text)
		}
	}
	if len(due) == 0 {
		return "", false
	}
	slog.Info("Swarm reply still owed; nudging agent", "session_id", sessionID, "pending", len(due), "nudges", due[0].Nudges)
	return swarmReplyContinuationPrompt(due), true
}

// failReplyObligations clears every reply sessionID owes after its turn
// ended abnormally and tells each waiting sender why. A canceled turn
// (user hit esc, workspace shutting down) is reported as such; any
// other error is forwarded verbatim.
func (c *coordinator) failReplyObligations(sessionID string, err error) {
	obs := c.swarmReplies.Clear(sessionID)
	if len(obs) == 0 {
		return
	}
	reason := "the turn was canceled"
	if err != nil && !errors.Is(err, context.Canceled) {
		reason = "the turn failed: " + err.Error()
	}
	text := "[auto-forwarded: this session could not complete the work you asked for; " + reason + "]"
	for _, ob := range obs {
		c.replyOnBehalf(sessionID, ob, text)
	}
}

// swarmReplyContinuationPrompt builds the directive injected as the next
// turn when a session tries to end its turn without replying.
func swarmReplyContinuationPrompt(due []swarm.ReplyObligation) string {
	var b strings.Builder
	b.WriteString("[reply required] You have not yet replied to a session that is waiting on you. ")
	b.WriteString("Before your turn can end, call the swarm tool with a concise summary of your outcome (what you did, results, anything they need to act on):\n")
	for _, ob := range due {
		fmt.Fprintf(&b, "- address=%q", ob.SenderAddress)
		if body := strings.TrimSpace(ob.Body); body != "" {
			fmt.Fprintf(&b, " — they asked: %s", firstLineOf(body, 200))
		}
		b.WriteString("\n")
	}
	b.WriteString("If the work is already done, just send the reply now; do not redo the work.")
	return b.String()
}

// replyOnBehalf delivers text from sessionID to the obligation's sender
// as a swarm message. Callers prefix text so the recipient knows the
// worker did not send it deliberately. Runs on a detached context
// because the turn's own context may already be winding down.
func (c *coordinator) replyOnBehalf(sessionID string, ob swarm.ReplyObligation, text string) {
	c.swarmMu.Lock()
	be, wsID, cfgFn := c.swarmBackend, c.swarmWorkspaceID, c.swarmConfig
	c.swarmMu.Unlock()
	if be == nil {
		slog.Warn("Swarm reply owed but no backend wired; dropping", "session_id", sessionID, "to", ob.SenderSessionID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), swarmReplyFallbackTimeout)
	defer cancel()
	if err := tools.SwarmReplyOnBehalf(ctx, be, c.sessions, cfgFn, wsID, sessionID, ob.SenderSessionID, text); err != nil {
		slog.Warn("Failed to forward swarm reply on agent's behalf", "session_id", sessionID, "to", ob.SenderSessionID, "error", err)
		return
	}
	slog.Info("Forwarded swarm reply on agent's behalf", "session_id", sessionID, "to", ob.SenderAddress)
}

// finalAssistantText returns the text of the turn's final assistant
// response, falling back to the most recent assistant message in the
// transcript when the result carries none (e.g. a tool-only last step).
func (c *coordinator) finalAssistantText(ctx context.Context, sessionID string, result *fantasy.AgentResult) string {
	if result != nil {
		if text := strings.TrimSpace(result.Response.Content.Text()); text != "" {
			return text
		}
	}
	if c.messages == nil {
		return ""
	}
	msgs, err := c.messages.List(ctx, sessionID)
	if err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != message.Assistant {
			continue
		}
		if text := strings.TrimSpace(msgs[i].Content().Text); text != "" {
			return text
		}
	}
	return ""
}

// firstLineOf returns the first non-empty line of s, truncated to
// maxRunes runes.
func firstLineOf(s string, maxRunes int) string {
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > maxRunes {
			return string(r[:maxRunes]) + "…"
		}
		return line
	}
	return ""
}
