package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
)

// deviceAdvertiseRetry is how soon a device port advertisement that some
// member did not receive is pushed again.
const deviceAdvertiseRetry = 5 * time.Second

// deviceRescanInterval is how often the host interfaces are re-read so
// wildcard device listeners are re-advertised when addresses change (a
// new Wi-Fi network, a VPN coming up). Only a changed record is pushed.
var deviceRescanInterval = 30 * time.Second

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
// whenever it changes (the listeners bind or unbind, or the host's
// interface addresses change, checked every deviceRescanInterval) and on
// start, and retries until every member has it.
func (p *pairingService) advertiseLoop(ctx context.Context) {
	var last *cluster.DeviceEndpoints
	failed := false
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	rescan := time.NewTicker(deviceRescanInterval)
	defer rescan.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.advDirty:
		case <-retry.C:
		case <-rescan.C:
		}
		rec := p.deviceRecord()
		if !failed && last.SameListeners(&rec) {
			continue
		}
		stored, pushErrs, err := p.node.AdvertiseDevice(ctx, rec)
		if ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, cluster.ErrNotInCluster):
			// Nothing to advertise to; startMesh re-advertises on join.
			last, failed = nil, false
		case err != nil:
			slog.Warn("Failed to store device port advertisement", "error", err)
			last, failed = nil, true
			retry.Reset(deviceAdvertiseRetry)
		default:
			last, failed = &stored, len(pushErrs) > 0
			if failed {
				slog.Debug("Device port advertisement will be retried", "failed", len(pushErrs))
				retry.Reset(deviceAdvertiseRetry)
			}
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

// deviceInterfaces lists the host interfaces whose addresses wildcard
// device listeners are advertised on (replaced in tests, before any run).
var deviceInterfaces = cluster.SystemInterfaces

// hostInterfaces is this node's current interfaces, or nil when they
// cannot be listed.
func (p *pairingService) hostInterfaces() []cluster.NetInterface {
	list := deviceInterfaces
	if p.interfaces != nil {
		list = p.interfaces
	}
	ifs, err := list()
	if err != nil {
		slog.Debug("Failed to list network interfaces", "error", err)
		return nil
	}
	return ifs
}

// deviceRecord is this node's device listener record, from the bound
// listener addresses (never the configured ones).
func (p *pairingService) deviceRecord() cluster.DeviceEndpoints {
	return p.deviceRecordFor(p.selfEndpoints())
}

// deviceRecordFor is deviceRecord with wildcard binds expanded on eps.
func (p *pairingService) deviceRecordFor(eps []cluster.Endpoint) cluster.DeviceEndpoints {
	ifs := p.hostInterfaces()
	return cluster.DeviceEndpoints{
		V:      cluster.DeviceEndpointsVersion,
		Client: listenerRecord(ifs, p.clientAddr != "", p.client.Addr(), p.client.LastAddr(), eps),
		WS:     listenerRecord(ifs, p.wsAddr != "", p.ws.Addr(), p.ws.LastAddr(), eps),
	}
}

// listenerRecord is one listener's record; a wildcard bind is advertised
// on the host's current interface addresses (ifs) and on eps, classified
// with the host's interfaces.
func listenerRecord(ifs []cluster.NetInterface, enabled bool, bound, last string, eps []cluster.Endpoint) cluster.DeviceListener {
	if !enabled {
		return cluster.DeviceListener{State: cluster.DeviceListenerOff}
	}
	l := cluster.DeviceListener{State: cluster.DeviceListenerOn}
	addr := bound
	if bound == "" {
		l.State, addr = cluster.DeviceListenerIdle, last
	}
	l.Endpoints = cluster.HostAdvertisedEndpoints(ifs, addr, eps)
	if len(l.Endpoints) == 0 {
		// Nothing a phone can use (a loopback bind): record the bind as is.
		l.Endpoints = nil
		l.Addrs = cluster.AdvertisedAddrs(addr, eps)
		return l
	}
	l.Addrs = cluster.EndpointAddrs(l.Endpoints)
	return l
}

// selfListenerEndpoints are the endpoints this node gives phones for one
// of its own listeners: bound, else last bound, else (an offer is created
// just before the gate binds) the configured address unless its port is
// ephemeral.
func selfListenerEndpoints(ifs []cluster.NetInterface, l cluster.DeviceListener, configured string, eps []cluster.Endpoint) []cluster.AdvertisedEndpoint {
	switch {
	case l.State == cluster.DeviceListenerOff:
		return nil
	case len(l.Endpoints) > 0:
		return l.Endpoints
	case len(l.Addrs) > 0:
		return cluster.ClassifyAddrs(ifs, l.Addrs)
	}
	return cluster.HostAdvertisedEndpoints(ifs, configured, eps)
}

// selfDevice is this node's own client endpoints and websocket URLs,
// best first (recorded endpoints may predate the current interfaces, so
// they are re-classified on every call).
func (p *pairingService) selfDevice(eps []cluster.Endpoint) ([]pairing.Endpoint, []string) {
	clients, ws := p.selfDeviceEndpoints(eps)
	return clients, wssURLs(ws)
}

// selfDeviceEndpoints is selfDevice with the websocket endpoints typed.
func (p *pairingService) selfDeviceEndpoints(eps []cluster.Endpoint) (clients, ws []pairing.Endpoint) {
	ifs := p.hostInterfaces()
	rec := p.deviceRecordFor(eps)
	return classifiedEndpoints(selfListenerEndpoints(ifs, rec.Client, p.clientAddr, eps)),
		classifiedEndpoints(selfListenerEndpoints(ifs, rec.WS, p.wsAddr, eps))
}

// memberDevice is what phones are told about another member's device
// ports: its own advertisement when it has one, the v1 fallback (this
// node's ports on its hosts) when it predates advertising, and nothing
// for a record version this node does not know. ws are the websocket
// endpoints (host:port).
func (p *pairingService) memberDevice(m cluster.Member, selfEps []cluster.Endpoint) (clients, ws []pairing.Endpoint, source string) {
	switch {
	case m.Device == nil:
		selfClient, selfWS := p.selfDeviceEndpoints(selfEps)
		if wsPort := portOfAny(endpointAddrs(selfWS)); wsPort != "" {
			ws = clientEndpoints(m.Endpoints, wsPort)
		}
		if clientPort := portOfAny(endpointAddrs(selfClient)); clientPort != "" {
			clients = clientEndpoints(m.Endpoints, clientPort)
		}
		return clients, ws, devicePortsLegacy
	case m.Device.V != cluster.DeviceEndpointsVersion:
		return nil, nil, devicePortsUnknown
	}
	return memberListener(m.Device.Client), memberListener(m.Device.WS), devicePortsAdvertised
}

// memberListener is another member's advertised listener: classified by
// the member itself when it says how, else by address range.
func memberListener(l cluster.DeviceListener) []pairing.Endpoint {
	switch {
	case l.State == cluster.DeviceListenerOff:
		return nil
	case len(l.Endpoints) > 0:
		return classifiedEndpoints(l.Endpoints)
	}
	return addrEndpoints(l.Addrs)
}

// natsEndpoints types websocket endpoints as wss:// URLs, in order.
func natsEndpoints(eps []pairing.Endpoint) []pairing.NATSEndpoint {
	var out []pairing.NATSEndpoint
	for _, ep := range eps {
		out = append(out, pairing.NATSEndpoint{URL: "wss://" + ep.Addr, Kind: ep.JSONKind(), Priority: ep.Priority})
	}
	return out
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

// addrEndpoints types host:port addresses of another node by address
// range, best first. Hosts a phone cannot use (empty, unspecified,
// loopback) and repeated hosts are skipped.
func addrEndpoints(addrs []string) []pairing.Endpoint {
	return classifiedEndpoints(cluster.ClassifyAddrs(nil, addrs))
}

// classifiedEndpoints maps classified addresses to typed endpoints, in
// order, skipping repeated hosts and hosts a phone cannot use.
func classifiedEndpoints(eps []cluster.AdvertisedEndpoint) []pairing.Endpoint {
	var out []pairing.Endpoint
	seen := map[string]bool{}
	for _, ep := range eps {
		host, port, err := net.SplitHostPort(ep.Addr)
		if err != nil || host == "" || host == "localhost" || port == "" || port == "0" || seen[host] {
			continue
		}
		if ip, err := netip.ParseAddr(host); err == nil && (ip.IsUnspecified() || ip.IsLoopback()) {
			continue
		}
		seen[host] = true
		out = append(out, pairing.Endpoint{
			Kind: binaryKind(ep.Kind), Addr: net.JoinHostPort(host, port),
			Class: string(ep.Kind), Priority: ep.Priority,
		})
	}
	return out
}

// binaryKind is the QR payload kind for a classified endpoint.
func binaryKind(k cluster.AddrKind) pairing.EndpointKind {
	switch k {
	case cluster.KindLAN:
		return pairing.EndpointLAN
	case cluster.KindWireGuard:
		return pairing.EndpointWireGuard
	case cluster.KindTailscale:
		return pairing.EndpointTailscale
	default:
		return pairing.EndpointDirect
	}
}

// hostOptions pins an offer's advertised client endpoints to hosts
// (crush pair --host), in the given order. A host without a port gets
// this node's client listener port.
func (p *pairingService) hostOptions(hosts []string) ([]pairing.SelfOption, error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	clients, _ := p.selfDevice(p.selfEndpoints())
	port := portOfAny(endpointAddrs(clients))
	if port == "" {
		port = portOfAny([]string{p.clientAddr})
	}
	var addrs []string
	for _, h := range hosts {
		addr, err := pinnedAddr(h, port)
		if err != nil {
			return nil, &pairing.Error{Code: pairing.ErrCodeBadRequest, Message: err.Error()}
		}
		addrs = append(addrs, addr)
	}
	eps := overrideEndpoints(addrs)
	if len(eps) == 0 {
		return nil, &pairing.Error{Code: pairing.ErrCodeBadRequest, Message: "no usable advertised host (loopback and unspecified addresses cannot reach a phone)"}
	}
	return []pairing.SelfOption{pairing.WithEndpoints(eps)}, nil
}

// pinnedAddr turns a --host value (host, host:port, or [v6]:port) into
// host:port.
func pinnedAddr(h, port string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" || strings.ContainsAny(h, "/?#@ ") {
		return "", fmt.Errorf("invalid advertised host %q", h)
	}
	if host, p, err := net.SplitHostPort(h); err == nil {
		if host == "" || p == "" {
			return "", fmt.Errorf("invalid advertised host %q", h)
		}
		if _, err := strconv.ParseUint(p, 10, 16); err != nil {
			return "", fmt.Errorf("invalid port in advertised host %q", h)
		}
		return net.JoinHostPort(host, p), nil
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if port == "" {
		return "", fmt.Errorf("advertised host %q needs a port: the client listener is not configured", h)
	}
	return net.JoinHostPort(h, port), nil
}

// overrideEndpoints types operator-pinned addresses (crush pair --host):
// kind override, priority 0, in the given order.
func overrideEndpoints(addrs []string) []pairing.Endpoint {
	var out []pairing.Endpoint
	ranked := map[string]cluster.AdvertisedEndpoint{}
	for _, ep := range cluster.ClassifyAddrs(nil, addrs) {
		ranked[ep.Addr] = ep
	}
	var keep []cluster.AdvertisedEndpoint
	for _, a := range addrs {
		if ep, ok := ranked[a]; ok {
			keep = append(keep, ep)
		}
	}
	for _, ep := range classifiedEndpoints(keep) {
		ep.Class, ep.Priority = string(cluster.KindOverride), cluster.PriorityOverride
		out = append(out, ep)
	}
	return out
}
