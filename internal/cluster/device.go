package cluster

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"
)

// DeviceEndpointsVersion is the version of the device endpoint record a
// member advertises (docs/specs/MOBILE_DEVICE_BRIDGE.md, Member device
// ports). Readers that do not know a record's version give phones no
// addresses for that member.
const DeviceEndpointsVersion = 1

// DeviceListenerState is the state of one device listener on a member.
type DeviceListenerState string

// Device listener states.
const (
	// DeviceListenerOn: bound now; Addrs are the bound addresses.
	DeviceListenerOn DeviceListenerState = "on"
	// DeviceListenerIdle: enabled but not bound right now (gated); Addrs
	// are those of the last bind, if any.
	DeviceListenerIdle DeviceListenerState = "idle"
	// DeviceListenerOff: disabled on the member; no Addrs.
	DeviceListenerOff DeviceListenerState = "off"
)

// DeviceListener is one advertised device listener.
type DeviceListener struct {
	State DeviceListenerState `json:"state"`
	// Addrs are host:port pairs built from the bound listener address: a
	// specific bind is its own host only, a wildcard bind every direct
	// endpoint host of the member.
	Addrs []string `json:"addrs,omitempty"`
	// Endpoints are Addrs classified by the advertising member, which
	// knows its own interfaces (additive: older members omit it).
	Endpoints []AdvertisedEndpoint `json:"endpoints,omitempty"`
}

// DeviceEndpoints is a member's own advertisement of its device
// listeners. Absence on a member record means the member predates
// advertising (legacy fallback).
type DeviceEndpoints struct {
	V int `json:"v"`
	// Seq increases with every advertisement from the member; an older or
	// equal Seq never replaces a stored record.
	Seq    int64          `json:"seq"`
	Client DeviceListener `json:"client"`
	WS     DeviceListener `json:"ws"`
}

// Clone returns a deep copy (nil for nil).
func (d *DeviceEndpoints) Clone() *DeviceEndpoints {
	if d == nil {
		return nil
	}
	out := *d
	out.Client.Addrs = slices.Clone(d.Client.Addrs)
	out.WS.Addrs = slices.Clone(d.WS.Addrs)
	return &out
}

// SameListeners reports whether a and b advertise the same listeners
// (ignoring Seq).
func (d *DeviceEndpoints) SameListeners(o *DeviceEndpoints) bool {
	if d == nil || o == nil {
		return d == o
	}
	return d.V == o.V && d.Client.State == o.Client.State && d.WS.State == o.WS.State &&
		slices.Equal(d.Client.Addrs, o.Client.Addrs) && slices.Equal(d.WS.Addrs, o.WS.Addrs) &&
		slices.Equal(d.Client.Endpoints, o.Client.Endpoints) && slices.Equal(d.WS.Endpoints, o.WS.Endpoints)
}

// newerDevice reports whether incoming should replace stored.
func newerDevice(incoming, stored *DeviceEndpoints) bool {
	if incoming == nil {
		return false
	}
	return stored == nil || incoming.Seq > stored.Seq
}

// mergeDevice picks the device record to keep for a member when a
// record for it arrives from sender. Only the member itself may set or
// change its record, and only forward in Seq; records relayed by other
// members are ignored (a member learned through a relay starts without
// one until it advertises itself).
func mergeDevice(memberID, senderID string, stored, incoming *DeviceEndpoints) *DeviceEndpoints {
	if memberID == senderID && newerDevice(incoming, stored) {
		return incoming.Clone()
	}
	return stored.Clone()
}

// AdvertisedAddrs expands a bound listener address into the addresses a
// member advertises for it: a specific host is advertised as bound; a
// wildcard (empty, 0.0.0.0, ::) host is advertised on every direct
// endpoint host of eps, with the bound port.
func AdvertisedAddrs(bound string, eps []Endpoint) []string {
	host, port, err := net.SplitHostPort(bound)
	if err != nil || port == "" || port == "0" {
		return nil
	}
	if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
		return []string{net.JoinHostPort(host, port)}
	}
	var out []string
	for _, ep := range eps {
		if ep.Kind != EndpointDirect {
			continue
		}
		h, _, err := net.SplitHostPort(ep.Addr)
		if err != nil || h == "" {
			continue
		}
		addr := net.JoinHostPort(h, port)
		if !slices.Contains(out, addr) {
			out = append(out, addr)
		}
	}
	return out
}

// deviceSeq hands out strictly increasing advertisement sequence numbers
// (wall-clock nanoseconds, bumped past the last one).
type deviceSeq struct {
	mu   sync.Mutex
	last int64
}

func (s *deviceSeq) next(floor int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := max(time.Now().UnixNano(), s.last+1, floor+1)
	s.last = v
	return v
}

// AdvertiseDevice stores rec (stamped with version and a new Seq) as this
// node's own device record and pushes it to every other member. err is
// set when the record could not be stored (ErrNotInCluster outside a
// cluster); pushErrs lists the members that did not take it.
func (n *Node) AdvertiseDevice(ctx context.Context, rec DeviceEndpoints) (stored DeviceEndpoints, pushErrs []error, err error) {
	var self Member
	err = n.store.Update(func(st *State) error {
		i := slices.IndexFunc(st.Members, func(m Member) bool { return m.NodeID == n.id.ID })
		if i < 0 {
			return ErrNotInCluster
		}
		var floor int64
		if old := st.Members[i].Device; old != nil {
			floor = old.Seq
		}
		rec.V = DeviceEndpointsVersion
		rec.Seq = n.deviceSeq.next(floor)
		st.Members[i].Device = rec.Clone()
		// The stored self row keeps the version from when this node
		// joined; advertise the one actually running.
		st.Members[i].Version = n.version
		self = st.Members[i]
		self.Endpoints = slices.Clone(self.Endpoints)
		self.PublicKey = slices.Clone(self.PublicKey)
		return nil
	})
	if err != nil {
		return rec, nil, err
	}
	self.LastSeen = time.Time{}
	pushErrs = n.broadcast(ctx, "/cluster/v1/members", membersBody{Members: []Member{self}}, "")
	if len(pushErrs) > 0 {
		slog.Debug("Device endpoint advertisement incomplete", "failed", len(pushErrs))
	}
	return rec, pushErrs, nil
}

// HostAdvertisedAddrs is EndpointAddrs of HostAdvertisedEndpoints.
func HostAdvertisedAddrs(ifs []NetInterface, bound string, eps []Endpoint) []string {
	return EndpointAddrs(HostAdvertisedEndpoints(ifs, bound, eps))
}

// HostAdvertisedEndpoints is AdvertisedAddrs for a listener on this host,
// classified and ranked with the host's interfaces ifs (see
// [SystemInterfaces], [ClassifyAddrs]). A wildcard bind is advertised on
// every usable address of the host's current interfaces (LAN, WireGuard,
// Tailscale, VPN, bridges), since a phone may not reach the recorded
// cluster endpoints. Loopback and link-local addresses are never
// included.
func HostAdvertisedEndpoints(ifs []NetInterface, bound string, eps []Endpoint) []AdvertisedEndpoint {
	out := AdvertisedAddrs(bound, eps)
	host, port, err := net.SplitHostPort(bound)
	if err == nil && port != "" && port != "0" {
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			for _, c := range candidateAddrs(ifs) {
				addr := net.JoinHostPort(c.ip.String(), port)
				if !slices.Contains(out, addr) {
					out = append(out, addr)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return ClassifyAddrs(ifs, out)
}
