package chat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"image"
	_ "image/gif"  // GIF decoding.
	_ "image/jpeg" // JPEG decoding.
	_ "image/png"  // PNG decoding.
	"os"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/attachments"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/dialog"
	fimage "github.com/taigrr/crush/internal/ui/image"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
	_ "golang.org/x/image/webp" // WebP decoding.
)

// skillInvocation represents the XML structure for a loaded skill.
type skillInvocation struct {
	Name         string `xml:"name"`
	Description  string `xml:"description"`
	Location     string `xml:"location"`
	Instructions string `xml:"instructions"`
}

// UserMessageItem represents a user message in the chat UI.
type UserMessageItem struct {
	*list.Versioned
	*highlightableMessageItem
	*cachedMessageItem
	*focusableMessageItem

	attachments *attachments.Renderer
	message     *message.Message
	sty         *styles.Styles
	imageConfig *ImageConfig

	// imageIDs memoizes imageCacheID results so the content hash is
	// computed once per attachment, not on every render frame. Keyed by
	// the attachment's index within the message; a user message is
	// immutable once submitted, so the cache never needs invalidation.
	imageIDs map[int]string
}

// NewUserMessageItem creates a new UserMessageItem.
func NewUserMessageItem(sty *styles.Styles, message *message.Message, attachments *attachments.Renderer) MessageItem {
	v := list.NewVersioned()
	return &UserMessageItem{
		Versioned:                v,
		highlightableMessageItem: defaultHighlighter(sty, v),
		cachedMessageItem:        &cachedMessageItem{},
		focusableMessageItem:     newFocusableMessageItem(v),
		attachments:              attachments,
		message:                  message,
		sty:                      sty,
	}
}

// Message returns the underlying user message.
func (m *UserMessageItem) Message() *message.Message { return m.message }

// Finished implements list.Item. User messages are immutable once
// submitted, so the entry is always safe to freeze.
func (m *UserMessageItem) Finished() bool {
	return true
}

// RawRender implements [MessageItem].
func (m *UserMessageItem) RawRender(width int) string {
	cappedWidth := cappedMessageWidth(width)

	content, height, ok := m.getCachedRender(cappedWidth)
	// cache hit
	if ok {
		return m.renderHighlighted(content, cappedWidth, height)
	}

	msgContent := strings.TrimSpace(m.message.Content().Text)

	// Check if this is a skill invocation (loaded_skill XML)
	if strings.HasPrefix(msgContent, "<loaded_skill>") {
		content = m.renderSkillInvocation(msgContent, cappedWidth)
		height = lipgloss.Height(content)
		m.setCachedRender(content, cappedWidth, height)
		return m.renderHighlighted(content, cappedWidth, height)
	}

	// A background-job completion notice is persisted as a user message
	// so it folds into the conversation like a steer, but the user did
	// not type it: render it as a job entry, not a prompt bubble.
	if strings.HasPrefix(msgContent, tools.JobFinishedNoticePrefix) {
		content = renderJobFinishedNotice(m.sty, msgContent, cappedWidth)
		height = lipgloss.Height(content)
		m.setCachedRender(content, cappedWidth, height)
		return m.renderHighlighted(content, cappedWidth, height)
	}

	// User messages are shown verbatim: no markdown/HTML rendering so
	// that literal characters like angle brackets are preserved. Strip
	// any raw ANSI escape sequences the source text may carry (e.g.
	// pasted terminal output), normalize CRLF/CR, and drop any
	// remaining C0/C1 control bytes (BEL, backspace, etc. can relocate
	// the cursor or trigger side effects just like a bare \r) so none
	// of it can leak into the rendered UI. Then word-wrap to the
	// available width and re-apply the themed base foreground per line
	// (markdown rendering used to carry this color implicitly;
	// ansi.Wrap emits no styling at all).
	sanitized := ansi.Strip(msgContent)
	sanitized = strings.ReplaceAll(sanitized, "\r\n", "\n")
	sanitized = strings.ReplaceAll(sanitized, "\r", "\n")
	sanitized = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, sanitized)
	if cappedWidth > 0 {
		content = ansi.Wrap(sanitized, cappedWidth, "")
	} else {
		content = sanitized
	}
	content = strings.TrimSuffix(content, "\n")
	if content != "" {
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			lines[i] = m.sty.Messages.NoContent.Render(line)
		}
		content = strings.Join(lines, "\n")
	}

	if len(m.message.BinaryContent()) > 0 {
		attachmentsStr := m.renderAttachments(cappedWidth)
		if content == "" {
			content = attachmentsStr
		} else {
			content = strings.Join([]string{content, "", attachmentsStr}, "\n")
		}
	}

	height = lipgloss.Height(content)
	m.setCachedRender(content, cappedWidth, height)
	return m.renderHighlighted(content, cappedWidth, height)
}

// renderSkillInvocation renders a loaded_skill XML as a special UI element.
func (m *UserMessageItem) renderSkillInvocation(content string, width int) string {
	var skill skillInvocation
	if err := xml.Unmarshal([]byte(content), &skill); err != nil {
		// If parsing fails, just render as markdown
		renderer := common.MarkdownRenderer(m.sty, width)
		mu := common.LockMarkdownRenderer(renderer)

		mu.Lock()
		result, err := renderer.Render(content)
		mu.Unlock()

		if err != nil {
			return content
		}
		return strings.TrimSuffix(result, "\n")
	}

	return toolOutputSkillContent(m.sty, skill.Name, skill.Description)
}

// renderJobFinishedNotice renders the folded "[background job finished]"
// aside in the same visual language as the job tools: a Job header with
// the shell id and description, then the command and its captured
// output. The trailing guidance paragraph aimed at the model is dropped;
// it carries nothing the user needs.
func renderJobFinishedNotice(sty *styles.Styles, content string, width int) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	first := strings.TrimSpace(strings.TrimPrefix(lines[0], tools.JobFinishedNoticePrefix))

	var shellID, description string
	if open := strings.LastIndex(first, "(job "); open >= 0 && strings.HasSuffix(first, ")") {
		shellID = strings.TrimSpace(first[open+len("(job ") : len(first)-1])
		description = strings.TrimSpace(first[:open])
	} else {
		description = first
	}

	body := lines[1:]
	if n := len(body); n > 0 && strings.HasPrefix(body[n-1], "This is an automatic notice") {
		body = body[:n-1]
	}
	bodyText := strings.TrimSpace(strings.Join(body, "\n"))

	header := jobHeader(sty, ToolStatusSuccess, "Finished", shellID, description, width)
	if bodyText == "" {
		return header
	}
	bodyWidth := width - toolBodyLeftPaddingTotal
	rendered := sty.Tool.Body.Render(toolOutputPlainContent(sty, bodyText, bodyWidth, false))
	return joinToolParts(header, rendered)
}

// Render implements MessageItem.
func (m *UserMessageItem) Render(width int) string {
	// Bypass the prefix cache while a highlight range is active so
	// selection drags reflect immediately without invalidating the
	// cache. Highlight changes are intentionally applied "above" the
	// prefix cache.
	useCache := !m.isHighlighted()
	var key uint64
	if m.focused {
		key = 1
	}
	if useCache {
		if cached, ok := m.getCachedPrefixedRender(width, key); ok {
			return cached
		}
	}
	var prefix string
	if m.focused {
		prefix = m.sty.Messages.UserFocused.Render()
	} else {
		prefix = m.sty.Messages.UserBlurred.Render()
	}
	lines := strings.Split(m.RawRender(width), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	out := strings.Join(lines, "\n")
	if useCache {
		m.setCachedPrefixedRender(out, width, key)
	}
	return out
}

// ID implements MessageItem.
func (m *UserMessageItem) ID() string {
	return m.message.ID
}

// renderAttachments renders attachments. If inline image rendering is supported,
// images are rendered inline; otherwise they are shown as filename chips.
func (m *UserMessageItem) renderAttachments(width int) string {
	binaryContents := m.message.BinaryContent()
	if len(binaryContents) == 0 {
		return ""
	}

	var parts []string

	if m.imageConfig != nil && m.imageConfig.Encoding == fimage.EncodingKitty {
		for i, bc := range binaryContents {
			if !strings.HasPrefix(bc.MIMEType, "image/") {
				continue
			}
			cols, rows := imageRenderDims(m.imageConfig)

			id := m.imageCacheID(i, bc)
			if fimage.HasTransmitted(id, cols, rows) {
				imgRender := m.imageConfig.Encoding.Render(id, cols, rows)
				parts = append(parts, imgRender)
			}
		}
	}

	var attachmentList []message.Attachment
	for _, at := range binaryContents {
		attachmentList = append(attachmentList, message.Attachment{
			FileName: at.Path,
			MimeType: at.MIMEType,
		})
	}
	chips := m.attachments.Render(attachmentList, false, width)
	if chips != "" {
		parts = append(parts, chips)
	}

	return strings.Join(parts, "\n")
}

// SetImageConfig sets the image rendering configuration.
func (m *UserMessageItem) SetImageConfig(cfg *ImageConfig) {
	m.imageConfig = cfg
}

// TransmitImages transmits any image attachments to the terminal for inline
// rendering. Returns a tea.Cmd if images need to be transmitted.
func (m *UserMessageItem) TransmitImages() tea.Cmd {
	if m.imageConfig == nil || m.imageConfig.Encoding != fimage.EncodingKitty {
		return nil
	}

	var cmds []tea.Cmd
	for i, bc := range m.message.BinaryContent() {
		if !strings.HasPrefix(bc.MIMEType, "image/") {
			continue
		}

		cols, rows := imageRenderDims(m.imageConfig)

		id := m.imageCacheID(i, bc)
		if fimage.HasTransmitted(id, cols, rows) {
			continue
		}

		var img image.Image
		var err error
		if len(bc.Data) > 0 {
			img, _, err = image.Decode(bytes.NewReader(bc.Data))
		} else if bc.Path != "" {
			img, err = loadImageFromFile(bc.Path)
		}
		if err != nil || img == nil {
			continue
		}

		cs := fimage.CellSize{
			Width:  m.imageConfig.CellWidth,
			Height: m.imageConfig.CellHeight,
		}

		cmd := m.imageConfig.Encoding.Transmit(id, img, cs, cols, rows, m.imageConfig.Tmux)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// imageCacheID identifies an image attachment for the terminal image cache.
// Pasted images across a session can share a file name ("paste_1.png"), so
// the name alone would make a later paste render as an earlier one; mix in
// a hash of the bytes when they are available.
//
// The result is memoized per item, keyed by the attachment's index within
// the message, so the hash is computed once rather than on every render
// frame. Indexing (rather than path+length) keeps the memo collision-free
// even for legacy messages that carry two same-named, equal-length
// attachments with different content.
func (m *UserMessageItem) imageCacheID(idx int, bc message.BinaryContent) string {
	if len(bc.Data) == 0 {
		return bc.Path
	}
	if m.imageIDs != nil {
		if id, ok := m.imageIDs[idx]; ok {
			return id
		}
	}
	sum := sha256.Sum256(bc.Data)
	id := bc.Path + "#" + hex.EncodeToString(sum[:8])
	if m.imageIDs == nil {
		m.imageIDs = make(map[int]string)
	}
	m.imageIDs[idx] = id
	return id
}

// loadImageFromFile loads an image from a file path.
func loadImageFromFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	return img, err
}

// HandleKeyEvent implements KeyEventHandler.
func (m *UserMessageItem) HandleKeyEvent(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "c", "y":
		text := m.message.Content().Text
		return true, common.CopyToClipboard(text, "Message copied to clipboard")
	case "F": // shift+F to fork from this message
		return true, func() tea.Msg {
			return dialog.ActionOpenForkDialog{
				SessionID: m.message.SessionID,
				MessageID: m.message.ID,
			}
		}
	}
	return false, nil
}
