package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// peerDialTimeout bounds one endpoint probe.
	peerDialTimeout = 2 * time.Second
	// peerPreferGrace is how long, after the first candidate answers, the
	// others get to answer too so the fastest can be picked.
	peerPreferGrace = 300 * time.Millisecond
	// peerReprobeInterval is how often a member's cached endpoint is
	// re-measured against its other candidates, so a faster path that
	// appears (back on the home network, say) is picked up without the
	// current one having to fail first.
	peerReprobeInterval = time.Minute
	// peerSwitchRatio and peerSwitchMargin are the hysteresis for leaving
	// a working endpoint: another must be faster by both, so noise does
	// not flap between two similar paths.
	peerSwitchRatio  = 0.7
	peerSwitchMargin = 2 * time.Millisecond
)

// errWrongPeer is returned when an endpoint answers as another node.
var errWrongPeer = errors.New("endpoint answered as another node")

// dialCandidate is one address to reach a member at.
type dialCandidate struct {
	addr string
}

// memberDialCandidates lists the addresses to reach m at: its advertised
// cluster endpoints, then every host its device record names (whatever
// the interface: LAN, WireGuard, Tailscale, another VPN) at the cluster
// port. Order is only a tie-break; the endpoint actually used is the one
// that connects fastest (see probeEndpoints), so no transport or subnet
// is assumed to be better than another.
func memberDialCandidates(m Member) []dialCandidate {
	var direct []string
	for _, e := range m.Endpoints {
		if e.Kind == EndpointDirect && !slices.Contains(direct, e.Addr) {
			direct = append(direct, e.Addr)
		}
	}
	var out []dialCandidate
	seen := map[string]bool{}
	add := func(addr string) {
		if !seen[addr] {
			seen[addr] = true
			out = append(out, dialCandidate{addr: addr})
		}
	}
	for _, addr := range direct {
		add(addr)
	}
	if port := clusterPort(direct); port != "" {
		for _, host := range memberDeviceHosts(m) {
			add(net.JoinHostPort(host, port))
		}
	}
	return out
}

// memberDeviceHosts are the hosts m's device record advertises, except
// container bridges, which are local to that machine.
func memberDeviceHosts(m Member) []string {
	if m.Device == nil {
		return nil
	}
	var out []string
	for _, l := range []DeviceListener{m.Device.Client, m.Device.WS} {
		for _, ep := range l.Endpoints {
			if ep.Kind == KindBridge {
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

// candidatesKey identifies a candidate set, so a change (new interfaces
// advertised) triggers a re-measurement.
func candidatesKey(cs []dialCandidate) string {
	addrs := make([]string, len(cs))
	for i, c := range cs {
		addrs[i] = c.addr
	}
	return strings.Join(addrs, ",")
}

// endpointCache remembers the endpoint chosen for each member and how
// fast it was when last measured.
type endpointCache struct {
	mu      sync.Mutex
	entries map[string]endpointEntry
	probing map[string]bool
}

type endpointEntry struct {
	addr     string
	rtt      time.Duration
	measured time.Time
	key      string
}

func (c *endpointCache) get(id string) (endpointEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	return e, ok
}

func (c *endpointCache) set(id string, e endpointEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]endpointEntry{}
	}
	c.entries[id] = e
}

func (c *endpointCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// startProbe marks a background re-measurement of id as running,
// reporting false when one already is.
func (c *endpointCache) startProbe(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.probing[id] {
		return false
	}
	if c.probing == nil {
		c.probing = map[string]bool{}
	}
	c.probing[id] = true
	return true
}

func (c *endpointCache) endProbe(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.probing, id)
}

// reprobeDue reports whether e should be re-measured against the
// candidate set identified by key.
func (e endpointEntry) reprobeDue(key string, now time.Time) bool {
	return e.key != key || now.Sub(e.measured) >= peerReprobeInterval
}

// maybeReprobe re-measures m's candidates in the background when its
// cached endpoint is due, switching only to a clearly faster one.
func (n *Node) maybeReprobe(m Member, cached endpointEntry, candidates []dialCandidate) {
	if len(candidates) < 2 || !cached.reprobeDue(candidatesKey(candidates), time.Now()) {
		return
	}
	if !n.endpoints.startProbe(m.NodeID) {
		return
	}
	go func() {
		defer n.endpoints.endProbe(m.NodeID)
		ctx, cancel := context.WithTimeout(context.Background(), peerDialTimeout+peerPreferGrace)
		defer cancel()
		_, _ = n.probeEndpoints(ctx, m, candidates)
	}()
}

type probeResult struct {
	idx int
	rtt time.Duration
	err error
}

// probeEndpoints dials every candidate at once and caches the one that
// connects fastest. After the first answer the rest get peerPreferGrace
// to answer too; a candidate that never answers (an address on a network
// this node left) thus costs the grace at most, not the dial timeout.
// A working cached endpoint is kept unless another is clearly faster.
func (n *Node) probeEndpoints(ctx context.Context, m Member, candidates []dialCandidate) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("member %s has no direct endpoints", m.Name)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan probeResult, len(candidates))
	for i, c := range candidates {
		go func() {
			rtt, err := n.probeEndpoint(ctx, m, c)
			results <- probeResult{idx: i, rtt: rtt, err: err}
		}()
	}
	rtts := make([]time.Duration, len(candidates))
	ok := make([]bool, len(candidates))
	errs := make([]error, len(candidates))
	answered := false
	var grace <-chan time.Time
collect:
	for pending := len(candidates); pending > 0; {
		select {
		case r := <-results:
			pending--
			if r.err != nil {
				errs[r.idx] = r.err
				continue
			}
			ok[r.idx], rtts[r.idx] = true, r.rtt
			if !answered {
				answered = true
				timer := time.NewTimer(peerPreferGrace)
				defer timer.Stop()
				grace = timer.C
			}
		case <-grace:
			break collect
		case <-ctx.Done():
			break collect
		}
	}
	if !answered {
		return "", fmt.Errorf("member %s unreachable: %w", m.Name, errors.Join(errs...))
	}
	best := -1
	for i := range candidates {
		if ok[i] && (best < 0 || rtts[i] < rtts[best]) {
			best = i
		}
	}
	if cached, found := n.endpoints.get(m.NodeID); found {
		i := slices.IndexFunc(candidates, func(c dialCandidate) bool { return c.addr == cached.addr })
		if i >= 0 && ok[i] && !clearlyFaster(rtts[best], rtts[i]) {
			best = i
		}
	}
	n.endpoints.set(m.NodeID, endpointEntry{
		addr:     candidates[best].addr,
		rtt:      rtts[best],
		measured: time.Now(),
		key:      candidatesKey(candidates),
	})
	return candidates[best].addr, nil
}

// clearlyFaster reports whether a beats b by enough to switch to it.
func clearlyFaster(a, b time.Duration) bool {
	return float64(a) < float64(b)*peerSwitchRatio && b-a > peerSwitchMargin
}

// probeEndpoint measures how long c takes to complete a TLS handshake,
// which spans several round trips over the real path, and checks that
// the node answering there is m under the cluster's certificate pinning.
// Every candidate is checked: an address that once reached m (a LAN IP
// on another network, say) may now reach another member.
func (n *Node) probeEndpoint(ctx context.Context, m Member, c dialCandidate) (time.Duration, error) {
	dialer := net.Dialer{Timeout: peerDialTimeout}
	_, clientTLS := tlsConfigs(n.currentCert, n.trustedKeys)
	td := tls.Dialer{NetDialer: &dialer, Config: clientTLS}
	start := time.Now()
	conn, err := td.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return 0, err
	}
	rtt := time.Since(start)
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	peer, err := peerFromState(&state, n.trustedKeys())
	if err != nil {
		return 0, err
	}
	if peer.NodeID != m.NodeID {
		return 0, fmt.Errorf("%s: %w (%s)", c.addr, errWrongPeer, peer.Name)
	}
	return rtt, nil
}
