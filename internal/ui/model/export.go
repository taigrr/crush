package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/util"
)

// reviewerLoader loads the transcript for reviewer i (0-indexed) of a
// review tool call, given the parent assistant message ID and the review
// tool call's ID. It returns nil when the reviewer's child session does
// not exist or has no messages, so callers can skip it gracefully.
type reviewerLoader func(parentMessageID, reviewToolCallID string, reviewer int) []message.Message

// exportConversation renders the active session's transcript to a Markdown
// file. When name is empty a timestamped filename is generated in the
// working directory. Relative names are resolved against the working
// directory; absolute names are written as-is. The caller (dispatchSlash)
// guarantees an active session.
func (m *UI) exportConversation(name string) tea.Cmd {
	sessionID := m.session.ID
	title := m.session.Title
	workingDir := m.com.Workspace.WorkingDir()
	return func() tea.Msg {
		msgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
		if err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: "Failed to load conversation: " + err.Error()}
		}

		loader := func(parentMessageID, reviewToolCallID string, reviewer int) []message.Message {
			childID := agent.ReviewSubToolCallID(reviewToolCallID, reviewer)
			childSessionID := m.com.Workspace.CreateAgentToolSessionID(parentMessageID, childID)
			childMsgs, err := m.com.Workspace.ListMessages(context.Background(), childSessionID)
			if err != nil {
				return nil
			}
			return childMsgs
		}

		path := resolveExportPath(name, title, workingDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: "Failed to create export directory: " + err.Error()}
		}
		if err := os.WriteFile(path, []byte(formatConversation(title, msgs, loader)), 0o644); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: "Failed to write export: " + err.Error()}
		}
		return util.InfoMsg{Type: util.InfoTypeInfo, Msg: "Exported conversation to " + path}
	}
}

// resolveExportPath determines the output path for an export. An empty name
// produces a timestamped default; a relative name is joined to workingDir.
func resolveExportPath(name, title, workingDir string) string {
	if name == "" {
		stamp := time.Now().Format("2006-01-02-150405")
		slug := slugify(title)
		if slug == "" {
			slug = "conversation"
		}
		name = fmt.Sprintf("crush-export-%s-%s.md", slug, stamp)
	} else {
		name = expandPath(name)
		if !strings.HasSuffix(name, ".md") {
			name += ".md"
		}
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(workingDir, name)
}

// expandPath expands a leading ~ (home directory) and any $VAR/${VAR}
// environment variables in a user-supplied path.
func expandPath(name string) string {
	name = os.ExpandEnv(name)
	if name == "~" || strings.HasPrefix(name, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			name = filepath.Join(home, strings.TrimPrefix(name[1:], "/"))
		}
	}
	return name
}

// slugify converts a session title into a filesystem-friendly slug.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// formatConversation renders a session transcript as Markdown. The loader
// is used to pull per-reviewer child-session transcripts for adversarial
// review tool calls; it may be nil (reviewer detail is then omitted).
func formatConversation(title string, msgs []message.Message, loader reviewerLoader) string {
	var b strings.Builder
	if title == "" {
		title = "Conversation"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "_Exported %s_\n\n", time.Now().Format(time.RFC1123))

	formatMessages(&b, msgs, loader, 2)
	return b.String()
}

// formatMessages renders a slice of messages into b. headingLevel controls
// the Markdown heading depth (2 for a top-level transcript) so nested
// reviewer transcripts render under deeper headings than their parent.
func formatMessages(b *strings.Builder, msgs []message.Message, loader reviewerLoader, headingLevel int) {
	// Collect tool results across the whole slice, keyed by tool call ID.
	// Tool results live on Tool-role messages that follow the assistant
	// message carrying the call.
	results := map[string]message.ToolResult{}
	for _, msg := range msgs {
		for _, tr := range msg.ToolResults() {
			results[tr.ToolCallID] = tr
		}
	}

	h := hashes(headingLevel)
	hSub := hashes(headingLevel + 1)

	for _, msg := range msgs {
		switch msg.Role {
		case message.User:
			text := strings.TrimSpace(msg.Content().Text)
			if text == "" {
				continue
			}
			fmt.Fprintf(b, "%s User\n\n", h)
			b.WriteString(text)
			b.WriteString("\n\n")
		case message.Assistant:
			if reasoning := strings.TrimSpace(msg.ReasoningContent().Thinking); reasoning != "" {
				fmt.Fprintf(b, "%s Assistant (thinking)\n\n", h)
				b.WriteString(reasoning)
				b.WriteString("\n\n")
			}
			if text := strings.TrimSpace(msg.Content().Text); text != "" {
				fmt.Fprintf(b, "%s Assistant\n\n", h)
				b.WriteString(text)
				b.WriteString("\n\n")
			}
			for _, tc := range msg.ToolCalls() {
				fmt.Fprintf(b, "%s Tool: %s\n\n", hSub, tc.Name)
				if input := strings.TrimSpace(tc.Input); input != "" {
					b.WriteString("```json\n")
					b.WriteString(input)
					b.WriteString("\n```\n\n")
				}
				if tc.Name == agent.ReviewToolName {
					writeReviewResult(b, msg.ID, tc.ID, results[tc.ID], loader, headingLevel)
				}
			}
		}
	}
}

// hashes returns a Markdown heading prefix of level hashes, clamped to
// the maximum heading depth of 6 so deep nesting never emits an invalid
// heading.
func hashes(level int) string {
	return strings.Repeat("#", min(level, 6))
}

// writeReviewResult renders the combined reviewer summary (the review tool
// call's result) followed by each reviewer's detailed transcript pulled
// from its child session via loader.
func writeReviewResult(b *strings.Builder, parentMessageID, reviewToolCallID string, result message.ToolResult, loader reviewerLoader, headingLevel int) {
	content := strings.TrimSpace(result.Content)
	switch {
	case result.IsError:
		// Always surface failures, even with empty content, so a review
		// call that failed never vanishes silently from the export.
		b.WriteString("**Result (error):**\n\n")
		if content == "" {
			content = "(no output)"
		}
		b.WriteString(content)
		b.WriteString("\n\n")
	case content != "":
		b.WriteString("**Result:**\n\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}

	if loader == nil {
		return
	}

	hReviewer := hashes(headingLevel + 2)
	for i := range agent.ReviewerCount {
		childMsgs := loader(parentMessageID, reviewToolCallID, i)
		if len(childMsgs) == 0 {
			continue
		}
		fmt.Fprintf(b, "%s Reviewer %d\n\n", hReviewer, i+1)
		formatMessages(b, childMsgs, nil, headingLevel+3)
	}
}
