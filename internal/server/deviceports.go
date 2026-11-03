package server

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
)

// deviceAdvertiseRetry is how soon a device port advertisement that some
// member did not receive is pushed again.
const deviceAdvertiseRetry = 5 * time.Second

// Values of pairing.MemberInfo.DevicePorts.
const (
	devicePortsAdvertised = "advertised"
	devicePortsLegacy     = "legacy"
	devicePortsUnknown    = "unknown"
)

// markAdvertise asks the advertiser to re-advertise this node's device
// ports. It never blocks, so listeners call it with their lock held.
func (p *pairingService) markAdvertise() {
	select {
	case p.advDirty <- struct{}{}:
	default:
	}
}

// advertiseLoop pushes this node's device listener record to the cluster
// whenever it changes (the listeners bind or unbind) and on start, and
// retries until every member has it.
func (p *pairingService) advertiseLoop(ctx context.Context) {
	var last *cluster.DeviceEndpoints
	failed := false
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.advDirty:
		case <-retry.C:
		}
		rec := p.deviceRecord()
		if !failed && last.SameListeners(&rec) {
			continue
		}
		stored, errs := p.node.AdvertiseDevice(ctx, rec)
		if ctx.Err() != nil {
			return
		}
		last, failed = &stored, len(errs) > 0
		if failed {
			slog.Debug("Device port advertisement will be retried", "failed", len(errs))
			retry.Reset(deviceAdvertiseRetry)
		}
	}
}

// selfEndpoints is this node's own cluster endpoints.
func (p *pairingService) selfEndpoints() []cluster.Endpoint {
	st, err := p.node.Store().Snapshot()
	if err != nil {
		return nil
	}
	m, _ := st.Member(p.node.Identity().ID)
	return m.Endpoints
}

// deviceRecord is this node's device listener record, from the bound
// listener addresses (never the configured ones).
func (p *pairingService) deviceRecord() cluster.DeviceEndpoints {
	return p.deviceRecordFor(p.selfEndpoints())
}

// deviceRecordFor is deviceRecord with wildcard binds expanded on eps.
func (p *pairingService) deviceRecordFor(eps []cluster.Endpoint) cluster.DeviceEndpoints {
	return cluster.DeviceEndpoints{
		V:      cluster.DeviceEndpointsVersion,
		Client: listenerRecord(p.clientAddr != "", p.client.Addr(), p.client.LastAddr(), eps),
		WS:     listenerRecord(p.wsAddr != "", p.ws.Addr(), p.ws.LastAddr(), eps),
	}
}

func listenerRecord(enabled bool, bound, last string, eps []cluster.Endpoint) cluster.DeviceListener {
	switch {
	case !enabled:
		return cluster.DeviceListener{State: cluster.DeviceListenerOff}
	case bound != "":
		return cluster.DeviceListener{State: cluster.DeviceListenerOn, Addrs: cluster.AdvertisedAddrs(bound, eps)}
	default:
		return cluster.DeviceListener{State: cluster.DeviceListenerIdle, Addrs: cluster.AdvertisedAddrs(last, eps)}
	}
}

// selfListenerAddrs are the addresses this node gives phones for one of
// its own listeners: bound, else last bound, else (an offer is created
// just before the gate binds) the configured address unless its port is
// ephemeral.
func selfListenerAddrs(l cluster.DeviceListener, configured string, eps []cluster.Endpoint) []string {
	if l.State == cluster.DeviceListenerOff {
		return nil
	}
	if len(l.Addrs) > 0 {
		return l.Addrs
	}
	return cluster.AdvertisedAddrs(configured, eps)
}

// selfDevice is this node's own client endpoints and websocket URLs.
func (p *pairingService) selfDevice(eps []cluster.Endpoint) ([]pairing.Endpoint, []string) {
	rec := p.deviceRecordFor(eps)
	clients := addrEndpoints(selfListenerAddrs(rec.Client, p.clientAddr, eps))
	return clients, wssURLs(addrEndpoints(selfListenerAddrs(rec.WS, p.wsAddr, eps)))
}

// memberDevice is what phones are told about another member's device
// ports: its own advertisement when it has one, the v1 fallback (this
// node's ports on its hosts) when it predates advertising, and nothing
// for a record version this node does not know.
func (p *pairingService) memberDevice(m cluster.Member, selfEps []cluster.Endpoint) ([]pairing.Endpoint, []string, string) {
	switch {
	case m.Device == nil:
		selfClient, selfWS := p.selfDevice(selfEps)
		var urls []string
		if wsPort := portOfAny(selfWS); wsPort != "" {
			urls = wssURLs(clientEndpoints(m.Endpoints, wsPort))
		}
		var clients []pairing.Endpoint
		if clientPort := portOfAny(endpointAddrs(selfClient)); clientPort != "" {
			clients = clientEndpoints(m.Endpoints, clientPort)
		}
		return clients, urls, devicePortsLegacy
	case m.Device.V != cluster.DeviceEndpointsVersion:
		return nil, nil, devicePortsUnknown
	}
	var clients []pairing.Endpoint
	if m.Device.Client.State != cluster.DeviceListenerOff {
		clients = addrEndpoints(m.Device.Client.Addrs)
	}
	var urls []string
	if m.Device.WS.State != cluster.DeviceListenerOff {
		urls = wssURLs(addrEndpoints(m.Device.WS.Addrs))
	}
	return clients, urls, devicePortsAdvertised
}

// portOfAny is the port of the first address (or wss:// URL) in addrs.
func portOfAny(addrs []string) string {
	for _, a := range addrs {
		if len(a) > len("wss://") && a[:len("wss://")] == "wss://" {
			a = a[len("wss://"):]
		}
		if _, port, err := net.SplitHostPort(a); err == nil && port != "" && port != "0" {
			return port
		}
	}
	return ""
}

func endpointAddrs(eps []pairing.Endpoint) []string {
	out := make([]string, 0, len(eps))
	for _, ep := range eps {
		out = append(out, ep.Addr)
	}
	return out
}

func wssURLs(eps []pairing.Endpoint) []string {
	var out []string
	for _, ep := range eps {
		out = append(out, "wss://"+ep.Addr)
	}
	return out
}

// clientEndpoints maps a member's direct endpoint hosts, with port, to
// typed client endpoints.
func clientEndpoints(eps []cluster.Endpoint, port string) []pairing.Endpoint {
	var addrs []string
	for _, ep := range eps {
		if ep.Kind != cluster.EndpointDirect {
			continue
		}
		if host, _, err := net.SplitHostPort(ep.Addr); err == nil {
			addrs = append(addrs, net.JoinHostPort(host, port))
		}
	}
	return addrEndpoints(addrs)
}

// addrEndpoints types host:port addresses by address range. Hosts a
// phone cannot use (empty, unspecified, loopback) and repeated hosts are
// skipped.
func addrEndpoints(addrs []string) []pairing.Endpoint {
	var out []pairing.Endpoint
	seen := map[string]bool{}
	tailnet := netip.MustParsePrefix("100.64.0.0/10")
	for _, addr := range addrs {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host == "" || host == "localhost" || port == "" || port == "0" || seen[host] {
			continue
		}
		seen[host] = true
		kind := pairing.EndpointDirect
		if ip, err := netip.ParseAddr(host); err == nil {
			if ip.IsUnspecified() || ip.IsLoopback() {
				continue
			}
			switch {
			case tailnet.Contains(ip):
				kind = pairing.EndpointTailscale
			case ip.IsPrivate() || ip.IsLinkLocalUnicast():
				kind = pairing.EndpointLAN
			}
		}
		out = append(out, pairing.Endpoint{Kind: kind, Addr: net.JoinHostPort(host, port)})
	}
	return out
}
