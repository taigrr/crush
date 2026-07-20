package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReverseDiffFiles(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/one.go b/one.go
@@ -1 +1 @@
-a
+A
diff --git a/two.go b/two.go
@@ -1 +1 @@
-b
+B
`
	got := reverseDiffFiles(diff)
	require.True(t, strings.Index(got, "two.go") < strings.Index(got, "one.go"),
		"expected two.go before one.go after reversal:\n%s", got)
	// Each file's hunk stays with its header.
	require.Contains(t, got, "b/two.go\n@@ -1 +1 @@\n-b\n+B")
	require.Contains(t, got, "b/one.go\n@@ -1 +1 @@\n-a\n+A")
}

func TestReverseDiffFilesPreamble(t *testing.T) {
	t.Parallel()

	diff := `some preamble
diff --git a/one.go b/one.go
@@ -1 +1 @@
-a
+A
`
	got := reverseDiffFiles(diff)
	require.True(t, strings.HasPrefix(got, "some preamble\n"), "preamble must be preserved at top:\n%s", got)
}

func TestReverseDiffFilesNoBoundary(t *testing.T) {
	t.Parallel()

	diff := "not a real diff\njust text\n"
	require.Equal(t, diff, reverseDiffFiles(diff))
}

func TestBuildReviewPromptVariantsDiffer(t *testing.T) {
	t.Parallel()

	params := ReviewParams{
		Diff: `diff --git a/one.go b/one.go
@@ -1 +1 @@
-a
+A
diff --git a/two.go b/two.go
@@ -1 +1 @@
-b
+B
`,
		Goal: "do the thing",
	}
	p0 := buildReviewPrompt(params, 0)
	p1 := buildReviewPrompt(params, 1)
	require.NotEqual(t, p0, p1, "the two reviewers must receive different prompts")

	// Variant 0: natural order, diff after goal.
	require.True(t, strings.Index(p0, "<goal>") < strings.Index(p0, "<diff>"))
	require.True(t, strings.Index(p0, "one.go") < strings.Index(p0, "two.go"))

	// Variant 1: diff first, files reversed.
	require.True(t, strings.Index(p1, "<diff>") < strings.Index(p1, "<goal>"))
	require.True(t, strings.Index(p1, "two.go") < strings.Index(p1, "one.go"))
}
