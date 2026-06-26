package list

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

// area large enough to hold the sample content.
func testArea(w, h int) image.Rectangle {
	return image.Rect(0, 0, w, h)
}

func TestHighlightContent_SingleLineRange(t *testing.T) {
	t.Parallel()
	// Select cols [0,5) of "hello world" -> "hello".
	got := HighlightContent("hello world", testArea(20, 1), 0, 0, 0, 5)
	require.Equal(t, "hello\n", got)
}

func TestHighlightContent_FullSingleLine(t *testing.T) {
	t.Parallel()
	// endCol -1 means to end of line; trailing blanks are trimmed to last
	// content cell.
	got := HighlightContent("hello", testArea(20, 1), 0, 0, 0, -1)
	require.Equal(t, "hello\n", got)
}

func TestHighlightContent_MultiLine(t *testing.T) {
	t.Parallel()
	content := "line one\nline two\nline three"
	// From line 0 col 5 to line 2 col 4: "one" + full "line two" + "line".
	got := HighlightContent(content, testArea(20, 3), 0, 5, 2, 4)
	require.Equal(t, "one\nline two\nline\n", got)
}

func TestHighlightContent_NegativeStartReturnsEmpty(t *testing.T) {
	t.Parallel()
	require.Empty(t, HighlightContent("hello", testArea(20, 1), -1, 0, 0, 5))
	require.Empty(t, HighlightContent("hello", testArea(20, 1), 0, -1, 0, 5))
}

func TestHighlightContent_TrailingSpacesTrimmed(t *testing.T) {
	t.Parallel()
	// Selecting beyond the content should not highlight trailing blanks.
	got := HighlightContent("hi", testArea(20, 1), 0, 0, 0, 10)
	require.Equal(t, "hi\n", got)
}

func TestHighlightContent_BlankLineInRange(t *testing.T) {
	t.Parallel()
	content := "a\n\nb"
	got := HighlightContent(content, testArea(10, 3), 0, 0, 2, -1)
	require.Equal(t, "a\n\nb\n", got)
}
