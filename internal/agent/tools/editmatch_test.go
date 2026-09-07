package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocateEditExact(t *testing.T) {
	t.Parallel()

	content := "a\nb\nc\n"
	m, err := locateEdit(content, "b\n", "B\n")
	require.NoError(t, err)
	require.False(t, m.fuzzy)
	got, region := applyMatch(content, m)
	require.Equal(t, "a\nB\nc\n", got)
	require.Equal(t, lineRange{2, 2}, region)
}

func TestLocateEditWhitespaceTolerant(t *testing.T) {
	t.Parallel()

	t.Run("over-indented old_string is de-indented", func(t *testing.T) {
		t.Parallel()
		content := "func f() {\n\tif x {\n\t\treturn 1\n\t}\n}\n"
		old := "\t\tif x {\n\t\t\treturn 1\n\t\t}\n"
		repl := "\t\tif x {\n\t\t\treturn 2\n\t\t}\n"
		m, err := locateEdit(content, old, repl)
		require.NoError(t, err)
		require.True(t, m.fuzzy)
		got, _ := applyMatch(content, m)
		require.Equal(t, "func f() {\n\tif x {\n\t\treturn 2\n\t}\n}\n", got)
		require.Contains(t, m.note, "de-indented")
	})

	t.Run("under-indented old_string is indented", func(t *testing.T) {
		t.Parallel()
		content := "    a\n    b\n"
		m, err := locateEdit(content, "a\nb\n", "a\nc\n")
		require.NoError(t, err)
		got, _ := applyMatch(content, m)
		require.Equal(t, "    a\n    c\n", got)
	})

	t.Run("spaces converted to tabs", func(t *testing.T) {
		t.Parallel()
		content := "x\n\tfoo()\n\t\tbar()\n"
		m, err := locateEdit(content, "    foo()\n        bar()\n", "    foo()\n        baz()\n")
		require.NoError(t, err)
		got, _ := applyMatch(content, m)
		require.Equal(t, "x\n\tfoo()\n\t\tbaz()\n", got)
		require.Contains(t, m.note, "tabs")
	})

	t.Run("trailing whitespace ignored", func(t *testing.T) {
		t.Parallel()
		content := "hello   \nworld\n"
		m, err := locateEdit(content, "hello\n", "hi\n")
		require.NoError(t, err)
		got, _ := applyMatch(content, m)
		require.Equal(t, "hi\nworld\n", got)
	})

	t.Run("ambiguous fuzzy match errors with occurrences", func(t *testing.T) {
		t.Parallel()
		content := "  a\n  b\n  a\n"
		_, err := locateEdit(content, "a  \n", "z\n")
		require.Error(t, err)
		require.Contains(t, err.Error(), "matches 2 regions")
		require.Contains(t, err.Error(), "     1|  a")
		require.Contains(t, err.Error(), "     3|  a")
	})
}

func TestLocateEditNotFoundShowsClosest(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		"package main",
		"",
		"func helper(a int) int {",
		"\treturn a + 1",
		"}",
		"",
		"func main() {",
		"\tx := helper(2)",
		"\tprintln(x)",
		"}",
	}, "\n") + "\n"

	_, err := locateEdit(content, "func helper(a int) int {\n\treturn a + 2\n}\n", "")
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "not found")
	require.Contains(t, msg, "lines 3-5")
	require.Contains(t, msg, "     4|\treturn a + 1")
	require.NotContains(t, msg, "println")
}

func TestLocateEditMultipleExact(t *testing.T) {
	t.Parallel()

	content := "x\ny\nx\ny\nx\n"
	_, err := locateEdit(content, "x\n", "z\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "appears multiple times")
	require.Contains(t, err.Error(), "3 occurrences")
	require.Contains(t, err.Error(), "line 1, line 3, line 5")
}

func TestApplyReplaceAllRegions(t *testing.T) {
	t.Parallel()

	got, regions, err := applyReplaceAll("a\nb\na\n", "a", "cc\ndd")
	require.NoError(t, err)
	require.Equal(t, "cc\ndd\nb\ncc\ndd\n", got)
	require.Equal(t, []lineRange{{1, 2}, {4, 5}}, regions)
}

func TestShiftRegions(t *testing.T) {
	t.Parallel()

	regions := []lineRange{{10, 12}}
	regions = shiftRegions(regions, lineRange{2, 4}, 1)
	require.Equal(t, []lineRange{{2, 4}, {12, 14}}, regions)

	regions = shiftRegions(regions, lineRange{5, 5}, 3)
	require.Equal(t, []lineRange{{2, 5}, {10, 12}}, regions)
}

func TestEditSuccessTextRendersRegion(t *testing.T) {
	t.Parallel()

	content := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	text := editSuccessText("ok", content, []lineRange{{5, 5}}, "")
	require.True(t, strings.HasPrefix(text, "ok\nUpdated line 5:\n"))
	require.Contains(t, text, "     2|2")
	require.Contains(t, text, "     8|8")
	require.NotContains(t, text, "     1|1")
	require.NotContains(t, text, "     9|9")
}

func TestRenderRegionsElidesLongSpans(t *testing.T) {
	t.Parallel()

	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, "l")
	}
	out := renderRegions(strings.Join(lines, "\n")+"\n", []lineRange{{1, 200}}, 0)
	require.Contains(t, out, "lines omitted")
	require.Less(t, strings.Count(out, "\n"), 70)
}
