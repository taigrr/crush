package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
)

func textMsg(role fantasy.MessageRole, text string) fantasy.Message {
	return fantasy.Message{Role: role, Content: []fantasy.MessagePart{fantasy.TextPart{Text: text}}}
}

func msgText(m fantasy.Message) string {
	for _, p := range m.Content {
		if tp, ok := fantasy.AsMessagePart[fantasy.TextPart](p); ok {
			return tp.Text
		}
	}
	return ""
}

func TestInsertFoldedAsides(t *testing.T) {
	t.Parallel()
	base := []fantasy.Message{
		textMsg(fantasy.MessageRoleUser, "u0"),
		textMsg(fantasy.MessageRoleAssistant, "a1"),
		textMsg(fantasy.MessageRoleTool, "t1"),
		textMsg(fantasy.MessageRoleAssistant, "a2"),
		textMsg(fantasy.MessageRoleTool, "t2"),
	}

	t.Run("none", func(t *testing.T) {
		t.Parallel()
		out := insertFoldedAsides(base, nil)
		require.Equal(t, base, out)
		out[0] = textMsg(fantasy.MessageRoleUser, "changed")
		require.Equal(t, "u0", msgText(base[0]), "base must not be aliased")
	})

	t.Run("interleaved at recorded offsets", func(t *testing.T) {
		t.Parallel()
		asides := []foldedAside{
			{at: 3, messages: []fantasy.Message{textMsg(fantasy.MessageRoleUser, "s1")}},
			{at: 5, messages: []fantasy.Message{textMsg(fantasy.MessageRoleUser, "s2"), textMsg(fantasy.MessageRoleUser, "s3")}},
		}
		out := insertFoldedAsides(base, asides)
		var got []string
		for _, m := range out {
			got = append(got, msgText(m))
		}
		require.Equal(t, []string{"u0", "a1", "t1", "s1", "a2", "t2", "s2", "s3"}, got)
	})

	t.Run("offset beyond base is clamped", func(t *testing.T) {
		t.Parallel()
		out := insertFoldedAsides(base[:2], []foldedAside{{at: 99, messages: []fantasy.Message{textMsg(fantasy.MessageRoleUser, "s")}}})
		require.Len(t, out, 3)
		require.Equal(t, "s", msgText(out[2]))
	})

	t.Run("same offset keeps insertion order", func(t *testing.T) {
		t.Parallel()
		asides := []foldedAside{
			{at: 1, messages: []fantasy.Message{textMsg(fantasy.MessageRoleUser, "x")}},
			{at: 1, messages: []fantasy.Message{textMsg(fantasy.MessageRoleUser, "y")}},
		}
		out := insertFoldedAsides(base, asides)
		require.Equal(t, "u0", msgText(out[0]))
		require.Equal(t, "x", msgText(out[1]))
		require.Equal(t, "y", msgText(out[2]))
		require.Equal(t, "a1", msgText(out[3]))
	})
}

func TestWrapSteer(t *testing.T) {
	t.Parallel()
	in := []fantasy.Message{
		{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: "  [btw] stop  "},
			fantasy.FilePart{Filename: "a.png", MediaType: "image/png"},
		}},
		textMsg(fantasy.MessageRoleAssistant, "keep"),
	}
	out := wrapSteer(in)
	require.Len(t, out, 2)
	require.Equal(t, steerPreamble+"[btw] stop", msgText(out[0]))
	_, isFile := fantasy.AsMessagePart[fantasy.FilePart](out[0].Content[1])
	require.True(t, isFile, "non-text parts pass through")
	require.Equal(t, "keep", msgText(out[1]))
	require.Equal(t, "  [btw] stop  ", msgText(in[0]), "input must not be mutated")
}
