package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/qr"
)

// Mesh subjects for the device registry (member-only, global account).
const (
	subjDevices        = "crush.devices"
	subjDevicesRequest = "crush.devices.request"
	// subjDevicesTo + node ID carries a registry announce for one node, so
	// a sync request does not fan out to every member.
	subjDevicesTo = "crush.devices.to."

	// DefaultClientPort is the default client listener port for devices.
	DefaultClientPort = 7733

	maxPairBody   = 16 << 10
	maxDeviceMsg  = 64 << 10
	qrPNGScale    = 8
	clientTimeout = 10 * time.Second
	// clientWriteTimeout must outlast the longest poll.
	clientWriteTimeout = pairing.MaxPollWait + 10*time.Second
	maxClientConns     = 256
	listenerRetry      = 30 * time.Second
	// clientStopGrace lets in-flight requests finish when the gate
	// unbinds the listener.
	clientStopGrace = 5 * time.Second
)

// devicesRequest asks peers to announce their registry when it differs.
type devicesRequest struct {
	NodeID string `json:"node_id"`
	Digest string `json:"digest"`
}

// pairingService owns mobile pairing on this node: offers, the device
// registry, its mesh replication, and the client listener.
type pairingService struct {
	srv  *Server
	node *cluster.Node
	mgr  *pairing.Manager
	reg  *pairing.Registry

	clientAddr string

	// meshRef is the running mesh, set and cleared by startMesh/stopMesh.
	// It is read without srv.meshMu so NATS callbacks and registry
	// watchers never wait on the server's mesh lock.
	meshRef atomic.Pointer[cluster.Mesh]
	// natsDirty coalesces NATS user reloads onto the apply goroutine.
	natsDirty chan struct{}

	// gate binds the client listener only while an offer is open or an
	// active device exists. It lives while the mesh is attached.
	gate   atomic.Pointer[listenerGate]
	client *httpListener

	mu   sync.Mutex
	subs []*nats.Subscription
	stop context.CancelFunc
	wg   sync.WaitGroup
}

func newPairingService(s *Server, n *cluster.Node) (*pairingService, error) {
	reg, err := pairing.OpenRegistry(n.Store().Dir())
	if err != nil {
		return nil, err
	}
	p := &pairingService{srv: s, node: n, reg: reg, clientAddr: s.clientAddr, natsDirty: make(chan struct{}, 1)}
	p.mgr = pairing.NewManager(pairing.Config{
		Self: p.self, OnApprove: p.onApprove, Extras: p.extras, Revoked: p.revoked,
		OnChange: p.kickGate,
	})
	reg.Watch(func(pairing.Device) {
		p.markNATSDirty()
		// A revocation (local or replicated) may leave nothing to serve.
		p.kickGate()
	})
	p.client = &httpListener{
		name: "client", addr: p.clientAddr, maxConn: maxClientConns, grace: clientStopGrace, backoff: listenerRetry,
		onDown: p.kickGate,
		server: func() *http.Server {
			return &http.Server{
				Handler:           p.clientHandler(),
				TLSConfig:         n.ClientTLSConfig(),
				ReadHeaderTimeout: clientTimeout,
				ReadTimeout:       clientTimeout,
				WriteTimeout:      clientWriteTimeout,
				IdleTimeout:       serverIdleTimeout,
				MaxHeaderBytes:    maxPairBody,
			}
		},
	}
	return p, nil
}

func (p *pairingService) self() (pairing.Self, error) {
	st, err := p.node.Store().Snapshot()
	if err != nil {
		return pairing.Self{}, err
	}
	if st.ClusterID == "" {
		return pairing.Self{}, pairing.ErrNotInCluster
	}
	fps, err := p.node.CAFingerprints()
	if err != nil {
		return pairing.Self{}, err
	}
	id := p.node.Identity()
	var eps []cluster.Endpoint
	if m, ok := st.Member(id.ID); ok {
		eps = m.Endpoints
	}
	return pairing.Self{
		// CAFingerprints lists the current key first (TestCAFingerprintOrder).
		ClusterID: st.ClusterID, NodeID: id.ID, NodeName: id.Name, NodeKey: id.PrivateKey(),
		CAFP: fps[0], Endpoints: clientEndpoints(eps, portOf(p.clientAddr, DefaultClientPort)),
	}, nil
}

// clientEndpoints maps a member's direct endpoints to client listener
// endpoints, typed by address range. Hosts a phone cannot use (empty,
// unspecified, loopback) are skipped.
func clientEndpoints(eps []cluster.Endpoint, port int) []pairing.Endpoint {
	var out []pairing.Endpoint
	seen := map[string]bool{}
	tailnet := netip.MustParsePrefix("100.64.0.0/10")
	for _, ep := range eps {
		if ep.Kind != cluster.EndpointDirect {
			continue
		}
		host, _, err := net.SplitHostPort(ep.Addr)
		if err != nil || host == "" || host == "localhost" || seen[host] {
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
		out = append(out, pairing.Endpoint{Kind: kind, Addr: net.JoinHostPort(host, strconv.Itoa(port))})
	}
	return out
}

func (p *pairingService) kickGate() {
	if g := p.gate.Load(); g != nil {
		g.Kick()
	}
}

// listenersWanted is the gating rule: the device-facing listeners are
// bound only while a pairing offer is open or claimed, or at least one
// paired device is not revoked. The second value is the next offer
// expiry, when the answer may change without any event.
func (p *pairingService) listenersWanted() (bool, time.Time) {
	n, next := p.mgr.Active()
	return n > 0 || p.reg.HasActive(), next
}

func (p *pairingService) revoked(deviceID string) bool {
	d, ok := p.reg.Get(deviceID)
	return ok && d.Revoked()
}

func (p *pairingService) onApprove(d pairing.Device) error {
	if cur, ok := p.reg.Get(d.DeviceID); ok {
		if cur.Revoked() {
			return errors.New("this device key was revoked and cannot pair again")
		}
		// Re-pairing an active device updates its record.
		d.Rev, d.PairedAt = cur.Rev+1, cur.PairedAt
	}
	changed, err := p.reg.Merge(d)
	if err != nil {
		return err
	}
	if !changed {
		return errors.New("device record was superseded; try again")
	}
	p.publish(subjDevices, d)
	slog.Info("Paired mobile device", "device", d.DeviceID, "name", d.Name, "role", d.Role)
	return nil
}

func (p *pairingService) extras() pairing.Extras {
	var ex pairing.Extras
	if fps, err := p.node.CAFingerprints(); err == nil {
		ex.CAFPs = fps
	}
	st, err := p.node.Store().Snapshot()
	if err != nil {
		return ex
	}
	port := portOf(p.clientAddr, DefaultClientPort)
	for _, m := range st.Members {
		info := pairing.MemberInfo{NodeID: m.NodeID, Name: m.Name, NodePub: m.PublicKey}
		for _, ep := range clientEndpoints(m.Endpoints, port) {
			info.Endpoints = append(info.Endpoints, pairing.InfoEndpoint{Kind: ep.Kind.String(), Addr: ep.Addr})
		}
		ex.Members = append(ex.Members, info)
	}
	// The device-facing NATS websocket is rollout phase 2, so no URLs are
	// advertised yet; the scoped user is already installed.
	return ex
}

func (p *pairingService) markNATSDirty() {
	select {
	case p.natsDirty <- struct{}{}:
	default:
	}
}

// applyNATS installs every non-revoked device as a scoped NATS user and
// drops revoked ones (which disconnects them). Only the apply goroutine
// calls it.
func (p *pairingService) applyNATS(mesh *cluster.Mesh) {
	var users []cluster.ClientUser
	for _, d := range p.reg.List() {
		if d.Revoked() {
			continue
		}
		nkey, err := pairing.NATSUser(d.PublicKey)
		if err != nil {
			continue
		}
		perms := pairing.DevicePermissions(d.DeviceID)
		users = append(users, cluster.ClientUser{Nkey: nkey, Publish: perms.Publish, Subscribe: perms.Subscribe})
	}
	if err := mesh.SetClientUsers(users); err != nil {
		slog.Warn("Failed to install device NATS users", "error", err)
	}
}

func (p *pairingService) publish(subject string, d pairing.Device) {
	mesh := p.meshRef.Load()
	if mesh == nil {
		return
	}
	data, err := json.Marshal(d)
	if err != nil {
		return
	}
	if err := mesh.Conn().Publish(subject, data); err != nil {
		slog.Warn("Failed to publish device record", "device", d.DeviceID, "error", err)
	}
}

func (p *pairingService) requestSync(mesh *cluster.Mesh) {
	data, _ := json.Marshal(devicesRequest{NodeID: p.node.Identity().ID, Digest: p.reg.Digest()})
	_ = mesh.Conn().Publish(subjDevicesRequest, data)
}

func (p *pairingService) receive(m *nats.Msg) {
	if len(m.Data) > maxDeviceMsg {
		return
	}
	var d pairing.Device
	if err := json.Unmarshal(m.Data, &d); err != nil {
		return
	}
	changed, err := p.reg.Merge(d)
	if err != nil {
		slog.Warn("Ignored invalid device record from peer", "error", err)
		return
	}
	if changed {
		slog.Info("Applied device record from cluster peer", "device", d.DeviceID, "revoked", d.Revoked())
	}
}

// receiveRequest answers a peer whose registry differs by sending it our
// records directly.
func (p *pairingService) receiveRequest(m *nats.Msg) {
	var req devicesRequest
	if json.Unmarshal(m.Data, &req) != nil || req.NodeID == p.node.Identity().ID || req.NodeID == "" {
		return
	}
	if req.Digest == p.reg.Digest() {
		return
	}
	for _, d := range p.reg.List() {
		p.publish(subjDevicesTo+req.NodeID, d)
	}
}

// startMesh subscribes to the registry subjects, installs device users,
// and starts the client listener. Called once the mesh is up.
func (p *pairingService) startMesh(mesh *cluster.Mesh) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop != nil {
		return errors.New("pairing is already attached to a mesh")
	}
	if st, err := p.node.Store().Snapshot(); err == nil {
		if err := p.reg.BindCluster(st.ClusterID); err != nil {
			return err
		}
	}
	subjects := map[string]func(*nats.Msg){
		subjDevices:                          p.receive,
		subjDevicesTo + p.node.Identity().ID: p.receive,
		subjDevicesRequest:                   p.receiveRequest,
	}
	for subj, fn := range subjects {
		sub, err := mesh.Subscribe(subj, fn)
		if err != nil {
			for _, s := range p.subs {
				_ = s.Unsubscribe()
			}
			p.subs = nil
			return err
		}
		p.subs = append(p.subs, sub)
	}
	p.meshRef.Store(mesh)
	ctx, cancel := context.WithCancel(context.Background())
	p.stop = cancel
	p.markNATSDirty()
	p.wg.Go(func() { p.applyLoop(ctx, mesh) })
	p.wg.Go(func() { p.syncLoop(ctx, mesh) })
	if p.clientAddr != "" {
		gate := newListenerGate(p.listenersWanted, listenerRetry, p.client)
		p.gate.Store(gate)
		p.wg.Go(func() { gate.run(ctx) })
	}
	return nil
}

// applyLoop serializes NATS user reloads.
func (p *pairingService) applyLoop(ctx context.Context, mesh *cluster.Mesh) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.natsDirty:
			p.applyNATS(mesh)
		}
	}
}

// syncLoop asks peers for their registry a few times (routes take a
// moment after start or join), then keeps reconciling periodically.
func (p *pairingService) syncLoop(ctx context.Context, mesh *cluster.Mesh) {
	waits := []time.Duration{0, 2 * time.Second, 8 * time.Second, 20 * time.Second}
	for i := 0; ; i++ {
		wait := listenerRetry
		if i < len(waits) {
			wait = waits[i]
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		// Keep reconciling after start-up too, so a missed publish (a
		// partition or route flap) cannot leave a revoked device trusted.
		p.requestSync(mesh)
	}
}

func (p *pairingService) stopMesh() {
	p.mu.Lock()
	for _, sub := range p.subs {
		_ = sub.Unsubscribe()
	}
	p.subs = nil
	if p.stop != nil {
		p.stop()
		p.stop = nil
	}
	p.meshRef.Store(nil)
	p.gate.Store(nil)
	p.mu.Unlock()
	// The gate goroutine stops the client listener on cancel.
	p.wg.Wait()
}

// clientHandler serves /client/v1 for devices.
func (p *pairingService) clientHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /client/v1/info", p.handleInfo)
	mux.HandleFunc("POST /client/v1/pair/claim", p.handleClaim)
	mux.HandleFunc("POST /client/v1/pair/confirm", p.handleConfirm)
	mux.HandleFunc("POST /client/v1/pair/poll", p.handlePoll)
	mux.HandleFunc("POST /client/v1/pair/abort", p.handleAbort)
	return mux
}

// remoteIP is the client's address from the connection itself, grouped
// for rate limiting; forwarded headers are ignored because the listener
// is never behind a proxy.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return pairing.SourceKey(host)
}

func writePairError(w http.ResponseWriter, err error) {
	var pe *pairing.Error
	switch {
	case errors.As(err, &pe):
	case errors.Is(err, pairing.ErrNotInCluster), errors.Is(err, cluster.ErrNotInCluster):
		pe = &pairing.Error{Code: pairing.ErrCodeNotInCluster, Message: err.Error()}
	default:
		slog.Warn("Pairing request failed", "error", err)
		pe = &pairing.Error{Code: pairing.ErrCodeInternal, Message: "internal error"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(pe.Status())
	_ = json.NewEncoder(w).Encode(pe)
}

func decodePair[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	// Unknown fields are ignored so newer rush builds stay compatible.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPairBody)).Decode(&v); err != nil {
		writePairError(w, &pairing.Error{Code: pairing.ErrCodeBadRequest, Message: "invalid request body"})
		return v, false
	}
	return v, true
}

// handleInfo describes this node to devices.
//
//	@Summary		Client node info
//	@Description	Device client listener (TLS, default :7733); the path is absolute, not under the /v1 base path.
//	@Tags			client
//	@Produce		json
//	@Success		200	{object}	pairing.Info
//	@Failure		400	{object}	pairing.Error
//	@Router			/client/v1/info [get]
func (p *pairingService) handleInfo(w http.ResponseWriter, _ *http.Request) {
	self, err := p.self()
	if err != nil {
		writePairError(w, err)
		return
	}
	jsonEncode(w, self.Info())
}

// handleClaim claims a pairing offer.
//
//	@Summary		Claim a pairing offer
//	@Description	Device client listener (TLS, default :7733); the path is absolute, not under the /v1 base path.
//	@Tags			client
//	@Accept			json
//	@Produce		json
//	@Param			request	body		pairing.ClaimRequest	true	"Claim"
//	@Success		200		{object}	pairing.ClaimResponse
//	@Failure		400		{object}	pairing.Error
//	@Failure		401		{object}	pairing.Error
//	@Failure		404		{object}	pairing.Error
//	@Failure		409		{object}	pairing.Error
//	@Failure		410		{object}	pairing.Error
//	@Failure		429		{object}	pairing.Error
//	@Router			/client/v1/pair/claim [post]
func (p *pairingService) handleClaim(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePair[pairing.ClaimRequest](w, r)
	if !ok {
		return
	}
	resp, err := p.mgr.Claim(remoteIP(r), req)
	if err != nil {
		writePairError(w, err)
		return
	}
	jsonEncode(w, resp)
}

// handleConfirm records that the user confirmed the SAS on the device.
//
//	@Summary		Confirm the SAS
//	@Description	Device client listener (TLS, default :7733); the path is absolute, not under the /v1 base path.
//	@Tags			client
//	@Accept			json
//	@Produce		json
//	@Param			request	body		pairing.ConfirmRequest	true	"Confirm"
//	@Success		200		{object}	pairing.StateResponse
//	@Failure		400		{object}	pairing.Error
//	@Failure		401		{object}	pairing.Error
//	@Failure		410		{object}	pairing.Error
//	@Router			/client/v1/pair/confirm [post]
func (p *pairingService) handleConfirm(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePair[pairing.ConfirmRequest](w, r)
	if !ok {
		return
	}
	resp, err := p.mgr.Confirm(req)
	if err != nil {
		writePairError(w, err)
		return
	}
	jsonEncode(w, resp)
}

// handlePoll long-polls for the pairing outcome.
//
//	@Summary		Poll the pairing outcome
//	@Description	Device client listener (TLS, default :7733); the path is absolute, not under the /v1 base path.
//	@Tags			client
//	@Accept			json
//	@Produce		json
//	@Param			request	body		pairing.PollRequest	true	"Poll"
//	@Success		200		{object}	pairing.PollResponse
//	@Failure		400		{object}	pairing.Error
//	@Failure		401		{object}	pairing.Error
//	@Failure		410		{object}	pairing.Error
//	@Router			/client/v1/pair/poll [post]
func (p *pairingService) handlePoll(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePair[pairing.PollRequest](w, r)
	if !ok {
		return
	}
	resp, err := p.mgr.Poll(r.Context(), req)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writePairError(w, err)
		return
	}
	jsonEncode(w, resp)
}

// handleAbort aborts an offer (SAS mismatch or cancel).
//
//	@Summary		Abort pairing
//	@Description	Device client listener (TLS, default :7733); the path is absolute, not under the /v1 base path.
//	@Tags			client
//	@Accept			json
//	@Produce		json
//	@Param			request	body		pairing.AbortRequest	true	"Abort"
//	@Success		200		{object}	pairing.StateResponse
//	@Failure		400		{object}	pairing.Error
//	@Failure		401		{object}	pairing.Error
//	@Failure		409		{object}	pairing.Error
//	@Router			/client/v1/pair/abort [post]
func (p *pairingService) handleAbort(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePair[pairing.AbortRequest](w, r)
	if !ok {
		return
	}
	resp, err := p.mgr.Abort(req)
	if err != nil {
		writePairError(w, err)
		return
	}
	if req.Reason == pairing.ReasonSASMismatch {
		slog.Warn("Device reported a pairing code mismatch: possible interception", "offer", base64.RawURLEncoding.EncodeToString(req.OfferID))
	}
	jsonEncode(w, resp)
}

func (s *Server) installPairingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/cluster/pairing/offers", s.handleCreatePairOffer)
	mux.HandleFunc("POST /v1/cluster/pairing/accept", s.handleAcceptPairCode)
	mux.HandleFunc("GET /v1/cluster/pairing/offers/{offer_id}", s.handleGetPairOffer)
	mux.HandleFunc("GET /v1/cluster/pairing/offers/{offer_id}/qr.png", s.handleGetPairOfferQR)
	mux.HandleFunc("POST /v1/cluster/pairing/offers/{offer_id}/approve", s.handleApprovePairOffer)
	mux.HandleFunc("POST /v1/cluster/pairing/offers/{offer_id}/reject", s.handleRejectPairOffer)
	mux.HandleFunc("GET /v1/cluster/devices", s.handleListDevices)
	mux.HandleFunc("POST /v1/cluster/devices/{device_id}/revoke", s.handleRevokeDevice)
}

func (s *Server) requirePairing(w http.ResponseWriter) (*pairingService, bool) {
	if s.pairing == nil {
		jsonError(w, http.StatusNotImplemented, "clustering is not enabled on this server")
		return nil, false
	}
	return s.pairing, true
}

func (s *Server) pairOffer(w http.ResponseWriter, r *http.Request) (*pairingService, []byte, bool) {
	p, ok := s.requirePairing(w)
	if !ok {
		return nil, nil, false
	}
	id, err := pairing.ParseOfferID(r.PathValue("offer_id"))
	if err != nil {
		writePairError(w, err)
		return nil, nil, false
	}
	return p, id, true
}

// handleCreatePairOffer creates a node pairing offer (flow a).
//
//	@Summary		Create a pairing offer
//	@Tags			pairing
//	@Accept			json
//	@Produce		json
//	@Param			request	body		pairing.CreateOfferRequest	false	"Offer options"
//	@Success		200		{object}	pairing.OfferView
//	@Failure		400		{object}	pairing.Error
//	@Failure		409		{object}	pairing.Error
//	@Failure		429		{object}	pairing.Error
//	@Router			/cluster/pairing/offers [post]
func (s *Server) handleCreatePairOffer(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePairing(w)
	if !ok {
		return
	}
	req, ok := decodeBody[pairing.CreateOfferRequest](w, r)
	if !ok {
		return
	}
	role, err := pairing.ParseRole(req.Role)
	if err != nil {
		writePairError(w, &pairing.Error{Code: pairing.ErrCodeBadRequest, Message: err.Error()})
		return
	}
	view, err := p.mgr.CreateNodeOffer(time.Duration(req.TTLSeconds)*time.Second, role)
	if err != nil {
		writePairError(w, err)
		return
	}
	jsonEncode(w, view)
}

// handleAcceptPairCode registers a phone's payload or typed code (flow b).
//
//	@Summary		Accept a device pairing code
//	@Tags			pairing
//	@Accept			json
//	@Produce		json
//	@Param			request	body		pairing.AcceptRequest	true	"Code or payload"
//	@Success		200		{object}	pairing.OfferView
//	@Failure		400		{object}	pairing.Error
//	@Failure		409		{object}	pairing.Error
//	@Failure		410		{object}	pairing.Error
//	@Router			/cluster/pairing/accept [post]
func (s *Server) handleAcceptPairCode(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePairing(w)
	if !ok {
		return
	}
	req, ok := decodeBody[pairing.AcceptRequest](w, r)
	if !ok {
		return
	}
	role, err := pairing.ParseRole(req.Role)
	if err != nil {
		writePairError(w, &pairing.Error{Code: pairing.ErrCodeBadRequest, Message: err.Error()})
		return
	}
	view, err := p.mgr.AcceptDevice(req.Code, role)
	if err != nil {
		writePairError(w, err)
		return
	}
	jsonEncode(w, view)
}

// handleGetPairOffer returns an offer, optionally waiting for a change.
//
//	@Summary		Get a pairing offer
//	@Tags			pairing
//	@Produce		json
//	@Param			offer_id	path		string	true	"Offer ID (base64url)"
//	@Param			state		query		string	false	"Wait until the state differs from this"
//	@Param			wait_ms		query		int		false	"Maximum wait (≤ 30000)"
//	@Success		200			{object}	pairing.OfferView
//	@Failure		400			{object}	pairing.Error
//	@Failure		404			{object}	pairing.Error
//	@Router			/cluster/pairing/offers/{offer_id} [get]
func (s *Server) handleGetPairOffer(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.pairOffer(w, r)
	if !ok {
		return
	}
	since := pairing.State(r.URL.Query().Get("state"))
	waitMS, _ := strconv.Atoi(r.URL.Query().Get("wait_ms"))
	var view pairing.OfferView
	var err error
	if since != "" && waitMS > 0 {
		view, err = p.mgr.Wait(r.Context(), id, since, time.Duration(waitMS)*time.Millisecond)
	} else {
		view, err = p.mgr.Get(id)
	}
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writePairError(w, err)
		return
	}
	jsonEncode(w, view)
}

// handleGetPairOfferQR renders an open offer's payload as a PNG.
//
//	@Summary		Pairing offer QR code
//	@Tags			pairing
//	@Produce		png
//	@Param			offer_id	path	string	true	"Offer ID (base64url)"
//	@Success		200
//	@Failure		404	{object}	pairing.Error
//	@Failure		409	{object}	pairing.Error
//	@Router			/cluster/pairing/offers/{offer_id}/qr.png [get]
func (s *Server) handleGetPairOfferQR(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.pairOffer(w, r)
	if !ok {
		return
	}
	view, err := p.mgr.Get(id)
	if err != nil {
		writePairError(w, err)
		return
	}
	text := view.Payload
	if text == "" {
		text = view.InfoPayload
	}
	if text == "" {
		writePairError(w, &pairing.Error{Code: pairing.ErrCodeOfferUsed, Message: "offer is no longer open"})
		return
	}
	code, err := qr.Encode([]byte(text), qr.Medium)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_ = code.WritePNG(w, qrPNGScale)
}

// handleApprovePairOffer approves a claimed offer after the SAS matched.
//
//	@Summary		Approve a pairing offer
//	@Tags			pairing
//	@Accept			json
//	@Produce		json
//	@Param			offer_id	path		string					true	"Offer ID (base64url)"
//	@Param			request		body		pairing.ApproveRequest	false	"Role"
//	@Success		200			{object}	pairing.OfferView
//	@Failure		400			{object}	pairing.Error
//	@Failure		404			{object}	pairing.Error
//	@Failure		410			{object}	pairing.Error
//	@Router			/cluster/pairing/offers/{offer_id}/approve [post]
func (s *Server) handleApprovePairOffer(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.pairOffer(w, r)
	if !ok {
		return
	}
	req, ok := decodeBody[pairing.ApproveRequest](w, r)
	if !ok {
		return
	}
	var role pairing.Role
	if req.Role != "" {
		var err error
		if role, err = pairing.ParseRole(req.Role); err != nil {
			writePairError(w, &pairing.Error{Code: pairing.ErrCodeBadRequest, Message: err.Error()})
			return
		}
	}
	view, err := p.mgr.Approve(id, role)
	if err != nil {
		writePairError(w, err)
		return
	}
	jsonEncode(w, view)
}

// handleRejectPairOffer rejects an offer (SAS mismatch on the node).
//
//	@Summary		Reject a pairing offer
//	@Tags			pairing
//	@Produce		json
//	@Param			offer_id	path		string	true	"Offer ID (base64url)"
//	@Success		200			{object}	pairing.OfferView
//	@Failure		404			{object}	pairing.Error
//	@Failure		409			{object}	pairing.Error
//	@Router			/cluster/pairing/offers/{offer_id}/reject [post]
func (s *Server) handleRejectPairOffer(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.pairOffer(w, r)
	if !ok {
		return
	}
	view, err := p.mgr.Reject(id)
	if err != nil {
		writePairError(w, err)
		return
	}
	jsonEncode(w, view)
}

// handleListDevices lists paired devices.
//
//	@Summary		List paired devices
//	@Tags			devices
//	@Produce		json
//	@Success		200	{array}		pairing.Device
//	@Failure		501	{object}	proto.Error
//	@Router			/cluster/devices [get]
func (s *Server) handleListDevices(w http.ResponseWriter, _ *http.Request) {
	p, ok := s.requirePairing(w)
	if !ok {
		return
	}
	jsonEncode(w, p.reg.List())
}

// handleRevokeDevice revokes a device on every node.
//
//	@Summary		Revoke a paired device
//	@Tags			devices
//	@Produce		json
//	@Param			device_id	path		string	true	"Device ID"
//	@Success		200			{object}	pairing.Device
//	@Failure		404			{object}	proto.Error
//	@Router			/cluster/devices/{device_id}/revoke [post]
func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePairing(w)
	if !ok {
		return
	}
	d, err := p.reg.Revoke(r.PathValue("device_id"), p.node.Identity().ID, time.Now())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, pairing.ErrDeviceNotFound) {
			status = http.StatusNotFound
		}
		jsonError(w, status, err.Error())
		return
	}
	p.publish(subjDevices, d)
	slog.Info("Revoked mobile device", "device", d.DeviceID, "name", d.Name)
	jsonEncode(w, d)
}
