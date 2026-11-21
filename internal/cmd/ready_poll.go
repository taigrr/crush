package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// defaultServerReadyTimeout is the overall budget for a server or
	// background service to start answering health checks. A service
	// manager restart can take several seconds to recreate the socket.
	defaultServerReadyTimeout = 30 * time.Second
	// readyPollFastInterval is the probe spacing during the first
	// readyPollFastWindow. A freshly spawned server usually starts
	// listening within a few hundred milliseconds, so exponential backoff
	// from the very first probe would overshoot that by up to a full
	// backoff step. A failed unix-socket dial is cheap.
	readyPollFastInterval = 10 * time.Millisecond
	// readyPollFastWindow is how long probes stay at readyPollFastInterval
	// before switching to exponential backoff.
	readyPollFastWindow = time.Second
	// readyPollMinBackoff is the first backoff delay after the fast window.
	readyPollMinBackoff = 100 * time.Millisecond
	// readyPollMaxBackoff caps the delay between probes.
	readyPollMaxBackoff = time.Second
	// readyPollAttemptTimeout bounds a single probe so a hung listener
	// doesn't burn the whole budget.
	readyPollAttemptTimeout = time.Second
)

// readyPoller polls a probe with exponential backoff until it succeeds,
// the deadline passes, or the context is canceled. The clock is
// injectable so tests stay hermetic.
type readyPoller struct {
	timeout    time.Duration
	fast       time.Duration
	fastWindow time.Duration
	minBackoff time.Duration
	maxBackoff time.Duration
	attempt    time.Duration
	now        func() time.Time
	after      func(time.Duration) <-chan time.Time
}

func newReadyPoller(timeout time.Duration) readyPoller {
	return readyPoller{
		timeout:    timeout,
		fast:       readyPollFastInterval,
		fastWindow: readyPollFastWindow,
		minBackoff: readyPollMinBackoff,
		maxBackoff: readyPollMaxBackoff,
		attempt:    readyPollAttemptTimeout,
		now:        time.Now,
		after:      time.After,
	}
}

// errStopPolling wraps a probe error that will not resolve by waiting.
type errStopPolling struct{ err error }

func (e *errStopPolling) Error() string { return e.err.Error() }
func (e *errStopPolling) Unwrap() error { return e.err }

// stopPolling marks err as permanent so [readyPoller.poll] returns it
// immediately instead of retrying.
func stopPolling(err error) error { return &errStopPolling{err: err} }

// poll runs probe until it returns nil. Any error (ENOENT, ECONNREFUSED,
// non-2xx, ...) counts as "not up yet" unless wrapped with
// [stopPolling]. After the deadline it returns the last probe error.
func (p readyPoller) poll(ctx context.Context, probe func(context.Context) error) error {
	start := p.now()
	deadline := start.Add(p.timeout)
	backoff := p.minBackoff
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, p.attempt)
		err := probe(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if stop, ok := errors.AsType[*errStopPolling](err); ok {
			return stop.err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		lastErr = err

		remaining := deadline.Sub(p.now())
		if remaining <= 0 {
			return fmt.Errorf("timed out after %s: %w", p.timeout, lastErr)
		}
		delay := backoff
		fast := p.now().Sub(start) < p.fastWindow
		if fast {
			delay = p.fast
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.after(min(delay, remaining)):
		}
		if !fast {
			backoff = min(backoff*2, p.maxBackoff)
		}
	}
}
