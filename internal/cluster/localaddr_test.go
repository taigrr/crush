package cluster

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func ips(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

const (
	up    = net.FlagUp | net.FlagRunning
	bcast = up | net.FlagBroadcast | net.FlagMulticast
)

// grokbox is the reported case: a WireGuard utun with a 192.168 address
// alongside the real LAN on en0.
var grokbox = []NetInterface{
	{Name: "lo0", Flags: up | net.FlagLoopback, Addrs: ips("127.0.0.1", "::1")},
	{Name: "utun4", Flags: up | net.FlagPointToPoint, Addrs: ips("192.168.2.241")},
	{Name: "en0", Flags: bcast, Addrs: ips("fe80::1", "192.168.9.238", "2001:db8::5")},
	{Name: "utun0", Flags: up | net.FlagPointToPoint, Addrs: ips("fe80::abcd")},
}

func TestLocalEndpointsFrom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ifs  []NetInterface
		want []string
	}{
		{"grokbox prefers en0, keeps wireguard utun last", grokbox, []string{"192.168.9.238:7733", "[2001:db8::5]:7733", "192.168.2.241:7733"}},
		{"down interface skipped", []NetInterface{
			{Name: "en0", Flags: net.FlagBroadcast, Addrs: ips("192.168.1.2")},
			{Name: "en1", Flags: bcast, Addrs: ips("10.0.0.2")},
		}, []string{"10.0.0.2:7733"}},
		{"tunnels by name: wireguard, vpn, then bridges last", []NetInterface{
			{Name: "wg0", Flags: bcast, Addrs: ips("10.8.0.1")},
			{Name: "docker0", Flags: bcast, Addrs: ips("172.17.0.1")},
			{Name: "br-1234", Flags: bcast, Addrs: ips("172.18.0.1")},
			{Name: "bridge100", Flags: bcast, Addrs: ips("192.168.64.1")},
			{Name: "vmnet8", Flags: bcast, Addrs: ips("192.168.100.1")},
			{Name: "ipsec0", Flags: up, Addrs: ips("10.9.0.1")},
			{Name: "eth0", Flags: bcast, Addrs: ips("192.168.1.9")},
		}, []string{"192.168.1.9:7733", "10.8.0.1:7733", "10.9.0.1:7733", "172.18.0.1:7733", "192.168.64.1:7733", "172.17.0.1:7733", "192.168.100.1:7733"}},
		{"tailscale after wireguard", []NetInterface{
			{Name: "tailscale0", Flags: up | net.FlagPointToPoint, Addrs: ips("100.101.102.103", "fd7a:115c:a1e0::1")},
			{Name: "utun3", Flags: up | net.FlagPointToPoint, Addrs: ips("192.168.2.15")},
			{Name: "eth0", Flags: bcast, Addrs: ips("203.0.113.7")},
		}, []string{"203.0.113.7:7733", "192.168.2.15:7733", "100.101.102.103:7733", "[fd7a:115c:a1e0::1]:7733"}},
		{"private broadcast before private non-broadcast before public", []NetInterface{
			{Name: "en5", Flags: up, Addrs: ips("10.1.1.1")},
			{Name: "en2", Flags: bcast, Addrs: ips("198.51.100.4")},
			{Name: "en10", Flags: bcast, Addrs: ips("192.168.0.10")},
			{Name: "en1", Flags: bcast, Addrs: ips("192.168.0.20")},
		}, []string{"192.168.0.20:7733", "192.168.0.10:7733", "10.1.1.1:7733", "198.51.100.4:7733"}},
		{"duplicates once", []NetInterface{
			{Name: "en0", Flags: bcast, Addrs: ips("192.168.1.2", "192.168.1.2")},
		}, []string{"192.168.1.2:7733"}},
		{"only wireguard", []NetInterface{grokbox[0], grokbox[1]}, []string{"192.168.2.241:7733"}},
		{"nothing usable", []NetInterface{grokbox[0], grokbox[3], {Name: "en1", Flags: net.FlagBroadcast, Addrs: ips("10.0.0.1")}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, ep := range localEndpointsFrom(tt.ifs, 7733) {
				require.Equal(t, EndpointDirect, ep.Kind)
				got = append(got, ep.Addr)
			}
			require.Equal(t, tt.want, got)
			// Deterministic whatever the interface order.
			rev := append([]NetInterface(nil), tt.ifs...)
			for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
				rev[i], rev[j] = rev[j], rev[i]
			}
			var again []string
			for _, ep := range localEndpointsFrom(rev, 7733) {
				again = append(again, ep.Addr)
			}
			require.Equal(t, got, again)
		})
	}
}

func TestOrderAddrs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			"recorded wireguard address kept after LAN",
			[]string{"192.168.2.241:7735", "192.168.9.238:7735"},
			[]string{"192.168.9.238:7735", "192.168.2.241:7735"},
		},
		{
			"wireguard after foreign hosts, before tailnet",
			[]string{"192.168.2.241:1", "100.64.1.1:1", "box.example:1"},
			[]string{"box.example:1", "192.168.2.241:1", "100.64.1.1:1"},
		},
		{
			"hostnames and foreign addresses kept after local LAN",
			[]string{"laptop.example:7735", "203.0.113.9:7735", "[2001:db8::5]:7735", "192.168.9.238:7735"},
			[]string{"192.168.9.238:7735", "[2001:db8::5]:7735", "laptop.example:7735", "203.0.113.9:7735"},
		},
		{"foreign tailnet last", []string{"100.64.1.1:1", "10.0.0.9:1"}, []string{"10.0.0.9:1", "100.64.1.1:1"}},
		{"loopback dropped", []string{"127.0.0.1:1", "192.168.9.238:1"}, []string{"192.168.9.238:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, orderAddrs(grokbox, tt.in))
		})
	}
}

func TestClassifyInterfaceAddr(t *testing.T) {
	t.Parallel()
	p2p := up | net.FlagPointToPoint
	tests := []struct {
		name  string
		ifc   NetInterface
		ip    string
		kind  AddrKind
		prio  int
		valid bool
	}{
		{"lan broadcast", NetInterface{Name: "en0", Flags: bcast}, "192.168.1.5", KindLAN, PriorityLAN, true},
		{"lan non-broadcast", NetInterface{Name: "en5", Flags: up}, "10.1.1.1", KindLAN, PriorityLANOther, true},
		{"lan ula", NetInterface{Name: "eth0", Flags: bcast}, "fd12:3456::1", KindLAN, PriorityLANv6, true},
		{"public v4", NetInterface{Name: "eth0", Flags: bcast}, "203.0.113.7", KindPublic, PriorityPublic, true},
		{"public v6", NetInterface{Name: "en0", Flags: bcast}, "2001:db8::5", KindPublic, PriorityPublicV6, true},
		{"wireguard utun p2p", NetInterface{Name: "utun4", Flags: p2p}, "192.168.2.241", KindWireGuard, PriorityWireGuard, true},
		{"wireguard wg0", NetInterface{Name: "wg0", Flags: bcast}, "10.8.0.1", KindWireGuard, PriorityWireGuard, true},
		{"utun without p2p is vpn", NetInterface{Name: "utun2", Flags: up}, "10.7.0.2", KindVPN, PriorityVPN, true},
		{"openvpn tun", NetInterface{Name: "tun0", Flags: p2p}, "10.9.0.1", KindVPN, PriorityVPN, true},
		{"ipsec", NetInterface{Name: "ipsec0", Flags: up}, "10.9.0.1", KindVPN, PriorityVPN, true},
		{"tailscale range on utun", NetInterface{Name: "utun5", Flags: p2p}, "100.101.102.103", KindTailscale, PriorityTailscale, true},
		{"tailscale0 v6", NetInterface{Name: "tailscale0", Flags: p2p}, "fd7a:115c:a1e0::1", KindTailscale, PriorityTailscaleV6, true},
		{"tailscale v6 range on utun", NetInterface{Name: "utun6", Flags: p2p}, "fd7a:115c:a1e0::2", KindTailscale, PriorityTailscaleV6, true},
		{"docker0", NetInterface{Name: "docker0", Flags: bcast}, "172.17.0.1", KindBridge, PriorityBridge, true},
		{"default docker range anywhere", NetInterface{Name: "eth1", Flags: bcast}, "172.17.0.5", KindBridge, PriorityBridge, true},
		{"br-", NetInterface{Name: "br-1234", Flags: bcast}, "172.18.0.1", KindBridge, PriorityBridge, true},
		{"veth", NetInterface{Name: "veth9", Flags: bcast}, "10.200.0.1", KindBridge, PriorityBridge, true},
		{"vmnet", NetInterface{Name: "vmnet8", Flags: bcast}, "192.168.100.1", KindBridge, PriorityBridge, true},
		{"vboxnet", NetInterface{Name: "vboxnet0", Flags: bcast}, "192.168.56.1", KindBridge, PriorityBridge, true},
		{"bridge100", NetInterface{Name: "bridge100", Flags: bcast}, "192.168.64.1", KindBridge, PriorityBridge, true},
		{"loopback iface", NetInterface{Name: "lo0", Flags: up | net.FlagLoopback}, "127.0.0.1", "", 0, false},
		{"down", NetInterface{Name: "en1", Flags: net.FlagBroadcast}, "10.0.0.1", "", 0, false},
		{"link-local v4", NetInterface{Name: "en0", Flags: bcast}, "169.254.1.1", "", 0, false},
		{"link-local v6", NetInterface{Name: "en0", Flags: bcast}, "fe80::1", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			kind, prio, ok := ClassifyInterfaceAddr(tt.ifc, netip.MustParseAddr(tt.ip))
			require.Equal(t, tt.valid, ok)
			if ok {
				require.Equal(t, tt.kind, kind)
				require.Equal(t, tt.prio, prio)
			}
		})
	}
}

func TestClassifyAddrsRanking(t *testing.T) {
	t.Parallel()
	ifs := []NetInterface{
		{Name: "docker0", Flags: bcast, Addrs: ips("172.17.0.1")},
		{Name: "tailscale0", Flags: up | net.FlagPointToPoint, Addrs: ips("100.64.0.9")},
		{Name: "utun4", Flags: up | net.FlagPointToPoint, Addrs: ips("192.168.2.241")},
		{Name: "en0", Flags: bcast, Addrs: ips("192.168.9.238", "fe80::1")},
		{Name: "lo0", Flags: up | net.FlagLoopback, Addrs: ips("127.0.0.1")},
	}
	got := ClassifyAddrs(ifs, []string{
		"172.17.0.1:1", "100.64.0.9:1", "192.168.2.241:1", "192.168.9.238:1", "127.0.0.1:1", "[fe80::1]:1",
		"box.lan:1", "10.0.0.7:1", "192.168.9.238:1", "nonsense",
	})
	require.Equal(t, []AdvertisedEndpoint{
		{Addr: "192.168.9.238:1", Kind: KindLAN, Priority: PriorityLAN},
		{Addr: "10.0.0.7:1", Kind: KindLAN, Priority: PriorityLANForeign},
		{Addr: "box.lan:1", Kind: KindPublic, Priority: PriorityHostname},
		{Addr: "192.168.2.241:1", Kind: KindWireGuard, Priority: PriorityWireGuard},
		{Addr: "100.64.0.9:1", Kind: KindTailscale, Priority: PriorityTailscale},
		{Addr: "172.17.0.1:1", Kind: KindBridge, Priority: PriorityBridge},
	}, got)

	// Another node's addresses are classified by range alone.
	got = ClassifyAddrs(nil, []string{"172.17.0.3:1", "100.100.1.1:1", "[fd7a:115c:a1e0::5]:1", "192.168.1.4:1", "198.51.100.1:1"})
	require.Equal(t, []AdvertisedEndpoint{
		{Addr: "192.168.1.4:1", Kind: KindLAN, Priority: PriorityLANForeign},
		{Addr: "198.51.100.1:1", Kind: KindPublic, Priority: PriorityHostname},
		{Addr: "100.100.1.1:1", Kind: KindTailscale, Priority: PriorityTailscale},
		{Addr: "[fd7a:115c:a1e0::5]:1", Kind: KindTailscale, Priority: PriorityTailscale},
		{Addr: "172.17.0.3:1", Kind: KindBridge, Priority: PriorityBridgeForeign},
	}, got)
}
