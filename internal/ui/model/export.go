package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/util"
)

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

		path := resolveExportPath(name, title, workingDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: "Failed to create export directory: " + err.Error()}
		}
		if err := os.WriteFile(path, []byte(formatConversation(title, msgs)), 0o644); err != nil {
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
	} else if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(workingDir, name)
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

// formatConversation renders a session transcript as Markdown.
func formatConversation(title string, msgs []message.Message) string {
	var b strings.Builder
	if title == "" {
		title = "Conversation"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "_Exported %s_\n\n", time.Now().Format(time.RFC1123))

	for _, msg := range msgs {
		switch msg.Role {
		case message.User:
			text := strings.TrimSpace(msg.Content().Text)
			if text == "" {
				continue
			}
			b.WriteString("## User\n\n")
			b.WriteString(text)
			b.WriteString("\n\n")
		case message.Assistant:
			wrote := false
			if reasoning := strings.TrimSpace(msg.ReasoningContent().Thinking); reasoning != "" {
				b.WriteString("## Assistant (thinking)\n\n")
				b.WriteString(reasoning)
				b.WriteString("\n\n")
				wrote = true
			}
			if text := strings.TrimSpace(msg.Content().Text); text != "" {
				b.WriteString("## Assistant\n\n")
				b.WriteString(text)
				b.WriteString("\n\n")
				wrote = true
			}
			for _, tc := range msg.ToolCalls() {
				fmt.Fprintf(&b, "### Tool: %s\n\n", tc.Name)
				if input := strings.TrimSpace(tc.Input); input != "" {
					b.WriteString("```json\n")
					b.WriteString(input)
					b.WriteString("\n```\n\n")
				}
				wrote = true
			}
			_ = wrote
		}
	}
	return b.String()
}
