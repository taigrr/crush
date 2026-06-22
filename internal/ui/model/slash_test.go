package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitSlash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantVerb string
		wantArgs string
		wantOK   bool
	}{
		{"/export", "export", "", true},
		{"/export foo.md", "export", "foo.md", true},
		{"/export   foo.md  ", "export", "foo.md", true},
		{"/btw hello world", "btw", "hello world", true},
		{"/continue", "continue", "", true},
		{"/goal clear", "goal", "clear", true},
		{"hello", "", "", false},
		{"", "", "", false},
		{"/", "", "", false},
		{"  not trimmed handled by caller", "", "", false},
	}
	for _, tc := range cases {
		verb, args, ok := splitSlash(tc.in)
		require.Equal(t, tc.wantOK, ok, "ok for %q", tc.in)
		require.Equal(t, tc.wantVerb, verb, "verb for %q", tc.in)
		require.Equal(t, tc.wantArgs, args, "args for %q", tc.in)
	}
}

func TestLookupSlash(t *testing.T) {
	t.Parallel()

	t.Run("matches by name", func(t *testing.T) {
		t.Parallel()
		c, ok := lookupSlash(builtinSlashCommands, "export")
		require.True(t, ok)
		require.Equal(t, "export", c.name)
	})

	t.Run("miss falls through", func(t *testing.T) {
		t.Parallel()
		_, ok := lookupSlash(builtinSlashCommands, "nope")
		require.False(t, ok)
	})

	t.Run("resolves aliases", func(t *testing.T) {
		t.Parallel()
		reg := []slashCommand{
			{name: "goal", aliases: []string{"objective", "g"}},
		}
		for _, verb := range []string{"goal", "objective", "g"} {
			c, ok := lookupSlash(reg, verb)
			require.True(t, ok, "verb %q", verb)
			require.Equal(t, "goal", c.name)
		}
		_, ok := lookupSlash(reg, "nope")
		require.False(t, ok)
	})
}

// TestBuiltinSlashCommandsWellFormed guards the registry against common
// mistakes: every command must have a name and a runner, and names/aliases
// must be unique across the registry so dispatch is unambiguous.
func TestBuiltinSlashCommandsWellFormed(t *testing.T) {
	t.Parallel()
	seen := make(map[string]string) // verb -> owning command name
	for _, c := range builtinSlashCommands {
		require.NotEmpty(t, c.name, "command with empty name")
		require.NotNil(t, c.run, "command %q has nil run", c.name)
		require.NotEmpty(t, c.description, "command %q has no description", c.name)

		verbs := append([]string{c.name}, c.aliases...)
		for _, v := range verbs {
			if owner, dup := seen[v]; dup {
				t.Fatalf("verb %q registered by both %q and %q", v, owner, c.name)
			}
			seen[v] = c.name
		}
	}
}
