package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamDelta(t *testing.T) {
	t.Parallel()

	t.Run("first chunk trims leading whitespace", func(t *testing.T) {
		t.Parallel()
		delta, n, printed, err := streamDelta("  \t hello", 0, false)
		require.NoError(t, err)
		require.Equal(t, "hello", delta)
		require.Equal(t, len("  \t hello"), n)
		require.True(t, printed)
	})

	t.Run("subsequent chunk returns only new bytes without trimming", func(t *testing.T) {
		t.Parallel()
		delta, n, printed, err := streamDelta("hello world", len("hello"), true)
		require.NoError(t, err)
		require.Equal(t, " world", delta)
		require.Equal(t, len("hello world"), n)
		require.True(t, printed)
	})

	t.Run("whitespace-only first chunk is not printed", func(t *testing.T) {
		t.Parallel()
		delta, n, printed, err := streamDelta("   \n\t", 0, false)
		require.NoError(t, err)
		require.Empty(t, delta)
		// read bytes still advances so the next chunk diffs correctly.
		require.Equal(t, len("   \n\t"), n)
		require.False(t, printed)
	})

	t.Run("whitespace chunk after printing is still emitted", func(t *testing.T) {
		t.Parallel()
		// once printed, even a whitespace-only delta is forwarded verbatim.
		delta, n, printed, err := streamDelta("hi   ", len("hi"), true)
		require.NoError(t, err)
		require.Equal(t, "   ", delta)
		require.Equal(t, len("hi   "), n)
		require.True(t, printed)
	})

	t.Run("no new content returns empty delta", func(t *testing.T) {
		t.Parallel()
		delta, n, printed, err := streamDelta("hello", len("hello"), true)
		require.NoError(t, err)
		require.Empty(t, delta)
		require.Equal(t, len("hello"), n)
		require.True(t, printed)
	})

	t.Run("content shorter than readBytes is an error", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := streamDelta("hi", 5, true)
		require.Error(t, err)
	})

	t.Run("first observed bytes always trim regardless of printed flag", func(t *testing.T) {
		t.Parallel()
		// readBytes==0 trims leading whitespace; the alreadyPrinted flag does
		// not change this (matches the original readBytes==0 condition).
		delta, _, printed, err := streamDelta("  x", 0, true)
		require.NoError(t, err)
		require.Equal(t, "x", delta)
		require.True(t, printed)
	})
}
