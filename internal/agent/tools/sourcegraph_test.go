package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustJSONMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m))
	return m
}

func TestFormatSourcegraphResults_APIError(t *testing.T) {
	t.Parallel()
	in := mustJSONMap(t, `{"errors":[{"message":"bad query"},{"message":"second"}]}`)
	out, err := formatSourcegraphResults(in, 10)
	require.NoError(t, err)
	require.Equal(t, "## Sourcegraph API Error\n\n- bad query\n- second\n", out)
}

func TestFormatSourcegraphResults_MissingData(t *testing.T) {
	t.Parallel()
	_, err := formatSourcegraphResults(mustJSONMap(t, `{}`), 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing data field")
}

func TestFormatSourcegraphResults_MissingSearch(t *testing.T) {
	t.Parallel()
	_, err := formatSourcegraphResults(mustJSONMap(t, `{"data":{}}`), 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing search field")
}

func TestFormatSourcegraphResults_MissingResults(t *testing.T) {
	t.Parallel()
	_, err := formatSourcegraphResults(mustJSONMap(t, `{"data":{"search":{}}}`), 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing results field")
}

func TestFormatSourcegraphResults_NoResults(t *testing.T) {
	t.Parallel()
	in := mustJSONMap(t, `{"data":{"search":{"results":{"matchCount":0,"resultCount":0,"results":[]}}}}`)
	out, err := formatSourcegraphResults(in, 10)
	require.NoError(t, err)
	require.Contains(t, out, "Found 0 matches across 0 results")
	require.Contains(t, out, "No results found. Try a different query.")
}

func TestFormatSourcegraphResults_LimitHit(t *testing.T) {
	t.Parallel()
	in := mustJSONMap(t, `{"data":{"search":{"results":{"matchCount":5,"resultCount":2,"limitHit":true,"results":[]}}}}`)
	out, err := formatSourcegraphResults(in, 10)
	require.NoError(t, err)
	require.Contains(t, out, "Found 5 matches across 2 results")
	require.Contains(t, out, "(Result limit reached, try a more specific query)")
}

func TestFormatSourcegraphResults_FileMatchWithContext(t *testing.T) {
	t.Parallel()
	in := mustJSONMap(t, `{"data":{"search":{"results":{
		"matchCount":1,"resultCount":1,
		"results":[{
			"__typename":"FileMatch",
			"repository":{"name":"github.com/foo/bar"},
			"file":{"path":"a/b.go","url":"https://example.com/b.go","content":"l1\nl2\nl3\nl4\nl5"},
			"lineMatches":[{"lineNumber":3,"preview":"l3match"}]
		}]
	}}}}`)
	out, err := formatSourcegraphResults(in, 1)
	require.NoError(t, err)
	require.Contains(t, out, "## Result 1: github.com/foo/bar/a/b.go")
	require.Contains(t, out, "URL: https://example.com/b.go")
	// context window 1: line 2 before, the match line, line 4 after.
	require.Contains(t, out, "2| l2\n")
	require.Contains(t, out, "3|  l3match\n")
	require.Contains(t, out, "4| l4\n")
	// lines outside the window must not appear.
	require.NotContains(t, out, "1| l1\n")
	require.NotContains(t, out, "5| l5\n")
}

func TestFormatSourcegraphResults_FileMatchWithoutContent(t *testing.T) {
	t.Parallel()
	in := mustJSONMap(t, `{"data":{"search":{"results":{
		"matchCount":1,"resultCount":1,
		"results":[{
			"__typename":"FileMatch",
			"repository":{"name":"r"},
			"file":{"path":"p"},
			"lineMatches":[{"lineNumber":7,"preview":"hit"}]
		}]
	}}}}`)
	out, err := formatSourcegraphResults(in, 10)
	require.NoError(t, err)
	require.Contains(t, out, "7| hit\n")
}

func TestFormatSourcegraphResults_SkipsNonFileMatch(t *testing.T) {
	t.Parallel()
	in := mustJSONMap(t, `{"data":{"search":{"results":{
		"matchCount":1,"resultCount":1,
		"results":[{"__typename":"CommitSearchResult"}]
	}}}}`)
	out, err := formatSourcegraphResults(in, 10)
	require.NoError(t, err)
	require.NotContains(t, out, "## Result")
}

func TestFormatSourcegraphResults_TruncatesToTen(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	sb.WriteString(`{"data":{"search":{"results":{"matchCount":15,"resultCount":15,"results":[`)
	for i := range 15 {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"__typename":"FileMatch","repository":{"name":"r"},"file":{"path":"p"},"lineMatches":[{"lineNumber":1,"preview":"x"}]}`)
	}
	sb.WriteString(`]}}}}`)
	out, err := formatSourcegraphResults(mustJSONMap(t, sb.String()), 10)
	require.NoError(t, err)
	require.Contains(t, out, "## Result 10:")
	require.NotContains(t, out, "## Result 11:")
}
