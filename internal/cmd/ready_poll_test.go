package cmd

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeClock advances virtual time whenever the poller sleeps.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (f *fakeClock) poller(timeout time.Duration) readyPoller {
	p := newReadyPoller(timeout)
	p.now = func() time.Time { return f.now }
	p.after = func(d time.Duration) <-chan time.Time {
		f.sleeps = append(f.sleeps, d)
		f.now = f.now.Add(d)
		ch := make(chan time.Time, 1)
		ch <- f.now
		return ch
	}
	return p
}

func dialErr(errno syscall.Errno) error {
	return fmt.Errorf("dial unix /tmp/crush.sock: connect: %w", errno)
}

func TestReadyPollerSocketAppearsLate(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Unix(0, 0)}
	calls := 0
	err := clock.poller(defaultServerReadyTimeout).poll(t.Context(), func(context.Context) error {
		calls++
		switch {
		case calls < 4:
			return dialErr(syscall.ENOENT)
		case calls < 6:
			return dialErr(syscall.ECONNREFUSED)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 6, calls)
	require.Equal(t, []time.Duration{
		readyPollMinBackoff,
		2 * readyPollMinBackoff,
		4 * readyPollMinBackoff,
		8 * readyPollMinBackoff,
		readyPollMaxBackoff,
	}, clock.sleeps)
}

func TestReadyPollerDeadlineReturnsLastError(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Unix(0, 0)}
	const timeout = 5 * time.Second
	calls := 0
	err := clock.poller(timeout).poll(t.Context(), func(context.Context) error {
		calls++
		if calls == 1 {
			return dialErr(syscall.ECONNREFUSED)
		}
		return dialErr(syscall.ENOENT)
	})
	require.Error(t, err)
	require.ErrorIs(t, err, syscall.ENOENT)
	require.Greater(t, calls, 2)
	require.Equal(t, time.Unix(0, 0).Add(timeout), clock.now)
	for _, d := range clock.sleeps {
		require.LessOrEqual(t, d, readyPollMaxBackoff)
	}
}

func TestReadyPollerContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	p := newReadyPoller(time.Hour)
	calls := 0
	err := p.poll(ctx, func(context.Context) error {
		calls++
		cancel()
		return dialErr(syscall.ENOENT)
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}

func TestReadyPollerStopPolling(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Unix(0, 0)}
	permanent := errors.New("wrong build")
	calls := 0
	err := clock.poller(time.Minute).poll(t.Context(), func(context.Context) error {
		calls++
		return stopPolling(permanent)
	})
	require.ErrorIs(t, err, permanent)
	require.Equal(t, 1, calls)
}

func TestServerReadyTimeoutDefault(t *testing.T) {
	t.Setenv("CRUSH_SERVER_READY_TIMEOUT", "")
	require.Equal(t, defaultServerReadyTimeout, serverReadyTimeout())
	t.Setenv("CRUSH_SERVER_READY_TIMEOUT", "2s")
	require.Equal(t, 2*time.Second, serverReadyTimeout())
}
