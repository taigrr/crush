package chat

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// TestUserMessageItemRawRenderPreservesLiteralCharacters guards the
// switch away from markdown rendering for user messages: characters
// that markdown would otherwise treat as syntax (angle brackets,
// asterisks) must survive verbatim in the rendered output.
func TestUserMessageItemRawRenderPreservesLiteralCharacters(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	msg := &message.Message{
		ID:   "m1",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "<b>not html</b> and *not bold*"},
		},
	}

	item := NewUserMessageItem(&sty, msg, nil).(*UserMessageItem)
	rendered := item.RawRender(80)
	plain := ansi.Strip(rendered)

	require.Contains(t, plain, "<b>not html</b>")
	require.Contains(t, plain, "*not bold*")
}

// TestUserMessageItemRawRenderStripsControlSequences guards against
// terminal-injection via raw ANSI/control bytes in user-authored text:
// switching from glamour (which reserializes content) to ansi.Wrap
// (which passes escape sequences through untouched) means RawRender
// itself must sanitize the input before wrapping it.
func TestUserMessageItemRawRenderStripsControlSequences(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	msg := &message.Message{
		ID:   "m2",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "before\x1b[31mred\x1b[0mafter"},
		},
	}

	item := NewUserMessageItem(&sty, msg, nil).(*UserMessageItem)
	rendered := item.RawRender(80)

	require.NotContains(t, rendered, "\x1b[31m")
	require.Contains(t, ansi.Strip(rendered), "beforeredafter")
}

// TestUserMessageItemRawRenderNormalizesCarriageReturns guards against
// a bare \r (or \r\n) relocating the terminal cursor and clobbering
// the message border prefix: RawRender must normalize it to \n before
// the content ever reaches the terminal.
func TestUserMessageItemRawRenderNormalizesCarriageReturns(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	msg := &message.Message{
		ID:   "m4",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "line1\rline2\r\nline3"},
		},
	}

	item := NewUserMessageItem(&sty, msg, nil).(*UserMessageItem)
	rendered := item.RawRender(80)

	require.NotContains(t, rendered, "\r")
	plain := ansi.Strip(rendered)
	require.Contains(t, plain, "line1")
	require.Contains(t, plain, "line2")
	require.Contains(t, plain, "line3")
}

// TestUserMessageItemRawRenderStripsBareControlBytes guards against
// standalone C0 control bytes (bell, backspace, etc.) that are not
// part of a full ANSI escape sequence and so survive ansi.Strip
// unchanged; RawRender must filter these out itself since a
// backspace can walk the cursor back over the border prefix just
// like a bare \r.
func TestUserMessageItemRawRenderStripsBareControlBytes(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	msg := &message.Message{
		ID:   "m5",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "before\x07\x08after"},
		},
	}

	item := NewUserMessageItem(&sty, msg, nil).(*UserMessageItem)
	rendered := item.RawRender(80)

	require.NotContains(t, rendered, "\x07")
	require.NotContains(t, rendered, "\x08")
	require.Contains(t, ansi.Strip(rendered), "beforeafter")
}

// TestUserMessageItemRawRenderAppliesBaseForeground guards the fix
// restoring the themed base foreground that glamour used to apply
// implicitly. ansi.Wrap alone emits no styling, so RawRender must
// re-apply it per line.
func TestUserMessageItemRawRenderAppliesBaseForeground(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	msg := &message.Message{
		ID:   "m3",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
		},
	}

	item := NewUserMessageItem(&sty, msg, nil).(*UserMessageItem)
	rendered := item.RawRender(80)

	require.NotEqual(t, "hello", rendered, "expected styling to be applied")
	require.Contains(t, ansi.Strip(rendered), "hello")
}
