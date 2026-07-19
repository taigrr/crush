package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
)

func TestSlugify(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Hello World":       "hello-world",
		"  Fix the Bug!!! ": "fix-the-bug",
		"multiple   spaces": "multiple-spaces",
		"":                  "",
		"---":               "",
		"CamelCase123":      "camelcase123",
		"weird/chars\\here": "weird-chars-here",
		"trailing-dash-":    "trailing-dash",
		"@#$%":              "",
	}
	for in, want := range cases {
		require.Equal(t, want, slugify(in), "slugify(%q)", in)
	}
}

func TestResolveExportPath(t *testing.T) {
	t.Parallel()
	wd := "/tmp/work"

	t.Run("empty name uses timestamped default", func(t *testing.T) {
		t.Parallel()
		got := resolveExportPath("", "My Session", wd)
		require.True(t, strings.HasPrefix(got, filepath.Join(wd, "crush-export-my-session-")))
		require.True(t, strings.HasSuffix(got, ".md"))
	})

	t.Run("empty name and empty title falls back to conversation", func(t *testing.T) {
		t.Parallel()
		got := resolveExportPath("", "", wd)
		require.True(t, strings.HasPrefix(got, filepath.Join(wd, "crush-export-conversation-")))
	})

	t.Run("relative name joined to working dir", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, filepath.Join(wd, "out.md"), resolveExportPath("out.md", "t", wd))
	})

	t.Run("relative name gets md suffix", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, filepath.Join(wd, "out.md"), resolveExportPath("out", "t", wd))
	})

	t.Run("absolute name preserved", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "/abs/path.md", resolveExportPath("/abs/path.md", "t", wd))
	})

	t.Run("tilde expands to home dir", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.Equal(t, filepath.Join(home, "notes.md"), resolveExportPath("~/notes.md", "t", wd))
	})

	t.Run("env var expands", func(t *testing.T) {
		os.Setenv("EXPORTDIR", "/tmp/exports")
		defer os.Unsetenv("EXPORTDIR")
		require.Equal(t, "/tmp/exports/out.md", resolveExportPath("$EXPORTDIR/out", "t", wd))
	})
}

func TestFormatConversation(t *testing.T) {
	t.Parallel()
	msgs := []message.Message{
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "hello there"}},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "let me think"},
				message.TextContent{Text: "hi back"},
				message.ToolCall{Name: "view", Input: `{"file_path":"x.go"}`},
			},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "   "}},
		},
	}

	out := formatConversation("My Chat", msgs)

	require.Contains(t, out, "# My Chat")
	require.Contains(t, out, "## User\n\nhello there")
	require.Contains(t, out, "## Assistant (thinking)\n\nlet me think")
	require.Contains(t, out, "## Assistant\n\nhi back")
	require.Contains(t, out, "### Tool: view")
	require.Contains(t, out, `"file_path":"x.go"`)
	// Blank user message should be skipped (only one User heading).
	require.Equal(t, 1, strings.Count(out, "## User\n"))
}

func TestFormatConversationEmptyTitle(t *testing.T) {
	t.Parallel()
	out := formatConversation("", nil)
	require.Contains(t, out, "# Conversation")
}
