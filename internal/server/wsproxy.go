package server

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/pairing"
	"golang.org/x/net/netutil"
)

// tlsProxyListener is a gated TLS front for the embedded NATS server's
// loopback device websocket. NATS cannot toggle its websocket listener by
// config reload, so it stays on loopback and this listener, which the gate
// binds only while wanted, terminates TLS with the node's client
// certificate and splices bytes to it.
type tlsProxyListener struct {
	name      string
	addr      string
	target    func() string
	tls       func() *tls.Config
	maxConn   int
	perSource int
	onDown    func()
	backoff   time.Duration

	mu       sync.Mutex
	ln       net.Listener
	bound    string
	conns    map[net.Conn]struct{}
	sources  map[string]int
	failedAt time.Time
}

const (
	proxyHandshakeTimeout = 10 * time.Second
	proxyDialTimeout      = 5 * time.Second
)

func (l *tlsProxyListener) Name() string { return l.name }

func (l *tlsProxyListener) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ln != nil
}

// Addr returns the bound address ("" when not bound).
func (l *tlsProxyListener) Addr() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bound
}

func (l *tlsProxyListener) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln != nil {
		return nil
	}
	if !l.failedAt.IsZero() && time.Since(l.failedAt) < l.backoff {
		return errBackoff
	}
	if l.target() == "" {
		return errors.New("no websocket target")
	}
	ln, err := net.Listen("tcp", l.addr)
	if err != nil {
		return fmt.Errorf("%s %s: %w", l.name, l.addr, err)
	}
	l.ln, l.bound = ln, ln.Addr().String()
	l.conns, l.sources = map[net.Conn]struct{}{}, map[string]int{}
	go l.serve(ln)
	slog.Info("Listener started", "listener", l.name, "addr", l.bound)
	return nil
}

func (l *tlsProxyListener) serve(ln net.Listener) {
	limited := netutil.LimitListener(ln, l.maxConn)
	for {
		conn, err := limited.Accept()
		if err != nil {
			l.mu.Lock()
			owned := l.ln == ln
			if owned {
				l.ln, l.bound = nil, ""
				l.failedAt = time.Now()
			}
			l.mu.Unlock()
			if owned {
				slog.Error("Listener stopped unexpectedly", "listener", l.name, "error", err)
				if l.onDown != nil {
					l.onDown()
				}
			}
			return
		}
		source := sourceOf(conn.RemoteAddr())
		if !l.track(ln, conn, source) {
			conn.Close()
			continue
		}
		go l.handle(ln, conn, source)
	}
}

func sourceOf(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	return pairing.SourceKey(host)
}

// track registers a connection unless the listener stopped or the source
// is over its limit.
func (l *tlsProxyListener) track(ln net.Listener, conn net.Conn, source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln != ln || l.sources[source] >= l.perSource {
		return false
	}
	l.conns[conn] = struct{}{}
	l.sources[source]++
	return true
}

func (l *tlsProxyListener) untrack(conn net.Conn, source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.conns[conn]; !ok {
		return
	}
	delete(l.conns, conn)
	if l.sources[source]--; l.sources[source] <= 0 {
		delete(l.sources, source)
	}
}

func (l *tlsProxyListener) handle(ln net.Listener, raw net.Conn, source string) {
	defer l.untrack(raw, source)
	defer raw.Close()
	conn := tls.Server(raw, l.tls())
	_ = conn.SetDeadline(time.Now().Add(proxyHandshakeTimeout))
	if err := conn.Handshake(); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	upstream, err := net.DialTimeout("tcp", l.target(), proxyDialTimeout)
	if err != nil {
		slog.Warn("Websocket upstream unavailable", "error", err)
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
	// Either side closing (the server dropping a revoked device, or the
	// phone leaving) ends both.
	<-done
}

// Stop closes the listener and every proxied connection.
func (l *tlsProxyListener) Stop() {
	l.mu.Lock()
	ln, conns := l.ln, l.conns
	l.ln, l.bound, l.conns, l.sources = nil, "", nil, nil
	l.mu.Unlock()
	if ln == nil {
		return
	}
	_ = ln.Close()
	for c := range conns {
		_ = c.Close()
	}
}
