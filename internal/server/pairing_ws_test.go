package server

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
)

// phoneHost is the address a phone would use; the test dialer maps it to
// loopback so the poll response's real URL can be used as-is.
const phoneHost = "laptop.phone.test"

type mapDialer struct{}

func (mapDialer) Dial(network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host == phoneHost {
		host = "127.0.0.1"
	}
	return net.DialTimeout(network, net.JoinHostPort(host, port), 3*time.Second)
}

func initPhoneNode(t *testing.T) (*Server, *client.Client) {
	t.Helper()
	srv, c := newClusterTestServerWith(t, "")
	_, port, _ := net.SplitHostPort(srv.clusterAddr)
	_, err := c.ClusterInit(t.Context(), proto.ClusterInitRequest{Endpoints: []cluster.Endpoint{
		{Kind: cluster.EndpointDirect, Addr: srv.clusterAddr},
		{Kind: cluster.EndpointDirect, Addr: net.JoinHostPort(phoneHost, port)},
	}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return srv.mesh != nil }, 10*time.Second, 20*time.Millisecond)
	return srv, c
}

// phone is a paired device's NATS identity, built only from what the
// pairing flow gave it.
type phone struct {
	key     ed25519.PrivateKey
	id      string
	url     string
	caFP    []byte
	caCerts [][]byte
	nodeID  string
}

func pairPhone(t *testing.T, srv *Server, c *client.Client) phone {
	t.Helper()
	key := newDeviceKey(t)
	poll := pairFlowA(t, srv, c, key)
	require.NotNil(t, poll.NATS)
	require.Len(t, poll.NATS.URLs, 1, "loopback endpoints are never advertised")
	_, wsPort, _ := net.SplitHostPort(srv.pairing.wsAddr)
	require.Equal(t, "wss://"+net.JoinHostPort(phoneHost, wsPort), poll.NATS.URLs[0])
	require.Len(t, poll.CAFPs, 1)
	require.Len(t, poll.CACerts, 1)
	return phone{
		key: key, id: poll.Device.DeviceID, url: poll.NATS.URLs[0],
		caFP: poll.CAFPs[0], caCerts: [][]byte{poll.CACerts[0]}, nodeID: poll.Members[0].NodeID,
	}
}

func (p phone) connect(t *testing.T, opts ...nats.Option) (*nats.Conn, error) {
	t.Helper()
	kp, err := nkeys.FromRawSeed(nkeys.PrefixByteUser, p.key.Seed())
	require.NoError(t, err)
	user, err := kp.PublicKey()
	require.NoError(t, err)
	tlsCfg, err := pairing.TrustedTLSConfig(p.caCerts, [][]byte{p.caFP}, p.nodeID)
	if err != nil {
		return nil, err
	}
	base := []nats.Option{
		nats.SetCustomDialer(mapDialer{}),
		nats.Secure(tlsCfg),
		nats.Nkey(user, kp.Sign),
		nats.CustomInboxPrefix("_INBOX." + p.id),
		nats.NoReconnect(), nats.Timeout(3 * time.Second),
	}
	return nats.Connect(p.url, append(base, opts...)...)
}

func TestDeviceWebsocketEndToEnd(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)

	errs := make(chan error, 16)
	closed := make(chan struct{})
	nc, err := ph.connect(t,
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errs <- err }),
		nats.ClosedHandler(func(*nats.Conn) { close(closed) }))
	require.NoError(t, err)
	defer nc.Close()

	// The mesh watches internal subjects; nothing from the device may
	// ever arrive there.
	leaked := make(chan string, 8)
	for _, subj := range []string{"crush.>", "_INBOX.>", "$SYS.>"} {
		_, err := srv.mesh.Subscribe(subj, func(m *nats.Msg) { leaked <- m.Subject })
		require.NoError(t, err)
	}
	require.NoError(t, srv.mesh.Conn().Flush())

	// Allowed: own subjects, including request/reply on the private inbox.
	own := "crush.client.v1." + ph.id + ".echo"
	_, err = nc.Subscribe(own, func(m *nats.Msg) { _ = m.Respond(m.Data) })
	require.NoError(t, err)
	reply, err := nc.Request(own, []byte("ping"), 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "ping", string(reply.Data))

	// Denied publishes and subscriptions are refused by the server.
	other := pairing.DeviceID(newDeviceKey(t).Public().(ed25519.PublicKey))
	for _, subj := range []string{"crush.creds", "crush.devices", "crush.repl.changed", "crush.client.v1." + other + ".api.x", "$SYS.REQ.SERVER.PING", "$JS.API.INFO", "_INBOX.x"} {
		require.NoError(t, nc.Publish(subj, []byte("forged")))
	}
	for _, subj := range []string{">", "crush.>", "crush.client.v1.*.>", "crush.client.v1." + other + ".>", "_INBOX.>", "$SYS.>", "$JS.API.>"} {
		_, err := nc.Subscribe(subj, func(*nats.Msg) {})
		require.NoError(t, err)
	}
	require.NoError(t, nc.Flush())
	violations := 0
	deadline := time.After(5 * time.Second)
	for violations < 14 {
		select {
		case err := <-errs:
			require.ErrorContains(t, err, "Permissions Violation")
			violations++
		case <-deadline:
			t.Fatalf("only %d of 14 violations reported", violations)
		}
	}
	select {
	case subj := <-leaked:
		t.Fatalf("device message reached the mesh on %s", subj)
	case <-time.After(200 * time.Millisecond):
	}

	// Revocation drops the live connection and refuses a reconnect.
	_, err = c.RevokeDevice(t.Context(), ph.id)
	require.NoError(t, err)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked device stayed connected")
	}
	_, err = ph.connect(t)
	require.Error(t, err)
	// Nothing left to serve: the websocket port is unbound too.
	require.Eventually(t, func() bool { return !srv.pairing.ws.Running() && !dialable(srv.pairing.wsAddr) }, gateWait, 20*time.Millisecond)
}

func TestDeviceWebsocketRevokedByReplication(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	closed := make(chan struct{})
	nc, err := ph.connect(t, nats.ClosedHandler(func(*nats.Conn) { close(closed) }))
	require.NoError(t, err)
	defer nc.Close()

	d, ok := srv.pairing.reg.Get(ph.id)
	require.True(t, ok)
	now := time.Now().UTC()
	d.RevokedAt, d.Rev, d.UpdatedAt, d.UpdatedBy = &now, d.Rev+1, now, "peer"
	data, err := json.Marshal(d)
	require.NoError(t, err)
	srv.pairing.receive(&nats.Msg{Subject: subjDevices, Data: data})

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("device stayed connected after a replicated revocation")
	}
	_, err = ph.connect(t)
	require.Error(t, err)
	require.Eventually(t, func() bool { return !srv.pairing.ws.Running() && !dialable(srv.pairing.wsAddr) }, gateWait, 20*time.Millisecond)
}

func TestDeviceWebsocketRejectsBadPinAndUnknownKeys(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)

	// A wrong pin (a MITM's CA) fails the TLS handshake.
	bad := ph
	bad.caFP = pairing.RandomBytes(pairing.FingerprintSize)
	_, err := bad.connect(t)
	require.ErrorContains(t, err, "pin")
	// So does the right CA for another node.
	other := ph
	other.nodeID = "not-this-node"
	_, err = other.connect(t)
	require.Error(t, err)

	// An unpaired key is refused by the server.
	stranger := ph
	stranger.key = newDeviceKey(t)
	// The server refuses it; depending on timing the client sees the
	// authorization error or the connection it closed.
	_, err = stranger.connect(t)
	require.Error(t, err)

	// A paired key cannot use the mesh's loopback client port.
	kp, err := nkeys.FromRawSeed(nkeys.PrefixByteUser, ph.key.Seed())
	require.NoError(t, err)
	user, err := kp.PublicKey()
	require.NoError(t, err)
	_, err = nats.Connect(srv.mesh.Server().ClientURL(), nats.Nkey(user, kp.Sign), nats.NoReconnect())
	require.Error(t, err)
}

func TestDeviceWebsocketGatingAndConnLimits(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ws := srv.pairing.wsAddr
	require.Never(t, func() bool { return dialable(ws) }, 300*time.Millisecond, 20*time.Millisecond, "unbound without offers or devices")
	offer, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return dialable(ws) }, gateWait, 20*time.Millisecond)

	// One source cannot hold more than its share of connections.
	var conns []net.Conn
	defer func() {
		for _, cn := range conns {
			cn.Close()
		}
	}()
	for range maxWSConnsPerSource {
		cn, err := net.Dial("tcp", ws)
		require.NoError(t, err)
		conns = append(conns, cn)
	}
	require.Eventually(t, func() bool {
		cn, err := net.Dial("tcp", ws)
		if err != nil {
			return false
		}
		defer cn.Close()
		_ = cn.SetReadDeadline(time.Now().Add(time.Second))
		_, err = cn.Read(make([]byte, 1))
		return err != nil // Closed straight away.
	}, 5*time.Second, 50*time.Millisecond)

	_, err = c.PairReject(context.Background(), offer.OfferID)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return !dialable(ws) }, gateWait, 20*time.Millisecond)
}

func TestDeviceWebsocketIsolationAndPartialRevoke(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	alice, bob := pairPhone(t, srv, c), pairPhone(t, srv, c)

	errs := make(chan error, 4)
	aliceClosed := make(chan struct{})
	an, err := alice.connect(t,
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errs <- err }),
		nats.ClosedHandler(func(*nats.Conn) { close(aliceClosed) }))
	require.NoError(t, err)
	defer an.Close()
	bobClosed := make(chan struct{})
	bn, err := bob.connect(t, nats.ClosedHandler(func(*nats.Conn) { close(bobClosed) }))
	require.NoError(t, err)
	defer bn.Close()

	// Bob listens on his own subjects; Alice tries to write into them,
	// including as the reply subject of an allowed publish.
	got := make(chan string, 4)
	_, err = bn.Subscribe("crush.client.v1."+bob.id+".>", func(m *nats.Msg) { got <- m.Subject })
	require.NoError(t, err)
	require.NoError(t, bn.Flush())
	require.NoError(t, an.Publish("crush.client.v1."+bob.id+".api.x", []byte("forged")))
	require.NoError(t, an.Flush())
	select {
	case err := <-errs:
		require.ErrorContains(t, err, "Permissions Violation")
	case <-time.After(5 * time.Second):
		t.Fatal("expected a permissions violation")
	}
	select {
	case subj := <-got:
		t.Fatalf("bob received alice's message on %s", subj)
	case <-time.After(300 * time.Millisecond):
	}

	// Revoking Alice closes only her connection; Bob keeps the port open.
	_, err = c.RevokeDevice(t.Context(), alice.id)
	require.NoError(t, err)
	select {
	case <-aliceClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked device stayed connected")
	}
	require.NoError(t, bn.Publish("crush.client.v1."+bob.id+".still", nil))
	require.NoError(t, bn.Flush())
	select {
	case <-bobClosed:
		t.Fatal("other device was disconnected")
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("bob's connection stopped working")
	}
	require.True(t, srv.pairing.ws.Running())
}

func TestWSURLs(t *testing.T) {
	t.Parallel()
	eps := []cluster.Endpoint{
		{Kind: cluster.EndpointDirect, Addr: "127.0.0.1:7731"},
		{Kind: cluster.EndpointDirect, Addr: "192.168.1.5:7731"},
		{Kind: cluster.EndpointDirect, Addr: "laptop.example:7731"},
	}
	p := &pairingService{wsAddr: ":7734", ws: &tlsProxyListener{}}
	require.Equal(t, []string{"wss://192.168.1.5:7734", "wss://laptop.example:7734"}, p.wsURLs(eps))
	p.wsAddr = "10.0.0.5:9000"
	require.Equal(t, []string{"wss://10.0.0.5:9000"}, p.wsURLs(eps), "a bound host is the only one advertised")
	p.wsAddr = "127.0.0.1:9000"
	require.Empty(t, p.wsURLs(eps), "loopback binds are not reachable by phones")
	p.wsAddr = ":0"
	require.Empty(t, p.wsURLs(eps), "an ephemeral port is unknown until bound")
	p.wsAddr = ""
	require.Empty(t, p.wsURLs(eps))
}

func TestTLSProxyPerSourceLimit(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	l := &tlsProxyListener{perSource: 2, ln: ln, conns: map[net.Conn]struct{}{}, sources: map[string]int{}}
	a1, a2, a3, b1 := &net.TCPConn{}, &net.TCPConn{}, &net.TCPConn{}, &net.TCPConn{}
	require.True(t, l.track(ln, a1, "a"))
	require.True(t, l.track(ln, a2, "a"))
	require.False(t, l.track(ln, a3, "a"), "a source over its limit is refused")
	require.True(t, l.track(ln, b1, "b"), "other sources are unaffected")
	l.untrack(a1, "a")
	require.True(t, l.track(ln, a3, "a"))
	other, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer other.Close()
	require.False(t, l.track(other, &net.TCPConn{}, "c"), "a stale listener cannot register")
}
