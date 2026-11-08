package cluster

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemberDialCandidates(t *testing.T) {
	t.Parallel()
	device := &DeviceEndpoints{
		V: DeviceEndpointsVersion,
		Client: DeviceListener{State: DeviceListenerOn, Endpoints: []AdvertisedEndpoint{
			{Addr: "10.20.0.5:7733", Kind: KindLAN, Priority: PriorityLAN},
			{Addr: "172.31.9.2:7733", Kind: KindWireGuard, Priority: PriorityWireGuard},
			{Addr: "172.17.0.1:7733", Kind: KindBridge, Priority: PriorityBridge},
		}},
		WS: DeviceListener{State: DeviceListenerOn, Endpoints: []AdvertisedEndpoint{
			{Addr: "10.20.0.5:7734", Kind: KindLAN, Priority: PriorityLAN},
			{Addr: "100.101.1.2:7734", Kind: KindTailscale, Priority: PriorityTailscale},
		}},
	}
	advertised := []Endpoint{{Kind: EndpointDirect, Addr: "172.31.9.2:7731"}, {Kind: EndpointNATS, Addr: "172.31.9.2:7732"}}
	tests := []struct {
		name string
		m    Member
		want []dialCandidate
	}{
		{"no device record: advertised only", Member{Endpoints: advertised}, []dialCandidate{{addr: "172.31.9.2:7731"}}},
		{"every transport but bridges, advertised first", Member{Endpoints: advertised, Device: device}, []dialCandidate{
			{addr: "172.31.9.2:7731"},
			{addr: "10.20.0.5:7731"},
			{addr: "100.101.1.2:7731"},
		}},
		{"no direct endpoints", Member{Device: device}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, memberDialCandidates(tt.m))
		})
	}
}

// slowProxy forwards to target, delaying every chunk by delay so each
// round trip through it is slower, standing in for a slower transport.
func slowProxy(t *testing.T, target string, delay time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	pipe := func(dst, src net.Conn) {
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				time.Sleep(delay)
				if _, werr := dst.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = dst.Close()
				return
			}
		}
	}
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp", target)
			if err != nil {
				_ = in.Close()
				continue
			}
			go pipe(out, in)
			go pipe(in, out)
		}
	}()
	return ln.Addr().String()
}

func TestResolveEndpointPicksFastest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a := newTestNode(t, "v1.0.0")
	b := newTestNode(t, "v1.0.0")
	c := newTestNode(t, "v1.0.0")
	aAddr := freeAddr(t)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))
	joinAndApprove(t, ctx, b, a, token)
	joinAndApprove(t, ctx, c, a, token)

	bm, ok := mustSnapshot(t, a).Member(b.Identity().ID)
	require.True(t, ok)
	direct := bm.Endpoints[0].Addr
	slow := slowProxy(t, direct, 40*time.Millisecond)

	// Whatever order or kind, the faster path wins.
	for _, order := range [][]dialCandidate{{{addr: slow}, {addr: direct}}, {{addr: direct}, {addr: slow}}} {
		a.forgetEndpoint(bm.NodeID)
		got, err := a.probeEndpoints(ctx, bm, order)
		require.NoError(t, err)
		require.Equal(t, direct, got)
	}

	// A working endpoint is kept over an equally fast one.
	a.endpoints.set(bm.NodeID, endpointEntry{addr: slow, measured: time.Now()})
	got, err := a.probeEndpoints(ctx, bm, []dialCandidate{{addr: slowProxy(t, direct, 40*time.Millisecond)}, {addr: slow}})
	require.NoError(t, err)
	require.Equal(t, slow, got)

	// Every candidate must prove it is the member: an address reaching
	// another node (B, while dialing C) is rejected.
	cm, ok := mustSnapshot(t, a).Member(c.Identity().ID)
	require.True(t, ok)
	_, err = a.probeEndpoint(ctx, cm, dialCandidate{addr: direct})
	require.ErrorIs(t, err, errWrongPeer)
	a.forgetEndpoint(cm.NodeID)
	got, err = a.probeEndpoints(ctx, cm, []dialCandidate{{addr: direct}, {addr: cm.Endpoints[0].Addr}})
	require.NoError(t, err)
	require.Equal(t, cm.Endpoints[0].Addr, got)

	// A candidate that never answers (accepts TCP, never speaks TLS)
	// delays a working one by the grace, not the dial timeout.
	hang, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = hang.Close() })
	go func() {
		for {
			conn, err := hang.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	a.forgetEndpoint(bm.NodeID)
	start := time.Now()
	got, err = a.probeEndpoints(ctx, bm, []dialCandidate{{addr: hang.Addr().String()}, {addr: direct}})
	require.NoError(t, err)
	require.Equal(t, direct, got)
	require.Less(t, time.Since(start), peerDialTimeout)
}

func TestClearlyFaster(t *testing.T) {
	t.Parallel()
	ms := time.Millisecond
	require.True(t, clearlyFaster(1*ms, 40*ms))
	require.False(t, clearlyFaster(35*ms, 40*ms), "within the ratio")
	require.False(t, clearlyFaster(500*time.Microsecond, 2*ms), "within the margin")
	require.False(t, clearlyFaster(40*ms, 40*ms))
}

func TestResolveEndpointReprobesInBackground(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a := newTestNode(t, "v1.0.0")
	b := newTestNode(t, "v1.0.0")
	aAddr := freeAddr(t)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))
	joinAndApprove(t, ctx, b, a, token)
	bm, ok := mustSnapshot(t, a).Member(b.Identity().ID)
	require.True(t, ok)
	direct := bm.Endpoints[0].Addr

	// Joined over a slow tunnel; a faster path to the same node becomes
	// reachable later (back on the home network).
	slow := slowProxy(t, direct, 40*time.Millisecond)
	candidates := []dialCandidate{{addr: slow}, {addr: direct}}
	fresh := endpointEntry{addr: slow, measured: time.Now(), key: candidatesKey(candidates)}
	a.endpoints.set(bm.NodeID, fresh)

	// Recently measured over the same candidates: nothing to do.
	a.maybeReprobe(bm, fresh, candidates)
	time.Sleep(100 * time.Millisecond)
	e, _ := a.endpoints.get(bm.NodeID)
	require.Equal(t, slow, e.addr)

	// Once due, the cached endpoint keeps serving while the candidates are
	// re-measured in the background, and the faster one takes over.
	stale := fresh
	stale.measured = time.Now().Add(-2 * peerReprobeInterval)
	a.endpoints.set(bm.NodeID, stale)
	a.maybeReprobe(bm, stale, candidates)
	require.Eventually(t, func() bool {
		e, ok := a.endpoints.get(bm.NodeID)
		return ok && e.addr == direct
	}, 5*time.Second, 10*time.Millisecond)

	// A changed candidate set is due right away.
	require.True(t, fresh.reprobeDue(candidatesKey([]dialCandidate{{addr: slow}}), time.Now()))
	require.False(t, fresh.reprobeDue(fresh.key, time.Now()))
}

func TestPeerHTTPClientSharedPerTrustGeneration(t *testing.T) {
	t.Parallel()
	a := newTestNode(t, "v1.0.0")
	_, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: freeAddr(t)}})
	require.NoError(t, err)
	first := a.httpClient()
	require.Same(t, first, a.httpClient(), "calls share one keep-alive client")
	_, _, err = a.RotateKey(t.Context())
	require.NoError(t, err)
	require.NotSame(t, first, a.httpClient(), "a key rotation rebuilds the client")
}
