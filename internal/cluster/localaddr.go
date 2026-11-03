package cluster

import (
	"cmp"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// NetInterface is the part of a network interface address selection
// looks at.
type NetInterface struct {
	Name  string
	Flags net.Flags
	Addrs []netip.Addr
}

// systemInterfaces lists this host's interfaces (replaced in tests).
var systemInterfaces = func() ([]NetInterface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]NetInterface, 0, len(ifs))
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		ni := NetInterface{Name: ifc.Name, Flags: ifc.Flags}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(ipn.IP); ok {
					ni.Addrs = append(ni.Addrs, ip.Unmap())
				}
			}
		}
		out = append(out, ni)
	}
	return out, nil
}

// Address ranks, best first. rankSkip addresses are never advertised.
const (
	rankLAN     = iota // private IPv4 on a broadcast-capable physical interface
	rankPrivate        // other private IPv4 on a physical interface
	rankPublic4        // public IPv4 on a physical interface
	rankIPv6           // routable IPv6 on a physical interface
	rankForeign        // not one of this host's addresses (hostnames, NAT)
	rankTunnel         // VPN, WireGuard, or point-to-point address (last resort)
	rankTailnet        // Tailscale CGNAT address on a tunnel interface
	rankSkip
)

// tunnelPrefixes name virtual interfaces whose addresses a phone on the
// LAN usually cannot reach: VPN and WireGuard tunnels, VM and container
// bridges, and Apple's peer-to-peer links. They are kept as last-resort
// candidates (a cluster mesh or a roaming phone may use a VPN).
var tunnelPrefixes = []string{
	"utun", "tun", "tap", "wg", "tailscale", "ts", "ipsec", "ppp", "gif", "stf",
	"bridge", "br-", "docker", "veth", "virbr", "vmnet", "vboxnet", "vnic",
	"zt", "awdl", "llw", "anpi", "ap1", "vethernet",
}

var tailnetPrefix = netip.MustParsePrefix("100.64.0.0/10")

func isTunnel(ifc NetInterface) bool {
	if ifc.Flags&net.FlagPointToPoint != 0 {
		return true
	}
	name := strings.ToLower(ifc.Name)
	for _, p := range tunnelPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// rankAddr ranks ip on ifc.
func rankAddr(ifc NetInterface, ip netip.Addr) int {
	if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
		return rankSkip
	}
	if !ip.IsValid() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return rankSkip
	}
	if isTunnel(ifc) {
		if ip.Is4() && tailnetPrefix.Contains(ip) {
			return rankTailnet
		}
		return rankTunnel
	}
	switch {
	case ip.Is4() && ip.IsPrivate() && ifc.Flags&net.FlagBroadcast != 0:
		return rankLAN
	case ip.Is4() && ip.IsPrivate():
		return rankPrivate
	case ip.Is4():
		return rankPublic4
	default:
		return rankIPv6
	}
}

type rankedAddr struct {
	ip    netip.Addr
	iface string
	rank  int
}

// candidateAddrs is every advertisable address of ifs, best first, in a
// deterministic order (rank, interface name, address).
func candidateAddrs(ifs []NetInterface) []rankedAddr {
	var out []rankedAddr
	seen := map[netip.Addr]bool{}
	for _, ifc := range ifs {
		for _, ip := range ifc.Addrs {
			ip = ip.Unmap()
			r := rankAddr(ifc, ip)
			if r == rankSkip || seen[ip] {
				continue
			}
			seen[ip] = true
			out = append(out, rankedAddr{ip: ip, iface: ifc.Name, rank: r})
		}
	}
	slices.SortFunc(out, func(a, b rankedAddr) int {
		return cmp.Or(cmp.Compare(a.rank, b.rank), compareIfaceName(a.iface, b.iface), a.ip.Compare(b.ip))
	})
	return out
}

// compareIfaceName orders names naturally, so en2 sorts before en10.
func compareIfaceName(a, b string) int {
	ap, an := splitIfaceName(a)
	bp, bn := splitIfaceName(b)
	return cmp.Or(cmp.Compare(ap, bp), cmp.Compare(an, bn), cmp.Compare(a, b))
}

func splitIfaceName(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		n = -1
	}
	return s[:i], n
}

// localEndpointsFrom is LocalEndpoints over ifs.
func localEndpointsFrom(ifs []NetInterface, port int) []Endpoint {
	var out []Endpoint
	for _, c := range candidateAddrs(ifs) {
		out = append(out, Endpoint{Kind: EndpointDirect, Addr: net.JoinHostPort(c.ip.String(), strconv.Itoa(port))})
	}
	return out
}

// OrderAdvertisedAddrs reorders host:port addresses by how reachable they
// are from a phone on this host's LAN, using the current interfaces:
// private IPv4 on physical broadcast interfaces first, then other
// physical addresses, then addresses that are not this host's own
// (hostnames, NAT), then tunnel and point-to-point addresses (WireGuard,
// VPNs), then Tailscale. This host's addresses on loopback or down
// interfaces, and link-local addresses, are dropped. The order is
// stable otherwise, and the input is returned unchanged when the
// interfaces cannot be listed.
func OrderAdvertisedAddrs(addrs []string) []string {
	ifs, err := systemInterfaces()
	if err != nil {
		return addrs
	}
	return orderAddrs(ifs, addrs)
}

func orderAddrs(ifs []NetInterface, addrs []string) []string {
	best := map[netip.Addr]int{}
	for _, ifc := range ifs {
		for _, ip := range ifc.Addrs {
			ip = ip.Unmap()
			r := rankAddr(ifc, ip)
			if old, ok := best[ip]; !ok || r < old {
				best[ip] = r
			}
		}
	}
	type item struct {
		addr string
		rank int
	}
	items := make([]item, 0, len(addrs))
	for _, a := range addrs {
		rank := rankForeign
		if host, _, err := net.SplitHostPort(a); err == nil {
			if ip, err := netip.ParseAddr(host); err == nil {
				if r, ok := best[ip.Unmap()]; ok {
					rank = r
				} else if ip.Is4() && tailnetPrefix.Contains(ip) {
					rank = rankTailnet
				}
			}
		}
		if rank == rankSkip {
			continue
		}
		items = append(items, item{a, rank})
	}
	slices.SortStableFunc(items, func(a, b item) int { return cmp.Compare(a.rank, b.rank) })
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.addr)
	}
	return out
}
