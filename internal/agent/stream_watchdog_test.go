package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamWatchdogFiresWhenIdle(t *testing.T) {
	t.Parallel()
	ctx, w := newStreamWatchdog(t.Context(), 20*time.Millisecond)
	defer w.stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not fire")
	}
	require.ErrorIs(t, stallErr(ctx), errStreamStalled)
}

func TestStreamWatchdogPausedDuringTools(t *testing.T) {
	t.Parallel()
	ctx, w := newStreamWatchdog(t.Context(), 20*time.Millisecond)
	defer w.stop()
	w.toolStarted()
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, ctx.Err())
	w.toolFinished()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not fire after tool finished")
	}
	require.ErrorIs(t, stallErr(ctx), errStreamStalled)
}

func TestStreamWatchdogStopIsNotStall(t *testing.T) {
	t.Parallel()
	ctx, w := newStreamWatchdog(t.Context(), time.Hour)
	w.touch()
	w.stop()
	require.True(t, errors.Is(ctx.Err(), context.Canceled))
	require.NoError(t, stallErr(ctx))
}

func TestStreamStallTimeoutFor(t *testing.T) {
	t.Parallel()
	require.Equal(t, keepaliveStreamStallTimeout, streamStallTimeoutFor("anthropic"))
	require.Equal(t, keepaliveStreamStallTimeout, streamStallTimeoutFor("bedrock"))
	require.Equal(t, streamStallTimeout, streamStallTimeoutFor("openai"))
	require.Equal(t, streamStallTimeout, streamStallTimeoutFor("google"))
}
