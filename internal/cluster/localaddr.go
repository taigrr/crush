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

// SystemInterfaces lists this host's network interfaces and addresses.
func SystemInterfaces() ([]NetInterface, error) {
	return systemInterfaces()
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

// AddrKind is how a device endpoint is reached: the network its address
// belongs to.
type AddrKind string

// Address kinds advertised to devices.
const (
	KindLAN       AddrKind = "lan"
	KindWireGuard AddrKind = "wireguard"
	KindTailscale AddrKind = "tailscale"
	KindVPN       AddrKind = "vpn"
	KindBridge    AddrKind = "bridge"
	KindPublic    AddrKind = "public"
	KindOverride  AddrKind = "override"
)

// Endpoint priorities: lower is preferred. Kinds occupy disjoint bands so
// clients may compare priorities across kinds and nodes.
const (
	PriorityOverride      = 0
	PriorityLAN           = 10 // private IPv4 on a broadcast physical interface
	PriorityLANOther      = 11 // other private IPv4 on a physical interface
	PriorityLANv6         = 12 // ULA on a physical interface
	PriorityLANForeign    = 15 // private address not on this host
	PriorityPublic        = 16 // public IPv4 on a physical interface
	PriorityPublicV6      = 17 // global IPv6 on a physical interface
	PriorityHostname      = 18 // a name, or a public address not on this host
	PriorityWireGuard     = 20
	PriorityTailscale     = 30
	PriorityTailscaleV6   = 31
	PriorityVPN           = 40
	PriorityBridge        = 90
	PriorityBridgeForeign = 95
	prioritySkip          = 1 << 30
)

// AdvertisedEndpoint is one classified device endpoint address.
type AdvertisedEndpoint struct {
	Addr     string   `json:"addr"`
	Kind     AddrKind `json:"kind"`
	Priority int      `json:"priority"`
}

// bridgePrefixes name container, VM, and host-only bridge interfaces
// (and Apple's peer-to-peer links): ranked last since a phone almost never
// reaches them.
var bridgePrefixes = []string{
	"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "bridge", "vnic",
	"vethernet", "awdl", "llw", "anpi", "ap1", "cni", "flannel", "lxc", "lxd",
}

// vpnPrefixes name tunnel interfaces that are not WireGuard or Tailscale.
var vpnPrefixes = []string{"utun", "tun", "tap", "ipsec", "ppp", "gif", "stf", "zt"}

var (
	tailnetPrefix   = netip.MustParsePrefix("100.64.0.0/10")
	tailnetPrefixV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	dockerPrefix    = netip.MustParsePrefix("172.17.0.0/16")
)

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func isTailnet(ip netip.Addr) bool {
	return tailnetPrefix.Contains(ip) || tailnetPrefixV6.Contains(ip)
}

func unusableIP(ip netip.Addr) bool {
	return !ip.IsValid() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

// ClassifyInterfaceAddr classifies ip on ifc. It reports false for
// addresses never advertised: down or loopback interfaces, and loopback,
// link-local, multicast, or unspecified addresses.
//
// WireGuard cannot be told apart from other tunnels reliably, so wg*
// interfaces and point-to-point utun interfaces count as WireGuard;
// tailscale* interfaces and Tailscale's address ranges as Tailscale.
func ClassifyInterfaceAddr(ifc NetInterface, ip netip.Addr) (AddrKind, int, bool) {
	ip = ip.Unmap()
	if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || unusableIP(ip) {
		return "", prioritySkip, false
	}
	name := strings.ToLower(ifc.Name)
	p2p := ifc.Flags&net.FlagPointToPoint != 0
	switch {
	case hasAnyPrefix(name, bridgePrefixes) || (ip.Is4() && dockerPrefix.Contains(ip)):
		return KindBridge, PriorityBridge, true
	case strings.HasPrefix(name, "tailscale") || strings.HasPrefix(name, "ts") || isTailnet(ip):
		if ip.Is6() {
			return KindTailscale, PriorityTailscaleV6, true
		}
		return KindTailscale, PriorityTailscale, true
	case strings.HasPrefix(name, "wg") || (strings.HasPrefix(name, "utun") && p2p):
		return KindWireGuard, PriorityWireGuard, true
	case hasAnyPrefix(name, vpnPrefixes) || p2p:
		return KindVPN, PriorityVPN, true
	case ip.IsPrivate() && ip.Is4() && ifc.Flags&net.FlagBroadcast != 0:
		return KindLAN, PriorityLAN, true
	case ip.IsPrivate() && ip.Is4():
		return KindLAN, PriorityLANOther, true
	case ip.IsPrivate():
		return KindLAN, PriorityLANv6, true
	case ip.Is4():
		return KindPublic, PriorityPublic, true
	default:
		return KindPublic, PriorityPublicV6, true
	}
}

// classifyForeign classifies a host that is not one of this host's
// addresses, by address range only.
func classifyForeign(host string) (AddrKind, int, bool) {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		if host == "" || strings.EqualFold(host, "localhost") {
			return "", prioritySkip, false
		}
		return KindPublic, PriorityHostname, true
	}
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast():
		return "", prioritySkip, false
	case isTailnet(ip):
		return KindTailscale, PriorityTailscale, true
	case ip.Is4() && dockerPrefix.Contains(ip):
		return KindBridge, PriorityBridgeForeign, true
	case ip.IsPrivate() || ip.IsLinkLocalUnicast():
		return KindLAN, PriorityLANForeign, true
	default:
		return KindPublic, PriorityHostname, true
	}
}

// ClassifyAddrs classifies host:port addresses using the interfaces ifs
// (this host's; nil for another node's, classified by range only) and
// returns them best first (stable within a priority). Addresses a phone
// cannot use (this host's loopback, link-local, or down-interface
// addresses; loopback, unspecified, or malformed ones) and repeats are
// dropped.
func ClassifyAddrs(ifs []NetInterface, addrs []string) []AdvertisedEndpoint {
	type cls struct {
		kind AddrKind
		prio int
	}
	best := map[netip.Addr]cls{}
	for _, ifc := range ifs {
		for _, ip := range ifc.Addrs {
			ip = ip.Unmap()
			k, pr, _ := ClassifyInterfaceAddr(ifc, ip)
			if old, ok := best[ip]; !ok || pr < old.prio {
				best[ip] = cls{k, pr}
			}
		}
	}
	out := make([]AdvertisedEndpoint, 0, len(addrs))
	seen := map[string]bool{}
	for _, a := range addrs {
		host, port, err := net.SplitHostPort(a)
		if err != nil || port == "" || port == "0" || seen[a] {
			continue
		}
		var c cls
		var ok bool
		if ip, perr := netip.ParseAddr(host); perr == nil {
			var found bool
			if c, found = best[ip.Unmap()]; found {
				ok = c.prio != prioritySkip
			} else {
				c.kind, c.prio, ok = classifyForeign(host)
			}
		} else {
			c.kind, c.prio, ok = classifyForeign(host)
		}
		if !ok {
			continue
		}
		seen[a] = true
		out = append(out, AdvertisedEndpoint{Addr: a, Kind: c.kind, Priority: c.prio})
	}
	slices.SortStableFunc(out, func(a, b AdvertisedEndpoint) int { return cmp.Compare(a.Priority, b.Priority) })
	return out
}

// EndpointAddrs is the addresses of eps, in order.
func EndpointAddrs(eps []AdvertisedEndpoint) []string {
	if len(eps) == 0 {
		return nil
	}
	out := make([]string, len(eps))
	for i, ep := range eps {
		out[i] = ep.Addr
	}
	return out
}

// rankAddr is the priority of ip on ifc (prioritySkip if unusable).
func rankAddr(ifc NetInterface, ip netip.Addr) int {
	_, p, _ := ClassifyInterfaceAddr(ifc, ip)
	return p
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
			if r == prioritySkip || seen[ip] {
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
// are from a phone, using the current interfaces (see [ClassifyAddrs]):
// LAN first, then public physical addresses and hostnames, WireGuard,
// Tailscale, other VPNs, and container or VM bridges last. The input is
// returned unchanged when the interfaces cannot be listed.
func OrderAdvertisedAddrs(addrs []string) []string {
	ifs, err := systemInterfaces()
	if err != nil {
		return addrs
	}
	return orderAddrs(ifs, addrs)
}

// OrderAddrsOn is OrderAdvertisedAddrs over the interfaces ifs.
func OrderAddrsOn(ifs []NetInterface, addrs []string) []string {
	return orderAddrs(ifs, addrs)
}

func orderAddrs(ifs []NetInterface, addrs []string) []string {
	return EndpointAddrs(ClassifyAddrs(ifs, addrs))
}
