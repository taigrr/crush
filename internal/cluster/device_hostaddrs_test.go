package cluster

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func fakeHostInterfaces() []NetInterface {
	up := net.FlagUp | net.FlagBroadcast
	return []NetInterface{
		{Name: "lo0", Flags: net.FlagUp | net.FlagLoopback, Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}},
		{Name: "en0", Flags: up, Addrs: []netip.Addr{netip.MustParseAddr("192.168.9.238"), netip.MustParseAddr("fe80::1")}},
		{Name: "utun4", Flags: net.FlagUp | net.FlagPointToPoint, Addrs: []netip.Addr{netip.MustParseAddr("192.168.2.241")}},
		{Name: "utun5", Flags: net.FlagUp | net.FlagPointToPoint, Addrs: []netip.Addr{netip.MustParseAddr("100.101.102.103")}},
	}
}

func TestHostAdvertisedAddrsWildcardAddsInterfaces(t *testing.T) {
	t.Parallel()
	eps := []Endpoint{{Kind: EndpointDirect, Addr: "192.168.2.241:7735"}}
	for _, bound := range []string{"[::]:7733", "0.0.0.0:7733", ":7733"} {
		got := HostAdvertisedAddrs(fakeHostInterfaces(), bound, eps)
		require.Equal(t, []string{"192.168.9.238:7733", "192.168.2.241:7733", "100.101.102.103:7733"}, got, bound)
	}
}

func TestHostAdvertisedAddrsKeepsForeignRecorded(t *testing.T) {
	t.Parallel()
	eps := []Endpoint{{Kind: EndpointDirect, Addr: "203.0.113.5:7735"}, {Kind: EndpointDirect, Addr: "192.168.9.238:7735"}}
	got := HostAdvertisedAddrs(fakeHostInterfaces(), "[::]:7734", eps)
	require.Equal(t, []string{"192.168.9.238:7734", "203.0.113.5:7734", "192.168.2.241:7734", "100.101.102.103:7734"}, got)
}

func TestHostAdvertisedAddrsSpecificBind(t *testing.T) {
	t.Parallel()
	got := HostAdvertisedAddrs(fakeHostInterfaces(), "192.168.2.241:7733", nil)
	require.Equal(t, []string{"192.168.2.241:7733"}, got)
	require.Nil(t, HostAdvertisedAddrs(fakeHostInterfaces(), "[::]:0", nil))
}
