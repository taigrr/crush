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
	// Stop stops serving, letting in-flight requests finish within a
	// grace period. It is idempotent.
	Stop()
	// Running reports whether the listener is bound.
	Running() bool
}

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
			timer.Reset(max(time.Until(next), 0))
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
	name    string
	addr    string
	server  func() *http.Server
	onDown  func()
	maxConn int
	grace   time.Duration

	mu    sync.Mutex
	hs    *http.Server
	bound string
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

func (l *httpListener) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hs != nil {
		return nil
	}
	ln, err := net.Listen("tcp", l.addr)
	if err != nil {
		return fmt.Errorf("%s %s: %w", l.name, l.addr, err)
	}
	hs := l.server()
	l.hs, l.bound = hs, ln.Addr().String()
	go func() {
		err := hs.ServeTLS(netutil.LimitListener(ln, l.maxConn), "", "")
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return
		}
		slog.Error("Listener stopped unexpectedly", "listener", l.name, "error", err)
		l.mu.Lock()
		if l.hs == hs {
			l.hs, l.bound = nil, ""
		}
		l.mu.Unlock()
		if l.onDown != nil {
			l.onDown()
		}
	}()
	slog.Info("Listener started", "listener", l.name, "addr", l.bound)
	return nil
}

func (l *httpListener) Stop() {
	l.mu.Lock()
	hs := l.hs
	l.hs, l.bound = nil, ""
	l.mu.Unlock()
	if hs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), l.grace)
	defer cancel()
	if hs.Shutdown(ctx) != nil {
		// Long-polls can outlast the grace period; cut them so the port
		// is free for the next bind.
		_ = hs.Close()
	}
}
