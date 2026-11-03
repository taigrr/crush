package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/netutil"
)

// gatedListener is a network listener that a listenerGate binds only while
// it is wanted. The device client listener is one; the phase-2 NATS
// websocket is meant to be another.
type gatedListener interface {
	// Name labels the listener in logs.
	Name() string
	// Start binds and serves. An error (for example a busy port) is
	// retried by the gate while the listener is still wanted.
	Start() error
	// Stop releases the port at once and lets in-flight requests finish
	// in the background within a grace period, so it never blocks the
	// gate. It is idempotent.
	Stop()
	// Running reports whether the listener is bound.
	Running() bool
}

// minWake bounds how often the gate re-evaluates on its own.
const minWake = 50 * time.Millisecond

// errBackoff is returned by Start while a listener that stopped
// unexpectedly is backing off.
var errBackoff = errors.New("backing off after an unexpected stop")

// listenerGate owns the lifecycle of its listeners from a single
// goroutine, so a listener is never bound twice or left bound. It
// re-evaluates want() whenever it is kicked, when want's deadline passes
// (an offer expiring), and when a failed start is due for a retry.
type listenerGate struct {
	// want reports whether the listeners should be bound, and when to
	// re-evaluate even without a kick (zero = only on kicks).
	want  func() (bool, time.Time)
	lns   []gatedListener
	retry time.Duration
	kick  chan struct{}
}

func newListenerGate(want func() (bool, time.Time), retry time.Duration, lns ...gatedListener) *listenerGate {
	return &listenerGate{want: want, lns: lns, retry: retry, kick: make(chan struct{}, 1)}
}

// Kick asks the gate to re-evaluate. It never blocks, so it is safe to
// call with locks held.
func (g *listenerGate) Kick() {
	select {
	case g.kick <- struct{}{}:
	default:
	}
}

// run reconciles until ctx is cancelled, then stops every listener.
func (g *listenerGate) run(ctx context.Context) {
	defer func() {
		for _, ln := range g.lns {
			ln.Stop()
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.kick:
		case <-timer.C:
		}
		next := g.reconcile()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if !next.IsZero() {
			// Never spin: a deadline at or before now waits minWake.
			timer.Reset(max(time.Until(next), minWake))
		}
	}
}

// reconcile binds or unbinds each listener and returns when to look again.
func (g *listenerGate) reconcile() time.Time {
	want, next := g.want()
	for _, ln := range g.lns {
		switch {
		case want && !ln.Running():
			if err := ln.Start(); err != nil {
				slog.Error("Listener failed to start; retrying", "listener", ln.Name(), "error", err, "retry", g.retry)
				next = earliest(next, time.Now().Add(g.retry))
			}
		case !want && ln.Running():
			ln.Stop()
			slog.Info("Listener stopped: nothing to serve", "listener", ln.Name())
		}
	}
	return next
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

// httpListener is a gated TLS HTTP listener.
type httpListener struct {
	name   string
	addr   string
	server func() *http.Server
	onDown func()
	// onChange is called whenever the listener binds or unbinds, so the
	// node can re-advertise its device ports. It may run with the
	// listener locked: it must only signal, never block or call back.
	onChange func()
	maxConn  int
	grace    time.Duration
	// backoff delays a restart after the server stopped unexpectedly
	// (bound fine, then serving failed), so it cannot flap.
	backoff time.Duration

	mu    sync.Mutex
	hs    *http.Server
	ln    net.Listener
	bound string
	// last is the most recent bound address, kept after unbinding.
	last     string
	failedAt time.Time
}

func (l *httpListener) Name() string { return l.name }

func (l *httpListener) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hs != nil
}

// Addr returns the bound address ("" when not bound).
func (l *httpListener) Addr() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bound
}

// LastAddr returns the most recent bound address, even after unbinding
// ("" if it never bound).
func (l *httpListener) LastAddr() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

func (l *httpListener) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hs != nil {
		return nil
	}
	if !l.failedAt.IsZero() && time.Since(l.failedAt) < l.backoff {
		return errBackoff
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", l.addr)
	if err != nil {
		return fmt.Errorf("%s %s: %w", l.name, l.addr, err)
	}
	hs := l.server()
	l.hs, l.ln, l.bound = hs, ln, ln.Addr().String()
	l.last = l.bound
	go func() {
		err := hs.ServeTLS(netutil.LimitListener(ln, l.maxConn), "", "")
		l.mu.Lock()
		owned := l.hs == hs
		if owned {
			l.hs, l.ln, l.bound = nil, nil, ""
			l.failedAt = time.Now()
		}
		l.mu.Unlock()
		// Errors after Stop (closed listener, ErrServerClosed) are expected.
		if !owned || errors.Is(err, http.ErrServerClosed) {
			return
		}
		slog.Error("Listener stopped unexpectedly", "listener", l.name, "error", err)
		if l.onDown != nil {
			l.onDown()
		}
		if l.onChange != nil {
			l.onChange()
		}
	}()
	slog.Info("Listener started", "listener", l.name, "addr", l.bound)
	if l.onChange != nil {
		defer l.onChange()
	}
	return nil
}

func (l *httpListener) Stop() {
	l.mu.Lock()
	hs, ln := l.hs, l.ln
	l.hs, l.ln, l.bound = nil, nil, ""
	l.mu.Unlock()
	if hs == nil {
		return
	}
	if l.onChange != nil {
		defer l.onChange()
	}
	// Free the port now so a new offer can rebind immediately; drain
	// in-flight requests (a poll learning its final state) in the
	// background.
	_ = ln.Close()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), l.grace)
		defer cancel()
		if hs.Shutdown(ctx) != nil {
			// Long-polls can outlast the grace period; cut them.
			_ = hs.Close()
		}
	}()
}
