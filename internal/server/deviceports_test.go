package server

import (
	"context"
	"crypto/ed25519"
	"net"
	"strings"
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

// testHostDialer maps the fake *.phone.test hosts to loopback.
type testHostDialer struct{}

func (testHostDialer) Dial(network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(host, ".phone.test") {
		host = "127.0.0.1"
	}
	return net.DialTimeout(network, net.JoinHostPort(host, port), 3*time.Second)
}

func portString(t *testing.T, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	return port
}

// pairServersWithHosts forms a two-node cluster where each node also
// advertises a fake phone-reachable host.
func pairServersWithHosts(t *testing.T, ctx context.Context, a, b *Server, ac, bc *client.Client, aHost, bHost string) {
	t.Helper()
	token, err := ac.ClusterInit(ctx, proto.ClusterInitRequest{Endpoints: []cluster.Endpoint{
		{Kind: cluster.EndpointDirect, Addr: a.clusterAddr},
		{Kind: cluster.EndpointDirect, Addr: net.JoinHostPort(aHost, portString(t, a.clusterAddr))},
	}})
	require.NoError(t, err)
	go func() {
		for ctx.Err() == nil {
			if st, err := ac.ClusterStatus(ctx); err == nil && len(st.Pending) == 1 {
				_, _ = ac.ClusterApprove(ctx, st.Pending[0].ID)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	_, err = bc.ClusterJoin(ctx, proto.ClusterJoinRequest{
		Addr: a.clusterAddr, Token: token, TimeoutSeconds: 20,
		Endpoints: []cluster.Endpoint{
			{Kind: cluster.EndpointDirect, Addr: b.clusterAddr},
			{Kind: cluster.EndpointDirect, Addr: net.JoinHostPort(bHost, portString(t, b.clusterAddr))},
		},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return a.mesh != nil && b.mesh != nil && a.mesh.NumRoutes() > 0 && b.mesh.NumRoutes() > 0
	}, 15*time.Second, 50*time.Millisecond)
}

func memberInfo(t *testing.T, srv *Server, nodeID string) pairing.MemberInfo {
	t.Helper()
	for _, m := range srv.pairing.extras().Members {
		if m.NodeID == nodeID {
			return m
		}
	}
	t.Fatalf("member %s not in extras", nodeID)
	return pairing.MemberInfo{}
}

func storedDevice(srv *Server, nodeID string) *cluster.DeviceEndpoints {
	st, err := srv.cluster.Store().Snapshot()
	if err != nil {
		return nil
	}
	m, _ := st.Member(nodeID)
	return m.Device
}

// TestMemberDevicePortsTwoNodes: pairing on A hands the phone B's own
// bound device ports (not A's), B re-advertises as its gated listeners
// bind, unbind, and rebind, and the phone reaches B's websocket with the
// URL A gave it. In-process nodes on random loopback ports only.
func TestMemberDevicePortsTwoNodes(t *testing.T) {
	isolateHome(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	a, ac := newClusterTestServerListeners(t, "", ":"+portString(t, freeTCPAddr(t)))
	bClient, bWS := ":"+portString(t, freeTCPAddr(t)), ":"+portString(t, freeTCPAddr(t))
	b, bc := newClusterTestServerListeners(t, bClient, bWS)
	pairServersWithHosts(t, ctx, a, b, ac, bc, "a.phone.test", "b.phone.test")
	bID := b.cluster.Identity().ID

	// B advertises at start: enabled, never bound yet, so no addresses
	// (its configured ports are never advertised).
	require.Eventually(t, func() bool { return storedDevice(a, bID) != nil }, 15*time.Second, 20*time.Millisecond)
	rec := storedDevice(a, bID)
	require.Equal(t, cluster.DeviceEndpointsVersion, rec.V)
	require.Equal(t, cluster.DeviceListenerIdle, rec.WS.State)
	require.Empty(t, rec.WS.Addrs)
	info := memberInfo(t, a, bID)
	require.Equal(t, devicePortsAdvertised, info.DevicePorts)
	require.Empty(t, info.NATSURLs, "nothing is synthesized from A's own ports")

	// Pairing a phone on A makes B's gate bind; B advertises the bound
	// ports, and A hands them out for B.
	key := newDeviceKey(t)
	poll := pairFlowA(t, a, ac, key)
	wantWS := "wss://b.phone.test:" + portString(t, bWS)
	require.Eventually(t, func() bool {
		info = memberInfo(t, a, bID)
		return len(info.NATSURLs) == 1 && info.NATSURLs[0] == wantWS
	}, 15*time.Second, 20*time.Millisecond, "A never learned B's websocket")
	require.Equal(t, []pairing.InfoEndpoint{{Kind: pairing.EndpointDirect.String(), Addr: "b.phone.test:" + portString(t, bClient)}}, info.Endpoints)
	require.Equal(t, cluster.DeviceListenerOn, storedDevice(a, bID).WS.State)
	require.Equal(t, portString(t, b.pairing.ws.Addr()), portString(t, wantWS[len("wss://"):]), "the advertised port is the bound one")
	aInfo := memberInfo(t, a, a.cluster.Identity().ID)
	require.Equal(t, []string{"wss://a.phone.test:" + portString(t, a.pairing.wsAddr)}, aInfo.NATSURLs, "A's own entry uses A's port")

	// The phone reaches B's websocket at the URL A gave it.
	id := poll.Device.DeviceID
	require.Eventually(t, func() bool { _, ok := b.pairing.reg.Get(id); return ok }, 15*time.Second, 50*time.Millisecond)
	kp, err := nkeys.FromRawSeed(nkeys.PrefixByteUser, key.Seed())
	require.NoError(t, err)
	user, err := kp.PublicKey()
	require.NoError(t, err)
	tlsCfg, err := pairing.TrustedTLSConfig(toBytes(poll.CACerts), toBytes(poll.CAFPs), bID)
	require.NoError(t, err)
	var nc *nats.Conn
	require.Eventually(t, func() bool {
		nc, err = nats.Connect(info.NATSURLs[0], nats.SetCustomDialer(testHostDialer{}), nats.Secure(tlsCfg),
			nats.Nkey(user, kp.Sign), nats.NoReconnect(), nats.Timeout(3*time.Second), nats.CustomInboxPrefix("_INBOX."+id))
		return err == nil
	}, 15*time.Second, 100*time.Millisecond, "phone could not connect to B")
	nc.Close()

	// Revoking the only device closes B's gated listeners; B re-advertises
	// them as idle (keeping the last bound addresses).
	_, err = ac.RevokeDevice(ctx, id)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		d := storedDevice(a, bID)
		return d != nil && d.WS.State == cluster.DeviceListenerIdle && d.Client.State == cluster.DeviceListenerIdle
	}, 15*time.Second, 20*time.Millisecond, "B's unbind was not re-advertised")
	require.Contains(t, storedDevice(a, bID).WS.Addrs, "b.phone.test:"+portString(t, bWS))

	// An offer on B binds them again.
	_, err = bc.PairCreateOffer(ctx, pairing.CreateOfferRequest{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		d := storedDevice(a, bID)
		return d != nil && d.WS.State == cluster.DeviceListenerOn
	}, 15*time.Second, 20*time.Millisecond, "B's rebind was not re-advertised")
}

func toBytes(in []pairing.Bytes) [][]byte {
	out := make([][]byte, 0, len(in))
	for _, b := range in {
		out = append(out, b)
	}
	return out
}

// TestMemberDevicePortsOffAndLegacy: a member that advertises its
// listeners off gives phones nothing, one that predates advertising gets
// this node's ports on its hosts, and an unknown record version gets
// nothing.
func TestMemberDevicePortsOffAndLegacy(t *testing.T) {
	isolateHome(t)
	srv, _ := initPhoneNode(t)
	wsPort := portString(t, srv.pairing.wsAddr)
	err := srv.cluster.Store().Update(func(st *cluster.State) error {
		direct := func(h string) []cluster.Endpoint {
			return []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: net.JoinHostPort(h, "7731")}}
		}
		pub := func() []byte { p, _, _ := ed25519.GenerateKey(nil); return p }
		st.Members = append(st.Members,
			cluster.Member{NodeID: "legacy", Name: "legacy", PublicKey: pub(), Endpoints: direct("10.9.0.3")},
			cluster.Member{NodeID: "off", Name: "off", PublicKey: pub(), Endpoints: direct("10.9.0.4"), Device: &cluster.DeviceEndpoints{
				V: 1, Seq: 1, Client: cluster.DeviceListener{State: cluster.DeviceListenerOff}, WS: cluster.DeviceListener{State: cluster.DeviceListenerOff},
			}},
			cluster.Member{NodeID: "ws-only", Name: "ws-only", PublicKey: pub(), Endpoints: direct("10.9.0.5"), Device: &cluster.DeviceEndpoints{
				V: 1, Seq: 1, Client: cluster.DeviceListener{State: cluster.DeviceListenerOff},
				WS: cluster.DeviceListener{State: cluster.DeviceListenerOn, Addrs: []string{"10.9.0.5:9734", "127.0.0.1:9734"}},
			}},
			cluster.Member{NodeID: "future", Name: "future", PublicKey: pub(), Endpoints: direct("10.9.0.6"), Device: &cluster.DeviceEndpoints{
				V: 2, Seq: 1, WS: cluster.DeviceListener{State: cluster.DeviceListenerOn, Addrs: []string{"10.9.0.6:9734"}},
			}},
		)
		return nil
	})
	require.NoError(t, err)

	legacy := memberInfo(t, srv, "legacy")
	require.Equal(t, devicePortsLegacy, legacy.DevicePorts)
	require.Equal(t, []string{"wss://10.9.0.3:" + wsPort}, legacy.NATSURLs, "legacy members get this node's port on their hosts")

	off := memberInfo(t, srv, "off")
	require.Equal(t, devicePortsAdvertised, off.DevicePorts)
	require.Empty(t, off.NATSURLs, "an advertised off listener gives no URL")
	require.Empty(t, off.Endpoints)

	wsOnly := memberInfo(t, srv, "ws-only")
	require.Equal(t, []string{"wss://10.9.0.5:9734"}, wsOnly.NATSURLs, "loopback addresses are dropped")
	require.Empty(t, wsOnly.Endpoints)

	future := memberInfo(t, srv, "future")
	require.Equal(t, devicePortsUnknown, future.DevicePorts)
	require.Empty(t, future.NATSURLs)
	require.Empty(t, future.Endpoints)
}

// TestDeviceRecordUsesBoundAddress: the record carries the listener's
// bound address, so an ephemeral (:0) port is advertised correctly, and
// a specific host bind is advertised for that host only.
func TestDeviceRecordUsesBoundAddress(t *testing.T) {
	t.Parallel()
	eps := []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: "10.0.0.2:7731"}, {Kind: cluster.EndpointDirect, Addr: "box.ts.net:7731"}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p := &pairingService{clientAddr: "127.0.0.1:0", wsAddr: ":0", client: &httpListener{bound: ln.Addr().String(), last: ln.Addr().String()}, ws: &tlsProxyListener{bound: "[::]:41999", last: "[::]:41999"}}
	rec := p.deviceRecordFor(eps)
	require.Equal(t, cluster.DeviceListener{State: cluster.DeviceListenerOn, Addrs: []string{"127.0.0.1:" + port}}, rec.Client)
	require.Equal(t, cluster.DeviceListener{State: cluster.DeviceListenerOn, Addrs: []string{"10.0.0.2:41999", "box.ts.net:41999"}}, rec.WS)

	p.ws = &tlsProxyListener{last: "[::]:41999"}
	require.Equal(t, cluster.DeviceListenerIdle, p.deviceRecordFor(eps).WS.State)
	p.ws = &tlsProxyListener{}
	require.Equal(t, cluster.DeviceListener{State: cluster.DeviceListenerIdle}, p.deviceRecordFor(eps).WS, "never bound: no addresses")
	p.wsAddr = ""
	require.Equal(t, cluster.DeviceListener{State: cluster.DeviceListenerOff}, p.deviceRecordFor(eps).WS)
}
