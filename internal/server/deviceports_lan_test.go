package server

import (
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
		{Kind: pairing.EndpointLAN, Addr: "192.168.9.238:7733"},
		{Kind: pairing.EndpointLAN, Addr: "192.168.2.241:7733"},
	}, clients)
	require.Equal(t, []string{"wss://192.168.9.238:7734", "wss://192.168.2.241:7734"}, urls)

	rec := p.deviceRecordFor(eps)
	require.Equal(t, []string{"192.168.9.238:7734", "192.168.2.241:7734"}, rec.WS.Addrs, "the gossiped record carries LAN addresses too")
}
