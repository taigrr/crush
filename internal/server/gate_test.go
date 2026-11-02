package server

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeListener records gate calls.
type fakeListener struct {
	running atomic.Bool
	starts  atomic.Int32
	stops   atomic.Int32
	fail    atomic.Bool
}

func (f *fakeListener) Name() string { return "fake" }

func (f *fakeListener) Start() error {
	f.starts.Add(1)
	if f.fail.Load() {
		return errBackoff
	}
	f.running.Store(true)
	return nil
}

func (f *fakeListener) Stop() {
	if f.running.Swap(false) {
		f.stops.Add(1)
	}
}

func (f *fakeListener) Running() bool { return f.running.Load() }

func TestGateDrivesListenersAndNeverSpins(t *testing.T) {
	t.Parallel()
	var want atomic.Bool
	var calls atomic.Int32
	a, b := &fakeListener{}, &fakeListener{}
	g := newListenerGate(func() (bool, time.Time) {
		calls.Add(1)
		// A deadline in the past must not cause a busy loop.
		return want.Load(), time.Now().Add(-time.Second)
	}, time.Hour, a, b)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { g.run(ctx); close(done) }()

	want.Store(true)
	g.Kick()
	require.Eventually(t, func() bool { return a.Running() && b.Running() }, time.Second, 5*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	require.Less(t, calls.Load(), int32(20), "the gate re-evaluates at most every minWake")
	require.EqualValues(t, 1, a.starts.Load(), "no double bind")

	want.Store(false)
	g.Kick()
	require.Eventually(t, func() bool { return !a.Running() && !b.Running() }, time.Second, 5*time.Millisecond)

	// A failing start is retried only while wanted, after the retry delay.
	a.fail.Store(true)
	want.Store(true)
	g.Kick()
	require.Eventually(t, func() bool { return a.starts.Load() >= 2 }, time.Second, 5*time.Millisecond)
	starts := a.starts.Load()
	time.Sleep(200 * time.Millisecond)
	require.LessOrEqual(t, a.starts.Load(), starts+4, "failed starts are not hammered")

	cancel()
	<-done
	require.False(t, b.Running(), "cancel stops every listener")
}

func TestHTTPListenerStopFreesPortImmediately(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	cert := testServerCert(t)
	block := make(chan struct{})
	l := &httpListener{
		name: "test", addr: addr, maxConn: 4, grace: 5 * time.Second, backoff: time.Hour,
		server: func() *http.Server {
			return &http.Server{
				TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
				Handler:   http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }),
			}
		},
	}
	require.NoError(t, l.Start())
	require.NoError(t, l.Start(), "starting twice is a no-op")

	// An in-flight request is still draining when Stop returns.
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	go func() {
		if rsp, err := hc.Get("https://" + addr); err == nil {
			rsp.Body.Close()
		}
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	l.Stop()
	require.Less(t, time.Since(start), time.Second, "Stop does not wait for the grace period")
	require.NoError(t, l.Start(), "the port is free right away")
	close(block)
	l.Stop()
}

func testServerCert(t *testing.T) tls.Certificate {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(nil, tmpl, tmpl, pub, priv)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}
