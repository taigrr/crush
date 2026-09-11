package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyRegexEdit_Single(t *testing.T) {
	t.Parallel()
	content := "a = foo(1)\nb = bar(2)\n"
	got, spans, err := applyRegexEdit(content, `foo\((\d)\)`, "baz($1)", false)
	require.NoError(t, err)
	require.Equal(t, "a = baz(1)\nb = bar(2)\n", got)
	require.Equal(t, []editSpan{{region: lineRange{1, 1}, oldLines: 1}}, spans)
}

func TestApplyRegexEdit_ReplaceAll(t *testing.T) {
	t.Parallel()
	content := "x.reloc = null;\nkeep\n  y.reloc = null;\nend\n"
	got, spans, err := applyRegexEdit(content, `(?m)^\s*\w+\.reloc = null;\n`, "", true)
	require.NoError(t, err)
	require.Equal(t, "keep\nend\n", got)
	require.Len(t, spans, 2)
}

func TestApplyRegexEdit_WordBoundaryAndNamedGroup(t *testing.T) {
	t.Parallel()
	content := "Settings SettingsService Settings\n"
	got, spans, err := applyRegexEdit(content, `\bSettings\b(?P<tail>[^A-Za-z]|$)`, "AppSettings${tail}", true)
	require.NoError(t, err)
	require.Equal(t, "AppSettings SettingsService AppSettings\n", got)
	require.Len(t, spans, 2)
}

func TestApplyRegexEdit_MultipleWithoutReplaceAll(t *testing.T) {
	t.Parallel()
	_, _, err := applyRegexEdit("a\na\n", `a`, "b", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "matches 2 times")
	require.Contains(t, err.Error(), "replace_all=true")
}

func TestApplyRegexEdit_Errors(t *testing.T) {
	t.Parallel()
	_, _, err := applyRegexEdit("abc", `(?<=a)b`, "x", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid regex")

	_, _, err = applyRegexEdit("abc", `zzz`, "x", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "matched nothing")

	_, _, err = applyRegexEdit("abc", `b`, "b", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "identical")
}

func TestApplyOperation_RegexSpansShift(t *testing.T) {
	t.Parallel()
	content := "one\ntwo\nthree\n"
	got, spans, note, err := applyOperation(content, MultiEditOperation{
		OldString: `two\n`, NewString: "2a\n2b\n", Regex: true,
	})
	require.NoError(t, err)
	require.Equal(t, "one\n2a\n2b\nthree\n", got)
	require.Contains(t, note, "regex replaced 1")
	require.Equal(t, []editSpan{{region: lineRange{2, 3}, oldLines: 1}}, spans)
}
