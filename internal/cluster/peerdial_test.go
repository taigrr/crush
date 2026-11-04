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
	lanDevice := &DeviceEndpoints{
		V: DeviceEndpointsVersion,
		Client: DeviceListener{State: DeviceListenerOn, Endpoints: []AdvertisedEndpoint{
			{Addr: "192.168.9.238:7733", Kind: KindLAN, Priority: PriorityLAN},
			{Addr: "192.168.2.241:7733", Kind: KindWireGuard, Priority: PriorityWireGuard},
		}},
		WS: DeviceListener{State: DeviceListenerOn, Endpoints: []AdvertisedEndpoint{
			{Addr: "192.168.9.238:7734", Kind: KindLAN, Priority: PriorityLAN},
			{Addr: "192.168.2.241:7734", Kind: KindWireGuard, Priority: PriorityWireGuard},
		}},
	}
	wg := []Endpoint{{Kind: EndpointDirect, Addr: "192.168.2.241:7731"}, {Kind: EndpointNATS, Addr: "192.168.2.241:7732"}}
	tests := []struct {
		name string
		m    Member
		want []dialCandidate
	}{
		{"no device record: advertised only", Member{Endpoints: wg}, []dialCandidate{{addr: "192.168.2.241:7731"}}},
		{"LAN from the device record first, verified", Member{Endpoints: wg, Device: lanDevice}, []dialCandidate{
			{addr: "192.168.9.238:7731", verify: true},
			{addr: "192.168.2.241:7731"},
		}},
		{"advertised LAN endpoint moves first, unverified", Member{
			Endpoints: []Endpoint{{Kind: EndpointDirect, Addr: "192.168.2.241:7731"}, {Kind: EndpointDirect, Addr: "192.168.9.238:7731"}},
			Device:    lanDevice,
		}, []dialCandidate{{addr: "192.168.9.238:7731"}, {addr: "192.168.2.241:7731"}}},
		{"no direct endpoints", Member{Device: lanDevice}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, memberDialCandidates(tt.m))
		})
	}
}

func TestResolveEndpointPrefersVerifiedLAN(t *testing.T) {
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
	_, bPort, err := net.SplitHostPort(bm.Endpoints[0].Addr)
	require.NoError(t, err)
	lan := &DeviceEndpoints{V: DeviceEndpointsVersion, Client: DeviceListener{State: DeviceListenerOn, Endpoints: []AdvertisedEndpoint{
		{Addr: "127.0.0.1:7733", Kind: KindLAN, Priority: PriorityLAN},
	}}}

	// B advertises only "localhost" (standing in for its tunnel address);
	// its device record names 127.0.0.1 as LAN, which is preferred.
	viaTunnel := bm
	viaTunnel.Endpoints = []Endpoint{{Kind: EndpointDirect, Addr: net.JoinHostPort("localhost", bPort)}}
	viaTunnel.Device = lan
	got, err := a.probeEndpoints(ctx, viaTunnel, memberDialCandidates(viaTunnel))
	require.NoError(t, err)
	require.Equal(t, net.JoinHostPort("127.0.0.1", bPort), got)

	// A derived LAN address answering as another node (B, while dialing
	// C) is rejected and the advertised endpoint used instead.
	cm, ok := mustSnapshot(t, a).Member(c.Identity().ID)
	require.True(t, ok)
	cm.Endpoints = []Endpoint{{Kind: EndpointDirect, Addr: net.JoinHostPort("localhost", bPort)}}
	cm.Device = lan
	require.ErrorIs(t, a.probeEndpoint(ctx, cm, dialCandidate{addr: net.JoinHostPort("127.0.0.1", bPort), verify: true}), errWrongPeer)
	got, err = a.probeEndpoints(ctx, cm, memberDialCandidates(cm))
	require.NoError(t, err)
	require.Equal(t, net.JoinHostPort("localhost", bPort), got)
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
