package server

import (
	"crypto/ed25519"
	"encoding/json"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
)

// A phone on the home LAN must be offered the host's LAN address even
// when the recorded cluster endpoints are only WireGuard addresses.
func TestSelfDeviceAdvertisesLANInterfaces(t *testing.T) {
	t.Parallel()
	ifs := []cluster.NetInterface{
		{Name: "lo0", Flags: net.FlagUp | net.FlagLoopback, Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{Name: "en0", Flags: net.FlagUp | net.FlagBroadcast, Addrs: []netip.Addr{netip.MustParseAddr("192.168.9.238"), netip.MustParseAddr("fe80::1")}},
		{Name: "utun4", Flags: net.FlagUp | net.FlagPointToPoint, Addrs: []netip.Addr{netip.MustParseAddr("192.168.2.241")}},
	}
	p := &pairingService{
		clientAddr: ":7733", wsAddr: ":7734",
		client:     &httpListener{bound: "[::]:7733", last: "[::]:7733"},
		ws:         &tlsProxyListener{bound: "[::]:7734", last: "[::]:7734"},
		interfaces: func() ([]cluster.NetInterface, error) { return ifs, nil },
	}
	eps := []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: "192.168.2.241:7731"}}
	clients, urls := p.selfDevice(eps)
	require.Equal(t, []pairing.Endpoint{
		{Kind: pairing.EndpointLAN, Addr: "192.168.9.238:7733", Class: "lan", Priority: cluster.PriorityLAN},
		{Kind: pairing.EndpointWireGuard, Addr: "192.168.2.241:7733", Class: "wireguard", Priority: cluster.PriorityWireGuard},
	}, clients)
	require.Equal(t, []string{"wss://192.168.9.238:7734", "wss://192.168.2.241:7734"}, urls)

	rec := p.deviceRecordFor(eps)
	require.Equal(t, []string{"192.168.9.238:7734", "192.168.2.241:7734"}, rec.WS.Addrs, "the gossiped record carries LAN addresses too")
}

func TestOverrideEndpointsWin(t *testing.T) {
	t.Parallel()
	var addrs []string
	for _, h := range []string{"box.example", "100.64.0.5:9000", "127.0.0.1", "192.168.9.238"} {
		addr, err := pinnedAddr(h, "7733")
		require.NoError(t, err)
		addrs = append(addrs, addr)
	}
	_, key, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	self := pairing.Self{NodeKey: key}
	require.NoError(t, pairing.WithEndpoints(overrideEndpoints(addrs))(&self))
	require.Equal(t, []pairing.Endpoint{
		{Kind: pairing.EndpointDirect, Addr: "box.example:7733", Class: "override", Priority: 0},
		{Kind: pairing.EndpointTailscale, Addr: "100.64.0.5:9000", Class: "override", Priority: 0},
		{Kind: pairing.EndpointLAN, Addr: "192.168.9.238:7733", Class: "override", Priority: 0},
	}, self.Endpoints, "overrides keep their order, loopback is dropped")
	require.Equal(t, []pairing.InfoEndpoint{
		{Kind: "override", Addr: "box.example:7733"},
		{Kind: "override", Addr: "100.64.0.5:9000"},
		{Kind: "override", Addr: "192.168.9.238:7733"},
	}, self.Info().Endpoints)
}

func TestDeviceListenersOffAdvertiseNothing(t *testing.T) {
	t.Parallel()
	ifs := []cluster.NetInterface{
		{Name: "en0", Flags: net.FlagUp | net.FlagBroadcast, Addrs: []netip.Addr{netip.MustParseAddr("192.168.9.238")}},
	}
	p := &pairingService{
		client:     &httpListener{},
		ws:         &tlsProxyListener{},
		interfaces: func() ([]cluster.NetInterface, error) { return ifs, nil },
	}
	rec := p.deviceRecordFor(nil)
	require.Equal(t, cluster.DeviceListener{State: cluster.DeviceListenerOff}, rec.Client)
	require.Equal(t, cluster.DeviceListener{State: cluster.DeviceListenerOff}, rec.WS)
	clients, ws := p.selfDeviceEndpoints(nil)
	require.Empty(t, clients)
	require.Empty(t, ws)
	other := cluster.Member{Device: &cluster.DeviceEndpoints{V: cluster.DeviceEndpointsVersion, Client: rec.Client, WS: rec.WS}}
	clients, ws, src := p.memberDevice(other, nil)
	require.Empty(t, clients)
	require.Empty(t, ws)
	require.Equal(t, devicePortsAdvertised, src)
}

func TestMemberDeviceUsesAdvertisedClassification(t *testing.T) {
	t.Parallel()
	p := &pairingService{interfaces: func() ([]cluster.NetInterface, error) { return nil, nil }}
	m := cluster.Member{Device: &cluster.DeviceEndpoints{
		V: cluster.DeviceEndpointsVersion,
		WS: cluster.DeviceListener{
			State: cluster.DeviceListenerOn,
			Addrs: []string{"10.0.0.2:7734", "192.168.2.9:7734"},
			Endpoints: []cluster.AdvertisedEndpoint{
				{Addr: "10.0.0.2:7734", Kind: cluster.KindLAN, Priority: cluster.PriorityLAN},
				{Addr: "192.168.2.9:7734", Kind: cluster.KindWireGuard, Priority: cluster.PriorityWireGuard},
			},
		},
		// An older member: addresses only, classified by range here.
		Client: cluster.DeviceListener{State: cluster.DeviceListenerOn, Addrs: []string{"100.64.0.2:7733", "192.168.2.9:7733"}},
	}}
	clients, ws, _ := p.memberDevice(m, nil)
	require.Equal(t, []pairing.NATSEndpoint{
		{URL: "wss://10.0.0.2:7734", Kind: "lan", Priority: cluster.PriorityLAN},
		{URL: "wss://192.168.2.9:7734", Kind: "wireguard", Priority: cluster.PriorityWireGuard},
	}, natsEndpoints(ws))
	require.Equal(t, []string{"192.168.2.9:7733", "100.64.0.2:7733"}, endpointAddrs(clients))
	require.Equal(t, []string{"lan", "tailscale"}, []string{clients[0].JSONKind(), clients[1].JSONKind()})
}

// Older rush builds read endpoints[].{kind,addr} and nats_urls as plain
// strings; the discovery fields are additive.
func TestMemberInfoPayloadBackwardCompatible(t *testing.T) {
	t.Parallel()
	ws := []pairing.Endpoint{{Kind: pairing.EndpointLAN, Addr: "192.168.9.238:7734", Class: "lan", Priority: cluster.PriorityLAN}}
	info := pairing.MemberInfo{
		NodeID: "n", Name: "box",
		Endpoints:     []pairing.InfoEndpoint{{Kind: "lan", Addr: "192.168.9.238:7733", Priority: cluster.PriorityLAN}},
		NATSURLs:      wssURLs(ws),
		NATSEndpoints: natsEndpoints(ws),
	}
	raw, err := json.Marshal(info)
	require.NoError(t, err)
	var old struct {
		Endpoints []struct {
			Kind string `json:"kind"`
			Addr string `json:"addr"`
		} `json:"endpoints"`
		NATSURLs []string `json:"nats_urls"`
	}
	require.NoError(t, json.Unmarshal(raw, &old))
	require.Equal(t, "192.168.9.238:7733", old.Endpoints[0].Addr)
	require.Equal(t, []string{"wss://192.168.9.238:7734"}, old.NATSURLs)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	require.Equal(t, []any{map[string]any{"url": "wss://192.168.9.238:7734", "kind": "lan", "priority": float64(cluster.PriorityLAN)}}, generic["nats_endpoints"])
	require.Equal(t, []any{map[string]any{"addr": "192.168.9.238:7733", "kind": "lan", "priority": float64(cluster.PriorityLAN)}}, generic["endpoints"])
}
