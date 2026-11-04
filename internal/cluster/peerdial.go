package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"time"
)

// peerDialTimeout bounds one endpoint probe.
const peerDialTimeout = 2 * time.Second

// errWrongPeer is returned when a derived endpoint answers as another node.
var errWrongPeer = errors.New("endpoint answered as another node")

// dialCandidate is one address to reach a member at.
type dialCandidate struct {
	addr string
	// verify requires a TLS handshake proving the address is the member:
	// set for addresses derived from its device record rather than
	// advertised as its cluster endpoints.
	verify bool
}

// memberDialCandidates lists the addresses to reach m at, best first.
// Members often advertise only the address they were joined over (a
// WireGuard one, say), while their device record, classified by the
// member's own interfaces, also names its LAN hosts. Those hosts at the
// cluster port come first, then the advertised endpoints in their order.
func memberDialCandidates(m Member) []dialCandidate {
	var direct []string
	for _, e := range m.Endpoints {
		if e.Kind == EndpointDirect && !slices.Contains(direct, e.Addr) {
			direct = append(direct, e.Addr)
		}
	}
	lanHosts := memberLANHosts(m)
	isLAN := func(addr string) bool {
		host, _, err := net.SplitHostPort(addr)
		return err == nil && slices.Contains(lanHosts, host)
	}
	var out []dialCandidate
	seen := map[string]bool{}
	add := func(addr string, verify bool) {
		if !seen[addr] {
			seen[addr] = true
			out = append(out, dialCandidate{addr: addr, verify: verify})
		}
	}
	for _, addr := range direct {
		if isLAN(addr) {
			add(addr, false)
		}
	}
	if port := clusterPort(direct); port != "" {
		for _, host := range lanHosts {
			addr := net.JoinHostPort(host, port)
			if !slices.Contains(direct, addr) {
				add(addr, true)
			}
		}
	}
	for _, addr := range direct {
		add(addr, false)
	}
	return out
}

// memberLANHosts are the hosts m's device record classifies as LAN.
func memberLANHosts(m Member) []string {
	if m.Device == nil {
		return nil
	}
	var out []string
	for _, l := range []DeviceListener{m.Device.Client, m.Device.WS} {
		for _, ep := range l.Endpoints {
			if ep.Kind != KindLAN {
				continue
			}
			if host, _, err := net.SplitHostPort(ep.Addr); err == nil && !slices.Contains(out, host) {
				out = append(out, host)
			}
		}
	}
	return out
}

// clusterPort is the port of the first parseable address.
func clusterPort(addrs []string) string {
	for _, addr := range addrs {
		if _, port, err := net.SplitHostPort(addr); err == nil && port != "" && port != "0" {
			return port
		}
	}
	return ""
}

type probeResult struct {
	idx int
	err error
}

// probeEndpoints dials every candidate at once and returns the best one
// that answers: a later candidate wins only once every earlier one has
// failed, so a reachable LAN address is preferred over a tunnel even when
// the tunnel answers first.
func (n *Node) probeEndpoints(ctx context.Context, m Member, candidates []dialCandidate) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("member %s has no direct endpoints", m.Name)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan probeResult, len(candidates))
	for i, c := range candidates {
		go func() { results <- probeResult{idx: i, err: n.probeEndpoint(ctx, m, c)} }()
	}
	done := make([]bool, len(candidates))
	errs := make([]error, len(candidates))
	for range candidates {
		r := <-results
		done[r.idx], errs[r.idx] = true, r.err
		for i := range candidates {
			if !done[i] {
				break
			}
			if errs[i] == nil {
				n.endpoints.set(m.NodeID, candidates[i].addr)
				return candidates[i].addr, nil
			}
		}
	}
	return "", fmt.Errorf("member %s unreachable: %w", m.Name, errors.Join(errs...))
}

// probeEndpoint checks that c accepts connections and, for derived
// addresses, that the node answering there is m under the cluster's
// certificate pinning.
func (n *Node) probeEndpoint(ctx context.Context, m Member, c dialCandidate) error {
	dialer := net.Dialer{Timeout: peerDialTimeout}
	if !c.verify {
		conn, err := dialer.DialContext(ctx, "tcp", c.addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	_, clientTLS := tlsConfigs(n.currentCert, n.trustedKeys)
	td := tls.Dialer{NetDialer: &dialer, Config: clientTLS}
	conn, err := td.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	peer, err := peerFromState(&state, n.trustedKeys())
	if err != nil {
		return err
	}
	if peer.NodeID != m.NodeID {
		return fmt.Errorf("%s: %w (%s)", c.addr, errWrongPeer, peer.Name)
	}
	return nil
}
