package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSoftInterrupt_AbsentNeverFires(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	require.Nil(t, SoftInterrupt(ctx))
	require.False(t, SoftInterrupted(ctx))
	select {
	case <-SoftInterrupt(ctx):
		t.Fatal("nil soft-interrupt channel must never fire")
	default:
	}
}

func TestSoftInterrupt_NilChannelIgnored(t *testing.T) {
	t.Parallel()
	ctx := WithSoftInterrupt(t.Context(), nil)
	require.Nil(t, SoftInterrupt(ctx))
	require.False(t, SoftInterrupted(ctx))
}

func TestSoftInterrupt_FiresWhenClosed(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{})
	ctx := WithSoftInterrupt(t.Context(), ch)
	require.NotNil(t, SoftInterrupt(ctx))
	require.False(t, SoftInterrupted(ctx))
	close(ch)
	require.True(t, SoftInterrupted(ctx))
	select {
	case <-SoftInterrupt(ctx):
	default:
		t.Fatal("closed soft-interrupt channel must be readable")
	}
}

func TestSoftInterrupt_LatestWins(t *testing.T) {
	t.Parallel()
	first := make(chan struct{})
	second := make(chan struct{})
	ctx := WithSoftInterrupt(WithSoftInterrupt(t.Context(), first), second)
	close(first)
	require.False(t, SoftInterrupted(ctx), "a re-armed step must not observe the previous step's interrupt")
	close(second)
	require.True(t, SoftInterrupted(ctx))
	// Derived contexts inherit the channel.
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	require.True(t, SoftInterrupted(child))
}
