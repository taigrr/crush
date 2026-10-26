package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
)

func TestStreamWatchdogFiresWhenIdle(t *testing.T) {
	t.Parallel()
	ctx, w := newStreamWatchdog(t.Context(), 20*time.Millisecond, 20*time.Millisecond)
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
	ctx, w := newStreamWatchdog(t.Context(), 20*time.Millisecond, 20*time.Millisecond)
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
	ctx, w := newStreamWatchdog(t.Context(), time.Hour, time.Hour)
	w.touch()
	w.stop()
	require.True(t, errors.Is(ctx.Err(), context.Canceled))
	require.NoError(t, stallErr(ctx))
}

func TestStreamStallTimeoutFor(t *testing.T) {
	t.Parallel()
	require.Equal(t, keepaliveStreamStallTimeout, streamStallTimeoutFor("anthropic"))
	require.Equal(t, streamStallTimeout, streamStallTimeoutFor("bedrock"), "bedrock does not forward pings")
	require.Equal(t, streamStallTimeout, streamStallTimeoutFor("openai"))
	require.Equal(t, streamStallTimeout, streamStallTimeoutFor("google"))
}

func TestStreamWatchdogExtendsDuringToolInput(t *testing.T) {
	t.Parallel()
	ctx, w := newStreamWatchdog(t.Context(), 30*time.Millisecond, time.Hour)
	defer w.stop()

	// A tool call's input starts streaming and the provider then goes
	// silent while it buffers the argument: that is not a stall.
	w.observe(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart})
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, ctx.Err())

	// Once the input ends, the short idle timeout applies again.
	w.observe(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd})
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not fire after tool input ended")
	}
	require.ErrorIs(t, stallErr(ctx), errStreamStalled)
}

func TestStreamWatchdogBeginStepClearsOpenToolInput(t *testing.T) {
	t.Parallel()
	ctx, w := newStreamWatchdog(t.Context(), 30*time.Millisecond, time.Hour)
	defer w.stop()
	w.observe(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart})
	w.beginStep()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("an input left open by an earlier step must not extend the next step")
	}
}
