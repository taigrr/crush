package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestStream() (*runStream, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &runStream{out: buf, read: map[string]int{}}, buf
}

func TestEmitIncremental_TrimsLeadingOnlyOnFirstChunk(t *testing.T) {
	t.Parallel()

	s, buf := newTestStream()

	// First chunk: leading spaces/tabs trimmed.
	require.NoError(t, s.emitIncremental("m1", "  \thello"))
	require.Equal(t, "hello", buf.String())

	// Second chunk: only the new suffix is written, no trimming.
	require.NoError(t, s.emitIncremental("m1", "  \thello world"))
	require.Equal(t, "hello world", buf.String())
}

func TestEmitIncremental_SuppressesBlankPrefix(t *testing.T) {
	t.Parallel()

	s, buf := newTestStream()

	// All-whitespace content before any real output: nothing printed,
	// and printed stays false so we keep trimming on the next chunk.
	require.NoError(t, s.emitIncremental("m1", "   "))
	require.Equal(t, "", buf.String())
	require.False(t, s.printed)

	require.NoError(t, s.emitIncremental("m1", "   real"))
	require.Equal(t, "real", buf.String())
	require.True(t, s.printed)
}

func TestEmitIncremental_PreservesInteriorWhitespaceAfterPrinted(t *testing.T) {
	t.Parallel()

	s, buf := newTestStream()

	require.NoError(t, s.emitIncremental("m1", "a"))
	// Once printed, even an all-whitespace delta is emitted verbatim.
	require.NoError(t, s.emitIncremental("m1", "a   "))
	require.Equal(t, "a   ", buf.String())
}

func TestEmitIncremental_NoopWhenNoNewBytes(t *testing.T) {
	t.Parallel()

	s, buf := newTestStream()
	require.NoError(t, s.emitIncremental("m1", "done"))
	buf.Reset()
	// Same content again: cursor already at end, nothing new to write.
	require.NoError(t, s.emitIncremental("m1", "done"))
	require.Equal(t, "", buf.String())
}

func TestEmitIncremental_ErrorsWhenContentShrinks(t *testing.T) {
	t.Parallel()

	s, _ := newTestStream()
	require.NoError(t, s.emitIncremental("m1", "hello world"))
	err := s.emitIncremental("m1", "hi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "shorter than read bytes")
}

func TestEmitIncremental_TracksMessagesIndependently(t *testing.T) {
	t.Parallel()

	s, buf := newTestStream()
	require.NoError(t, s.emitIncremental("m1", "one"))
	require.NoError(t, s.emitIncremental("m2", "two"))
	require.Equal(t, "onetwo", buf.String())
	require.Equal(t, 3, s.read["m1"])
	require.Equal(t, 3, s.read["m2"])
}
