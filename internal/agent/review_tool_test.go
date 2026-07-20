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

func TestReverseDiffFilesIgnoresInContentMarker(t *testing.T) {
	t.Parallel()

	// A single-file diff whose added lines contain "diff --git " in
	// hunk content (a diff-of-a-diff). It must NOT be split there.
	diff := `diff --git a/one.go b/one.go
@@ -1 +2 @@
+diff --git a/embedded.txt b/embedded.txt
+more content
`
	got := reverseDiffFiles(diff)
	// Only one real file boundary → unchanged.
	require.Equal(t, diff, got)
}

func TestBuildReviewPromptVariantsDiffer(t *testing.T) {
	t.Parallel()

	change := `diff --git a/one.go b/one.go
@@ -1 +1 @@
-a
+A
diff --git a/two.go b/two.go
@@ -1 +1 @@
-b
+B
`
	params := ReviewParams{
		Command: "git diff",
		Goal:    "do the thing",
	}
	p0 := buildReviewPrompt(params, change, 0)
	p1 := buildReviewPrompt(params, change, 1)
	require.NotEqual(t, p0, p1, "the two reviewers must receive different prompts")

	// Variant 0: natural order, diff after goal.
	require.True(t, strings.Index(p0, "<goal>") < strings.Index(p0, "<diff>"))
	require.True(t, strings.Index(p0, "one.go") < strings.Index(p0, "two.go"))

	// Variant 1: diff first, files reversed.
	require.True(t, strings.Index(p1, "<diff>") < strings.Index(p1, "<goal>"))
	require.True(t, strings.Index(p1, "two.go") < strings.Index(p1, "one.go"))
}

func TestReviewSubToolCallIDRoundtrip(t *testing.T) {
	t.Parallel()

	parent := "toolu_01ABC"
	for i := range reviewerCount {
		child := reviewSubToolCallID(parent, i)
		require.NotEqual(t, parent, child, "child id must differ from parent")
		require.Equal(t, parent, StripReviewSuffix(child),
			"stripping the suffix must recover the parent id")
	}
	// Distinct children per reviewer (so they get distinct sessions).
	require.NotEqual(t, reviewSubToolCallID(parent, 0), reviewSubToolCallID(parent, 1))
	// No suffix is a no-op.
	require.Equal(t, parent, StripReviewSuffix(parent))
}

func TestReviewerIndexFromToolCallID(t *testing.T) {
	t.Parallel()

	parent := "toolu_01ABC"
	for i := range 5 {
		got, ok := ReviewerIndexFromToolCallID(reviewSubToolCallID(parent, i))
		require.True(t, ok)
		require.Equal(t, i, got)
	}
	// A parent id (no suffix) reports no reviewer index.
	_, ok := ReviewerIndexFromToolCallID(parent)
	require.False(t, ok)
	// ReviewSubToolCallID and reviewSubToolCallID agree.
	require.Equal(t, reviewSubToolCallID(parent, 3), ReviewSubToolCallID(parent, 3))
}
