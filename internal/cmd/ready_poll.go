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
	// readyPollMinBackoff is the delay after the first failed probe.
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
	minBackoff time.Duration
	maxBackoff time.Duration
	attempt    time.Duration
	now        func() time.Time
	after      func(time.Duration) <-chan time.Time
}

func newReadyPoller(timeout time.Duration) readyPoller {
	return readyPoller{
		timeout:    timeout,
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
	deadline := p.now().Add(p.timeout)
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.after(min(backoff, remaining)):
		}
		backoff = min(backoff*2, p.maxBackoff)
	}
}
