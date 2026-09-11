package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBashTool_DescriptionIsOptional(t *testing.T) {
	tool := newBashToolForTest(t.TempDir())
	require.NotContains(t, tool.Info().Required, "description",
		"description must not be a required schema field: models drop it under load and fantasy rejects the whole call")
	require.Contains(t, tool.Info().Required, "command")
}

func TestBashTool_MissingDescriptionDefaultsFromCommand(t *testing.T) {
	tool := newBashToolForTest(t.TempDir())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{Command: "echo hi\necho there"})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "echo hi", meta.Description)
}

func TestDefaultBashDescription(t *testing.T) {
	cases := map[string]struct {
		in, want string
	}{
		"single line":   {"ls -la", "ls -la"},
		"skips blanks":  {"\n\n  go test ./...  \n", "go test ./..."},
		"first of many": {"cd /x && \\\n  make", "cd /x && \\"},
		"truncates":     {"0123456789012345678901234567890123456789012345678901234567890123456789", "012345678901234567890123456789012345678901234567890123456789…"},
		"empty":         {"   ", "shell command"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, DefaultBashDescription(tc.in))
		})
	}
}
