package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackgroundRequest_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "s1")
	ch, release := RegisterBackgroundable(ctx, "call-rt")
	defer release()
	require.Contains(t, BackgroundableToolCalls("s1"), "call-rt")
	require.NotContains(t, BackgroundableToolCalls("other"), "call-rt")

	require.False(t, RequestBackground("other", "call-rt"), "another session must not background this call")
	select {
	case <-ch:
		t.Fatal("must not fire for a mismatched session")
	default:
	}

	require.True(t, RequestBackground("s1", "call-rt"))
	select {
	case <-ch:
	default:
		t.Fatal("channel must be closed after a matching request")
	}
	require.False(t, RequestBackground("s1", "call-rt"), "second request is a no-op, not a double close")
	require.NotContains(t, BackgroundableToolCalls("s1"), "call-rt", "fired calls are no longer offered")
}

func TestBackgroundRequest_ReleaseAndUnknown(t *testing.T) {
	t.Parallel()
	require.False(t, RequestBackground("s2", "never-registered"))
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "s2")
	_, release := RegisterBackgroundable(ctx, "call-rel")
	release()
	require.False(t, RequestBackground("s2", "call-rel"), "released calls cannot be backgrounded")

	// Re-registering after release yields a fresh channel; a stale
	// release from the first registration must not remove it.
	_, release1 := RegisterBackgroundable(ctx, "call-re")
	ch2, release2 := RegisterBackgroundable(ctx, "call-re")
	release1()
	require.True(t, RequestBackground("s2", "call-re"))
	<-ch2
	release2()

	ch, release := RegisterBackgroundable(ctx, "")
	require.Nil(t, ch, "empty IDs are never registered")
	release()
}

func TestBackgroundRequest_SessionMustMatchExactly(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "s3")
	ch, release := RegisterBackgroundable(ctx, "call-any")
	defer release()
	require.False(t, RequestBackground("", "call-any"), "an empty session must not act as a wildcard")
	require.Empty(t, BackgroundableToolCalls(""), "an empty session lists nothing registered under a real one")
	require.True(t, RequestBackground("s3", "call-any"))
	<-ch
}
