package model

import (
	"cmp"
	"context"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/completions"
)

// closeCompletions closes the completions popup and resets state.
func (m *UI) closeCompletions() {
	m.completionsOpen = false
	m.completionsQuery = ""
	m.completionsStartIndex = 0
	m.completionsTrigger = 0
	m.completions.Close()
}

// insertCompletionText replaces the @query in the textarea with the given text.
// Returns false if the replacement cannot be performed.
func (m *UI) insertCompletionText(text string) bool {
	value := m.textarea.Value()
	if m.completionsStartIndex > len(value) {
		return false
	}

	word := m.textareaWord()
	endIdx := min(m.completionsStartIndex+len(word), len(value))
	newValue := value[:m.completionsStartIndex] + text + value[endIdx:]
	m.textarea.SetValue(newValue)
	m.textarea.MoveToEnd()
	m.textarea.InsertRune(' ')
	return true
}

// argCompletionTrigger marks the completions popup as offering slash command
// arguments (opened by a space, not by a typed sigil).
const argCompletionTrigger = 'a'

// maybeOpenArgCompletions opens argument completions when the textarea
// holds a slash command that offers them and the cursor sits right after a
// space. It is a no-op otherwise.
func (m *UI) maybeOpenArgCompletions() {
	if m.completionsOpen {
		return
	}
	value := m.textarea.Value()
	if !strings.HasPrefix(value, "/") || !strings.HasSuffix(value, " ") {
		return
	}
	vals := m.slashArgCompletions(value)
	if len(vals) == 0 {
		return
	}
	m.completionsOpen = true
	m.completionsTrigger = argCompletionTrigger
	m.completionsQuery = ""
	m.completionsStartIndex = len(value)
	m.completionsPositionStart = m.completionsPosition()
	m.completions.SetArgs(vals)
}

// insertArgCompletion replaces the partial argument with text.
func (m *UI) insertArgCompletion(text string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(text) {
		return nil
	}
	return m.handleTextareaHeightChange(prevHeight)
}

// fileCompletionMsg reports a file chosen from @-completion that was read
// for attachment. attachment is nil when the file could not be read.
type fileCompletionMsg struct {
	absPath    string
	attachment *message.Attachment
}

// handleFileCompletion records the read on the model and forwards the
// attachment, if any, to the attachments component.
func (m *UI) handleFileCompletion(msg fileCompletionMsg) tea.Cmd {
	if !slices.Contains(m.sessionFileReads, msg.absPath) {
		m.sessionFileReads = append(m.sessionFileReads, msg.absPath)
	}
	if msg.attachment == nil {
		return nil
	}
	attachment := *msg.attachment
	return func() tea.Msg { return attachment }
}

// insertFileCompletion inserts the selected file path into the textarea,
// replacing the @query, and adds the file as an attachment.
func (m *UI) insertFileCompletion(path string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(path) {
		return nil
	}
	heightCmd := m.handleTextareaHeightChange(prevHeight)

	// Snapshot the model state the command needs: it runs off the Update
	// goroutine, so it must not read or write m directly. The read is
	// recorded back on the model when fileCompletionMsg is handled.
	var sessionID string
	if m.hasSession() {
		sessionID = m.session.ID
	}
	priorReads := slices.Clone(m.sessionFileReads)
	workspace := m.com.Workspace
	fileCmd := func() tea.Msg {
		absPath, _ := filepath.Abs(path)

		if sessionID != "" {
			// Skip attachment if file was already read and hasn't been modified.
			lastRead := workspace.FileTrackerLastReadTime(context.Background(), sessionID, absPath)
			if !lastRead.IsZero() {
				if info, err := os.Stat(path); err == nil && !info.ModTime().After(lastRead) {
					return nil
				}
			}
		} else if slices.Contains(priorReads, absPath) {
			return nil
		}

		done := fileCompletionMsg{absPath: absPath}
		// Add file as attachment. If reading fails, let the LLM handle it
		// later.
		if content, err := os.ReadFile(path); err == nil {
			done.attachment = &message.Attachment{
				FilePath: path,
				FileName: filepath.Base(path),
				MimeType: mimeOf(content),
				Content:  content,
			}
		}
		return done
	}
	return tea.Batch(heightCmd, fileCmd)
}

// insertCommandCompletion inserts the selected slash command into the
// textarea, replacing the partially typed verb. A trailing space is added so
// the user can immediately type arguments.
func (m *UI) insertCommandCompletion(name string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText("/" + name) {
		return nil
	}
	return m.handleTextareaHeightChange(prevHeight)
}

// insertMCPResourceCompletion inserts the selected resource into the textarea,
// replacing the @query, and adds the resource as an attachment.
func (m *UI) insertMCPResourceCompletion(item completions.ResourceCompletionValue) tea.Cmd {
	displayText := cmp.Or(item.Title, item.URI)

	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(displayText) {
		return nil
	}
	heightCmd := m.handleTextareaHeightChange(prevHeight)

	resourceCmd := func() tea.Msg {
		contents, err := m.com.Workspace.ReadMCPResource(
			context.Background(),
			item.MCPName,
			item.URI,
		)
		if err != nil {
			slog.Warn("Failed to read MCP resource", "uri", item.URI, "error", err)
			return nil
		}
		if len(contents) == 0 {
			return nil
		}

		content := contents[0]
		var data []byte
		if content.Text != "" {
			data = []byte(content.Text)
		} else if len(content.Blob) > 0 {
			data = content.Blob
		}
		if len(data) == 0 {
			return nil
		}

		mimeType := cmp.Or(item.MIMEType, content.MIMEType, "text/plain")

		return message.Attachment{
			FilePath: item.URI,
			FileName: displayText,
			MimeType: mimeType,
			Content:  data,
		}
	}
	return tea.Batch(heightCmd, resourceCmd)
}

// completionsPosition returns the X and Y position for the completions popup.
func (m *UI) completionsPosition() image.Point {
	cur := m.textarea.Cursor()
	if cur == nil {
		return image.Point{
			X: m.layout.editor.Min.X,
			Y: m.layout.editor.Min.Y,
		}
	}
	return image.Point{
		X: cur.X + m.layout.editor.Min.X,
		Y: m.layout.editor.Min.Y + cur.Y,
	}
}

// textareaWord returns the current word at the cursor position.
func (m *UI) textareaWord() string {
	return m.textarea.Word()
}

// isWhitespace returns true if the byte is a whitespace character.
func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
