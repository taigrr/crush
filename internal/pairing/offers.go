package pairing

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"
	"time"
)

// Offer timing and limits from the spec.
const (
	DefaultOfferTTL  = 120 * time.Second
	MaxOfferTTL      = 10 * time.Minute
	DeviceOfferTTL   = 10 * time.Minute
	ClaimApprovalTTL = 10 * time.Minute
	MaxPollWait      = 30 * time.Second
	TombstoneTTL     = time.Hour

	maxFailures     = 3
	maxActiveOffers = 16
	// Claims are limited per source address, never node-wide, so a LAN
	// attacker probing offer IDs cannot lock a real phone out. Attacks on
	// a specific offer need its ID (which travels with the secret) and are
	// bounded by that offer's three-strike limit.
	claimsPerWindow = 5
	claimWindow     = time.Minute
	maxSources      = 1024
	// unknownPerWindow caps claims of unknown offer IDs node-wide.
	unknownPerWindow = 120

	macSize = 32
)

// Self describes the pairing node, captured when an offer is created so
// the QR, /client/v1/info, and the transcript always agree.
type Self struct {
	ClusterID string
	NodeID    string
	NodeName  string
	NodeKey   ed25519.PrivateKey
	CAFP      []byte
	Endpoints []Endpoint
}

// NodePub returns the node's public key.
func (s Self) NodePub() ed25519.PublicKey { return s.NodeKey.Public().(ed25519.PublicKey) }

// Info renders s as the /client/v1/info response.
func (s Self) Info() Info {
	eps := make([]InfoEndpoint, 0, len(s.Endpoints))
	for _, ep := range s.Endpoints {
		eps = append(eps, InfoEndpoint{Kind: ep.Kind.String(), Addr: ep.Addr})
	}
	return Info{
		ClientProto: ProtoVersion,
		ClusterID:   s.ClusterID,
		NodeID:      s.NodeID,
		NodeName:    s.NodeName,
		NodePub:     Bytes(s.NodePub()),
		CAFP:        Bytes(s.CAFP),
		Endpoints:   eps,
	}
}

// InfoPayload is the secretless kind 3 payload for s.
func (s Self) InfoPayload() (string, error) {
	return Payload{
		Kind: KindNodeInfo, ClusterID: s.ClusterID, NodeID: s.NodeID, NodeName: s.NodeName,
		NodePub: s.NodePub(), CAFP: s.CAFP, Endpoints: s.Endpoints, Proto: ProtoVersion,
	}.Encode()
}

// Extras supplies what an approved device learns besides its record.
type Extras struct {
	NATSURLs []string
	CAFPs    [][]byte
	Members  []MemberInfo
}

// Config wires a Manager to its node.
type Config struct {
	// Self returns the current node description.
	Self func() (Self, error)
	// OnApprove stores and replicates a newly approved device. The offer
	// only becomes approved if it succeeds.
	OnApprove func(Device) error
	// Revoked reports whether a device ID was revoked; such keys cannot
	// pair again (their record would lose to the revocation).
	Revoked func(deviceID string) bool
	// Extras returns the post-approval data for a device.
	Extras func() Extras
	// OnChange runs whenever the set of open or claimed offers changes
	// (created, approved, expired, aborted, rejected). It is called with
	// the manager lock held, so it must not block or call back in; the
	// server uses it to kick its listener gate.
	OnChange func()
	// Now overrides the clock (tests).
	Now func() time.Time
}

type offer struct {
	id      []byte
	kind    Kind
	secret  []byte
	key     []byte
	self    Self
	role    Role
	payload string
	pinned  ed25519.PublicKey

	state    State
	reason   string
	expires  time.Time
	endedAt  time.Time
	failures int

	transcript Transcript
	t1         []byte
	sas        SAS
	meta       DeviceMeta
	claimResp  ClaimResponse
	deviceOK   bool
	operatorOK bool
	approving  bool
	device     *Device

	changed chan struct{}
}

func (o *offer) signal() {
	close(o.changed)
	o.changed = make(chan struct{})
}

// Manager holds this node's pairing offers. Offers are local: a node
// offer's secret never leaves the node that created it.
type Manager struct {
	cfg Config

	mu      sync.Mutex
	offers  map[string]*offer
	sources map[string][]time.Time
	unknown []time.Time
	// lastEnded is when an offer last reached a terminal state.
	lastEnded time.Time
}

// NewManager creates an offer manager.
func NewManager(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Extras == nil {
		cfg.Extras = func() Extras { return Extras{} }
	}
	return &Manager{cfg: cfg, offers: map[string]*offer{}, sources: map[string][]time.Time{}}
}

func offerKey(id []byte) string { return base64.RawURLEncoding.EncodeToString(id) }

// ParseOfferID decodes an offer ID as used in local API paths.
func ParseOfferID(s string) ([]byte, error) {
	id, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(id) != OfferIDSize {
		return nil, pairErr(ErrCodeBadRequest, "invalid offer ID")
	}
	return id, nil
}

// refreshLocked expires stale offers and prunes old tombstones.
func (m *Manager) refreshLocked(now time.Time) {
	for k, o := range m.offers {
		if !o.state.Terminal() && !o.approving && now.After(o.expires) {
			m.endLocked(o, StateExpired, "", now)
		}
		if o.state.Terminal() && now.Sub(o.endedAt) > TombstoneTTL {
			delete(m.offers, k)
		}
	}
}

func (m *Manager) endLocked(o *offer, st State, reason string, now time.Time) {
	o.state, o.reason, o.endedAt = st, reason, now
	// The secret is single use. K stays until the tombstone is pruned so
	// the device can re-poll an approved offer.
	o.secret = nil
	o.signal()
	if st.Terminal() {
		m.lastEnded = now
	}
	m.changedLocked()
}

func (m *Manager) changedLocked() {
	if m.cfg.OnChange != nil {
		m.cfg.OnChange()
	}
}

// EndedGrace is how long the device listener stays wanted after the last
// offer ends, so a phone between polls still learns the final state
// instead of hitting a closed port.
const EndedGrace = 5 * time.Second

// Active reports how many offers are open or claimed, counting an offer
// that ended within EndedGrace (expiring stale ones first), and the next
// time that count can drop without an event. Offers being approved count
// but never set the deadline: they cannot expire, and finishing the
// approval notifies.
func (m *Manager) Active() (int, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.cfg.Now()
	m.refreshLocked(now)
	n, next := 0, time.Time{}
	later := func(t time.Time) {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	for _, o := range m.offers {
		if o.state.Terminal() {
			continue
		}
		n++
		if !o.approving {
			// refreshLocked expires strictly after the deadline.
			later(o.expires.Add(time.Nanosecond))
		}
	}
	if tail := m.lastEnded.Add(EndedGrace); n == 0 && !m.lastEnded.IsZero() && tail.After(now) {
		n = 1
		later(tail)
	}
	return n, next
}

func (m *Manager) activeLocked() int {
	n := 0
	for _, o := range m.offers {
		if !o.state.Terminal() {
			n++
		}
	}
	return n
}

func (m *Manager) addLocked(o *offer) error {
	m.refreshLocked(m.cfg.Now())
	if m.activeLocked() >= maxActiveOffers {
		return pairErr(ErrCodeRateLimited, "too many open pairing offers")
	}
	if cur, ok := m.offers[offerKey(o.id)]; ok && !cur.state.Terminal() {
		return pairErr(ErrCodeOfferUsed, "this pairing code is already registered")
	}
	o.changed = make(chan struct{})
	m.offers[offerKey(o.id)] = o
	m.changedLocked()
	return nil
}

// CreateNodeOffer creates a kind 1 offer (flow a).
func (m *Manager) CreateNodeOffer(ttl time.Duration, role Role) (OfferView, error) {
	if ttl <= 0 {
		ttl = DefaultOfferTTL
	}
	if ttl > MaxOfferTTL {
		return OfferView{}, pairErr(ErrCodeBadRequest, fmt.Sprintf("ttl exceeds %s", MaxOfferTTL))
	}
	self, err := m.cfg.Self()
	if err != nil {
		return OfferView{}, err
	}
	now := m.cfg.Now()
	o := &offer{
		id: RandomBytes(OfferIDSize), kind: KindNode, secret: RandomBytes(NodeSecretSize),
		self: self, role: role, state: StateOpen, expires: now.Add(ttl),
	}
	o.key = MACKey(o.secret, o.id)
	o.payload, err = Payload{
		Kind: KindNode, ClusterID: self.ClusterID, NodeID: self.NodeID, NodeName: self.NodeName,
		NodePub: self.NodePub(), CAFP: self.CAFP, Endpoints: self.Endpoints,
		OfferID: o.id, Secret: o.secret, Expires: o.expires, Proto: ProtoVersion,
	}.Encode()
	if err != nil {
		return OfferView{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.addLocked(o); err != nil {
		return OfferView{}, err
	}
	return m.viewLocked(o), nil
}

// AcceptDevice registers a phone's kind 2 payload or typed code (flow b).
func (m *Manager) AcceptDevice(input string, role Role) (OfferView, error) {
	p, err := ParseDeviceInput(input)
	if err != nil {
		return OfferView{}, pairErr(ErrCodeBadRequest, err.Error())
	}
	now := m.cfg.Now()
	expires := now.Add(DeviceOfferTTL)
	if !p.Expires.IsZero() {
		if !p.Expires.After(now) {
			return OfferView{}, pairErr(ErrCodeOfferExpired, "the phone's pairing code has expired")
		}
		expires = minTime(expires, p.Expires)
	}
	self, err := m.cfg.Self()
	if err != nil {
		return OfferView{}, err
	}
	o := &offer{
		id: OfferIDFromSecret(p.Secret), kind: KindDevice, secret: p.Secret,
		self: self, role: role, state: StateOpen, expires: expires, pinned: p.DevicePub,
	}
	o.key = MACKey(o.secret, o.id)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.addLocked(o); err != nil {
		return OfferView{}, err
	}
	return m.viewLocked(o), nil
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// lookupLocked finds a live offer, translating terminal states into the
// spec's error codes.
func (m *Manager) lookupLocked(id []byte) (*offer, error) {
	m.refreshLocked(m.cfg.Now())
	o, ok := m.offers[offerKey(id)]
	if !ok {
		return nil, pairErr(ErrCodeOfferUnknown, "unknown pairing offer")
	}
	switch o.state {
	case StateExpired:
		return o, pairErr(ErrCodeOfferExpired, "pairing offer expired")
	case StateAborted, StateRejected:
		return o, pairErr(ErrCodeOfferAborted, "pairing offer was aborted")
	}
	return o, nil
}

// failLocked records a MAC or signature failure.
func (m *Manager) failLocked(o *offer) error {
	o.failures++
	if o.failures >= maxFailures && !o.state.Terminal() && !o.approving {
		m.endLocked(o, StateAborted, ReasonBadMAC, m.cfg.Now())
	}
	return pairErr(ErrCodeMACInvalid, "pairing authentication failed")
}

// recordFailureLocked counts a failed claim from source and reports
// whether the source is still under its limit.
func (m *Manager) recordFailureLocked(source string, now time.Time) bool {
	recent := func(ts []time.Time) []time.Time {
		return slices.DeleteFunc(ts, func(t time.Time) bool { return now.Sub(t) >= claimWindow })
	}
	hits := recent(m.sources[source])
	if len(hits) >= claimsPerWindow {
		m.sources[source] = hits
		return false
	}
	if _, ok := m.sources[source]; !ok && len(m.sources) >= maxSources {
		m.pruneSourcesLocked(now, recent)
	}
	m.sources[source] = append(hits, now)
	return true
}

// unknownLimitLocked bounds node-wide probing of unknown offer IDs. It
// never affects claims of a live offer, so it cannot lock a phone out.
func (m *Manager) unknownLimitLocked(now time.Time) bool {
	m.unknown = slices.DeleteFunc(m.unknown, func(t time.Time) bool { return now.Sub(t) >= claimWindow })
	if len(m.unknown) >= unknownPerWindow {
		return false
	}
	m.unknown = append(m.unknown, now)
	return true
}

// SourceKey normalizes a client IP for rate limiting: IPv6 clients are
// grouped by /64, since one host can hold a whole /64.
func SourceKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// pruneSourcesLocked drops idle sources, then the least recently seen
// one, so the table stays bounded without refusing new sources.
func (m *Manager) pruneSourcesLocked(now time.Time, recent func([]time.Time) []time.Time) {
	oldestKey, oldest := "", now
	for k, ts := range m.sources {
		ts = recent(ts)
		if len(ts) == 0 {
			delete(m.sources, k)
			continue
		}
		m.sources[k] = ts
		if last := ts[len(ts)-1]; !last.After(oldest) {
			oldestKey, oldest = k, last
		}
	}
	if len(m.sources) >= maxSources {
		delete(m.sources, oldestKey)
	}
}

func checkLen(b []byte, n int, what string) error {
	if len(b) != n {
		return pairErr(ErrCodeBadRequest, "invalid "+what)
	}
	return nil
}

// Claim handles POST /client/v1/pair/claim from source (the client's
// address, used only for rate limiting).
func (m *Manager) Claim(source string, req ClaimRequest) (ClaimResponse, error) {
	for _, c := range []struct {
		b    []byte
		n    int
		what string
	}{
		{req.OfferID, OfferIDSize, "offer_id"},
		{req.DevicePub, KeySize, "device_pub"},
		{req.NonceDevice, NonceSize, "nonce_device"},
		{req.MAC, macSize, "mac"},
		{req.Sig, ed25519.SignatureSize, "sig"},
	} {
		if err := checkLen(c.b, c.n, c.what); err != nil {
			return ClaimResponse{}, err
		}
	}
	for _, s := range []string{req.DeviceName, req.Platform, req.Model, req.AppVersion} {
		if len(s) > maxTextBytes {
			return ClaimResponse{}, pairErr(ErrCodeBadRequest, "metadata field too long")
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.cfg.Now()
	// Only failed attempts are rate limited, so a valid claim always goes
	// through even if an attacker shares the phone's address range.
	limited := func(err error) error {
		if !m.recordFailureLocked(source, now) {
			return pairErr(ErrCodeRateLimited, "too many failed pairing attempts; wait a minute")
		}
		return err
	}
	o, err := m.lookupLocked(req.OfferID)
	if err != nil {
		if pe, ok := err.(*Error); ok && pe.Code == ErrCodeOfferUnknown && !m.unknownLimitLocked(now) {
			return ClaimResponse{}, pairErr(ErrCodeRateLimited, "too many pairing attempts; wait a minute")
		}
		return ClaimResponse{}, limited(err)
	}
	if o.state == StateApproved {
		return ClaimResponse{}, pairErr(ErrCodeOfferUsed, "pairing offer was already used")
	}

	tr := Transcript{
		Kind: o.kind, OfferID: o.id, ClusterID: o.self.ClusterID, NodeID: o.self.NodeID,
		NodePub: o.self.NodePub(), CAFP: o.self.CAFP,
		DevicePub: ed25519.PublicKey(clone(req.DevicePub)), NonceDevice: clone(req.NonceDevice),
	}
	msg := Message(LabelClaim, tr.T0())
	if !CheckMAC(o.key, msg, req.MAC) || !ed25519.Verify(tr.DevicePub, msg, req.Sig) ||
		(len(o.pinned) > 0 && !o.pinned.Equal(tr.DevicePub)) {
		return ClaimResponse{}, limited(m.failLocked(o))
	}
	if m.cfg.Revoked != nil && m.cfg.Revoked(DeviceID(tr.DevicePub)) {
		return ClaimResponse{}, pairErr(ErrCodeBadRequest, "this device key was revoked; reinstall rush or reset its key to pair again")
	}
	if o.state == StateClaimed && tr.DevicePub.Equal(o.transcript.DevicePub) &&
		hmac.Equal(tr.NonceDevice, o.transcript.NonceDevice) {
		// An exact retry (the response was lost) gets the same answer.
		return o.claimResp, nil
	}
	if o.state != StateOpen {
		// A second valid claim proves someone else holds the secret (a
		// leaked QR); the offer is burned. Only checked after the MAC so
		// knowing the offer ID alone cannot abort a pairing.
		if !o.approving {
			m.endLocked(o, StateAborted, ReasonDoubleClaim, now)
		}
		return ClaimResponse{}, pairErr(ErrCodeOfferUsed, "pairing offer was already used")
	}

	tr.NonceNode = RandomBytes(NonceSize)
	o.transcript, o.t1 = tr, tr.T1()
	o.sas = ComputeSAS(o.secret, o.t1)
	o.meta = DeviceMeta{
		DeviceID: DeviceID(tr.DevicePub), Name: req.DeviceName, Platform: req.Platform,
		Model: req.Model, AppVersion: req.AppVersion,
	}
	o.state = StateClaimed
	o.expires = now.Add(ClaimApprovalTTL)
	m.changedLocked()
	nodeMsg := Message(LabelNode, o.t1)
	o.claimResp = ClaimResponse{
		OfferID: o.id, State: StateClaimed, NonceNode: tr.NonceNode,
		MAC: MAC(o.key, nodeMsg), Sig: ed25519.Sign(o.self.NodeKey, nodeMsg), ExpiresAt: o.expires,
	}
	o.signal()
	return o.claimResp, nil
}

// claimedLocked looks up an offer that must be past the claim.
func (m *Manager) claimedLocked(id []byte) (*offer, error) {
	if err := checkLen(id, OfferIDSize, "offer_id"); err != nil {
		return nil, err
	}
	o, err := m.lookupLocked(id)
	if err != nil {
		return nil, err
	}
	if o.state == StateOpen {
		return nil, pairErr(ErrCodeBadRequest, "pairing offer has not been claimed")
	}
	return o, nil
}

// Confirm handles POST /client/v1/pair/confirm.
func (m *Manager) Confirm(req ConfirmRequest) (StateResponse, error) {
	m.mu.Lock()
	o, err := m.claimedLocked(req.OfferID)
	if err != nil {
		m.mu.Unlock()
		return StateResponse{}, err
	}
	msg := Message(LabelConfirm, o.t1)
	if !CheckMAC(o.key, msg, req.MAC) || !ed25519.Verify(o.transcript.DevicePub, msg, req.Sig) {
		err := m.failLocked(o)
		m.mu.Unlock()
		return StateResponse{}, err
	}
	if o.state == StateApproved {
		m.mu.Unlock()
		return StateResponse{OfferID: o.id, State: o.state}, nil
	}
	o.deviceOK = true
	d := m.prepareApprovalLocked(o)
	o.signal()
	m.mu.Unlock()
	if err := m.commitApproval(o, d); err != nil {
		return StateResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return StateResponse{OfferID: o.id, State: o.state}, nil
}

// Approve records the operator's approval of a claimed offer.
func (m *Manager) Approve(id []byte, role Role) (OfferView, error) {
	m.mu.Lock()
	o, err := m.claimedLocked(id)
	if err != nil {
		m.mu.Unlock()
		return OfferView{}, err
	}
	if o.state == StateApproved || o.approving {
		view := m.viewLocked(o)
		m.mu.Unlock()
		return view, nil
	}
	if role != "" {
		o.role = role
	}
	o.operatorOK = true
	d := m.prepareApprovalLocked(o)
	o.signal()
	m.mu.Unlock()
	if err := m.commitApproval(o, d); err != nil {
		return OfferView{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewLocked(o), nil
}

// prepareApprovalLocked builds the device record once both sides agreed
// and marks the offer as approving, which freezes it (no expiry, reject,
// or abort) until commitApproval finishes outside the lock.
func (m *Manager) prepareApprovalLocked(o *offer) *Device {
	if !o.deviceOK || !o.operatorOK || o.approving || o.state != StateClaimed {
		return nil
	}
	now := m.cfg.Now().UTC()
	d := Device{
		DeviceID: o.meta.DeviceID, Name: o.meta.Name, Platform: o.meta.Platform,
		Model: o.meta.Model, AppVersion: o.meta.AppVersion,
		PublicKey: clone(o.transcript.DevicePub), Role: o.role, Scopes: o.role.Scopes(),
		PairedAt: now, PairedVia: o.self.NodeID, ApprovedBy: o.self.NodeID,
		Rev: 1, UpdatedAt: now, UpdatedBy: o.self.NodeID,
	}
	if d.Name == "" {
		d.Name = d.DeviceID
	}
	o.approving = true
	return &d
}

// commitApproval stores the device (without holding the manager lock,
// since storing replicates and reloads NATS) and finishes the offer.
func (m *Manager) commitApproval(o *offer, d *Device) error {
	if d == nil {
		return nil
	}
	var err error
	if m.cfg.OnApprove != nil {
		err = m.cfg.OnApprove(*d)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o.approving = false
	if err != nil {
		o.operatorOK = false
		o.signal()
		m.changedLocked()
		return fmt.Errorf("store device: %w", err)
	}
	o.device = d
	m.endLocked(o, StateApproved, "", m.cfg.Now())
	return nil
}

// Reject ends an offer at the operator's request.
func (m *Manager) Reject(id []byte) (OfferView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, err := m.lookupLocked(id)
	if err != nil {
		return OfferView{}, err
	}
	if o.state == StateApproved || o.approving {
		return OfferView{}, pairErr(ErrCodeOfferUsed, "device already approved; revoke it instead")
	}
	m.endLocked(o, StateRejected, ReasonRejected, m.cfg.Now())
	return m.viewLocked(o), nil
}

// Abort handles POST /client/v1/pair/abort.
func (m *Manager) Abort(req AbortRequest) (StateResponse, error) {
	if req.Reason != ReasonSASMismatch && req.Reason != ReasonUserCancel {
		return StateResponse{}, pairErr(ErrCodeBadRequest, "invalid abort reason")
	}
	if err := checkLen(req.OfferID, OfferIDSize, "offer_id"); err != nil {
		return StateResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, err := m.lookupLocked(req.OfferID)
	if err != nil {
		return StateResponse{}, err
	}
	if o.state == StateApproved || o.approving {
		return StateResponse{}, pairErr(ErrCodeOfferUsed, "device already approved")
	}
	if !CheckMAC(o.key, AbortMessage(o.id, req.Reason), req.MAC) {
		return StateResponse{}, m.failLocked(o)
	}
	m.endLocked(o, StateAborted, req.Reason, m.cfg.Now())
	return StateResponse{OfferID: o.id, State: o.state}, nil
}

// Poll handles POST /client/v1/pair/poll, waiting up to wait for the
// offer to leave the claimed state.
func (m *Manager) Poll(ctx context.Context, req PollRequest) (PollResponse, error) {
	wait := min(time.Duration(max(req.WaitMS, 0))*time.Millisecond, MaxPollWait)
	m.mu.Lock()
	o, err := m.claimedLocked(req.OfferID)
	if err != nil {
		m.mu.Unlock()
		return PollResponse{}, err
	}
	if !CheckMAC(o.key, Message(LabelPoll, o.t1), req.MAC) {
		err := m.failLocked(o)
		m.mu.Unlock()
		return PollResponse{}, err
	}
	m.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		m.mu.Lock()
		m.refreshLocked(m.cfg.Now())
		st, ch := o.state, o.changed
		if st != StateClaimed {
			resp := m.pollResponseLocked(o)
			m.mu.Unlock()
			return resp, nil
		}
		m.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			m.mu.Lock()
			resp := m.pollResponseLocked(o)
			m.mu.Unlock()
			return resp, nil
		case <-ctx.Done():
			return PollResponse{}, ctx.Err()
		}
	}
}

func (m *Manager) pollResponseLocked(o *offer) PollResponse {
	resp := PollResponse{OfferID: o.id, State: o.state}
	if o.state == StateApproved && o.device != nil {
		d := *o.device
		ex := m.cfg.Extras()
		user, _ := NATSUser(d.PublicKey)
		resp.Device = &d
		resp.NATS = &NATSCredential{User: user, Account: ClientsAccount, Permissions: DevicePermissions(d.DeviceID), URLs: ex.NATSURLs}
		for _, fp := range ex.CAFPs {
			resp.CAFPs = append(resp.CAFPs, Bytes(fp))
		}
		resp.Members = ex.Members
	}
	return resp
}

// Get returns the local view of an offer.
func (m *Manager) Get(id []byte) (OfferView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshLocked(m.cfg.Now())
	o, ok := m.offers[offerKey(id)]
	if !ok {
		return OfferView{}, pairErr(ErrCodeOfferUnknown, "unknown pairing offer")
	}
	return m.viewLocked(o), nil
}

// Wait returns the offer once its state differs from since, or after
// wait.
func (m *Manager) Wait(ctx context.Context, id []byte, since State, wait time.Duration) (OfferView, error) {
	timer := time.NewTimer(min(wait, MaxPollWait))
	defer timer.Stop()
	for {
		m.mu.Lock()
		m.refreshLocked(m.cfg.Now())
		o, ok := m.offers[offerKey(id)]
		if !ok {
			m.mu.Unlock()
			return OfferView{}, pairErr(ErrCodeOfferUnknown, "unknown pairing offer")
		}
		view, ch := m.viewLocked(o), o.changed
		m.mu.Unlock()
		if view.State != since || view.State.Terminal() {
			return view, nil
		}
		// Expiry is lazy; wake up for it too, but only while it is ahead.
		if done, err := m.waitOnce(ctx, ch, timer.C, view.ExpiresAt); done {
			return view, err
		}
	}
}

// waitOnce blocks until the offer changes, its expiry passes, the overall
// wait ends (done), or ctx is cancelled (done with its error).
func (m *Manager) waitOnce(ctx context.Context, changed <-chan struct{}, deadline <-chan time.Time, expires time.Time) (bool, error) {
	var expiryC <-chan time.Time
	if left := expires.Sub(m.cfg.Now()); left > 0 {
		expiry := time.NewTimer(left + 10*time.Millisecond)
		defer expiry.Stop()
		expiryC = expiry.C
	}
	select {
	case <-changed:
	case <-expiryC:
	case <-deadline:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
	return false, nil
}

func (m *Manager) viewLocked(o *offer) OfferView {
	v := OfferView{
		OfferID: offerKey(o.id), Kind: o.kind, State: o.state, Reason: o.reason, Role: o.role,
		ExpiresAt: o.expires, DeviceOK: o.deviceOK, OperatorOK: o.operatorOK,
	}
	if o.state == StateOpen {
		v.Payload = o.payload
	}
	if o.kind == KindDevice {
		v.InfoPayload, _ = o.self.InfoPayload()
	}
	if o.t1 != nil {
		meta := o.meta
		v.Device = &meta
		v.SAS = &SASView{Names: o.sas.Names(), Digits: o.sas.DigitString()}
		for _, idx := range o.sas.Emoji {
			v.SAS.Emoji = append(v.SAS.Emoji, EmojiTable[idx].Glyph)
		}
	}
	return v
}

// ErrNotInCluster is returned by Self when the node has no cluster.
var ErrNotInCluster = errors.New("this node is not part of a cluster (run `crush cluster init` first)")
