package tools

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestHasChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit *protocol.WorkspaceEdit
		want bool
	}{
		{name: "nil edit", edit: nil, want: false},
		{name: "empty edit", edit: &protocol.WorkspaceEdit{}, want: false},
		{
			name: "Changes populated",
			edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentURI][]protocol.TextEdit{
					protocol.URIFromPath("/tmp/x.go"): {{NewText: "x"}},
				},
			},
			want: true,
		},
		{
			name: "DocumentChanges populated",
			edit: &protocol.WorkspaceEdit{
				DocumentChanges: []protocol.DocumentChange{{
					TextDocumentEdit: &protocol.TextDocumentEdit{},
				}},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, hasChanges(tt.edit))
		})
	}
}

func TestAffectedPaths_DedupesAndSorts(t *testing.T) {
	t.Parallel()

	edit := &protocol.WorkspaceEdit{
		Changes: map[protocol.DocumentURI][]protocol.TextEdit{
			protocol.URIFromPath("/tmp/b.go"): {},
			protocol.URIFromPath("/tmp/a.go"): {},
		},
		DocumentChanges: []protocol.DocumentChange{
			{TextDocumentEdit: &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{
						URI: protocol.URIFromPath("/tmp/a.go"), // duplicate of Changes
					},
				},
			}},
			{TextDocumentEdit: &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{
						URI: protocol.URIFromPath("/tmp/c.go"),
					},
				},
			}},
		},
	}

	got := affectedPaths(edit)
	require.Equal(t, []string{"/tmp/a.go", "/tmp/b.go", "/tmp/c.go"}, got)
}

func TestFormatRenameResult(t *testing.T) {
	t.Parallel()
	got := formatRenameResult("Foo", "Bar", []string{"/tmp/a.go", "/tmp/b.go"}, nil)
	require.True(t, strings.Contains(got, "Renamed 'Foo' -> 'Bar' in 2 file(s):"), "got: %s", got)
	require.Contains(t, got, "/tmp/a.go")
	require.Contains(t, got, "/tmp/b.go")
	require.NotContains(t, got, "WARNING")

	got = formatRenameResult("Foo", "Bar", []string{"/tmp/a.go"}, []string{"/tmp/c.go"})
	require.Contains(t, got, "WARNING: 1 other file(s) still contain 'Foo'")
	require.Contains(t, got, "/tmp/c.go")
}

func TestMissedFiles(t *testing.T) {
	t.Parallel()
	got := missedFiles([]string{"/a", "/b", "/c"}, []string{"/b"})
	require.Equal(t, []string{"/a", "/c"}, got)
	require.Nil(t, missedFiles([]string{"/a"}, []string{"/a"}))
}

func TestSymbolPattern(t *testing.T) {
	t.Parallel()
	require.Equal(t, `\bfolderName\b`, symbolPattern("folderName"))
	require.Equal(t, `\bBar\b`, symbolPattern("foo.Bar"))
	require.Equal(t, `\bbaz\b`, symbolPattern("ns::baz"))
}
