package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/chat"
)

func forkMsgs() []*message.Message {
	return []*message.Message{
		{ID: "aaaaaaaa-1111", Parts: []message.ContentPart{message.TextContent{Text: "first\nsecond line"}}},
		{ID: "bbbbbbbb-2222", Parts: []message.ContentPart{message.SwarmMessage{Text: "message from x-y: hi", Body: "hi"}}},
		{ID: "cccccccc-3333", Parts: []message.ContentPart{message.BinaryContent{Path: "a.png"}}},
	}
}

func TestResolveForkTarget(t *testing.T) {
	t.Parallel()
	msgs := forkMsgs()

	for _, arg := range []string{"", "last", "3", "#3", "cccc"} {
		got, err := resolveForkTarget(msgs, arg)
		require.NoError(t, err, arg)
		require.Equal(t, "cccccccc-3333", got.ID, arg)
	}
	got, err := resolveForkTarget(msgs, "#1")
	require.NoError(t, err)
	require.Equal(t, "aaaaaaaa-1111", got.ID)

	_, err = resolveForkTarget(msgs, "#4")
	require.Error(t, err)
	_, err = resolveForkTarget(msgs, "zzz")
	require.Error(t, err)
	_, err = resolveForkTarget(append(msgs, &message.Message{ID: "cccccccc-9999"}), "cccc")
	require.Error(t, err, "ambiguous prefix")
}

func TestForkPreview(t *testing.T) {
	t.Parallel()
	msgs := forkMsgs()
	require.Equal(t, "first", forkPreview(msgs[0]), "first line only")
	require.Equal(t, "⇄ hi", forkPreview(msgs[1]), "swarm body, unprefixed")
	require.Equal(t, "(1 attachment(s))", forkPreview(msgs[2]))
}

func TestForkArgCompletions_NewestFirst(t *testing.T) {
	t.Parallel()
	m := newTestUI()
	m.session = &session.Session{ID: "s1"}
	items := make([]chat.MessageItem, 0)
	for _, msg := range forkMsgs() {
		items = append(items, chat.NewUserMessageItem(m.com.Styles, msg, m.attachments.Renderer()))
	}
	m.chat.SetMessages(items...)

	out := m.forkArgCompletions("")
	require.Len(t, out, 3)
	require.Equal(t, "cccccccc", out[0].Text)
	require.Contains(t, out[0].Description, "#3 (latest)")
	require.Equal(t, "aaaaaaaa", out[2].Text)
	require.Contains(t, out[2].Description, "first")

	require.Nil(t, m.forkArgCompletions("cccc"), "only the first argument completes")
	m.previewSessionID = "other"
	require.Nil(t, m.forkArgCompletions(""))
}
