package cluster

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// DefaultRoutePort is the default TCP port for the NATS mesh routes.
const DefaultRoutePort = DefaultPort + 1

// Mesh is this node's embedded NATS server, clustered with every member
// over mutually authenticated TLS routes. There is no central server:
// every node runs one and routes to all others, so any subset of nodes
// keeps working when others are offline. Local code talks to it through
// an in-process connection; the client port is loopback-only and
// token-protected.
type Mesh struct {
	node *Node

	mu     sync.Mutex
	srv    *natsserver.Server
	opts   *natsserver.Options
	nc     *nats.Conn
	bc     *nats.Conn
	ec     *nats.Conn
	routes []string
	wsAddr string
	stop   chan struct{}
	// redialed records when a peer's route was last re-solicited.
	redialed map[string]time.Time

	devMu   sync.RWMutex
	devices map[string]string
	conns   *deviceConns
	// evict closes a client connection (a seam for tests).
	evict func(cid uint64) error
}

// StartMesh starts the embedded NATS server with its route listener on
// routeAddr (host:port) and connects to every known member.
func (n *Node) StartMesh(routeAddr string) (*Mesh, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil, err
	}
	host, portStr, err := net.SplitHostPort(routeAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid mesh address %q: %w", routeAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid mesh port %q: %w", portStr, err)
	}
	if host == "" {
		host = "0.0.0.0"
	}

	m := &Mesh{node: n, conns: newDeviceConns(DefaultDeviceMaxConns)}
	clientToken := rand.Text()
	bridgeToken := rand.Text()
	eventsToken := rand.Text()
	clients := natsserver.NewAccount(ClientsAccount)
	sysAcc := natsserver.NewAccount(natsserver.DEFAULT_SYSTEM_ACCOUNT)
	opts := &natsserver.Options{
		ServerName: n.id.ID,
		// Routing only starts once the client listener is up, so bind it to
		// loopback on a random port and lock it with a random token. Local
		// code still connects in-process.
		Host: "127.0.0.1",
		Port: natsserver.RANDOM_PORT,
		// The mesh connection is a password user in the global account;
		// paired devices are nkey users in the CLIENTS account
		// (SetClientUsers).
		// The mesh user may only connect in-process, so neither the
		// loopback client port nor the device websocket can use it.
		Users: []*natsserver.User{{
			Username: meshUser, Password: clientToken,
			AllowedConnectionTypes: connTypes(jwt.ConnectionTypeInProcess),
		}, {
			// The device bridge serves device subjects from inside the
			// CLIENTS account, in-process only.
			Username: bridgeUser, Password: bridgeToken, Account: clients,
			AllowedConnectionTypes: connTypes(jwt.ConnectionTypeInProcess),
			Permissions:            bridgePermissions(),
		}, {
			// Watches device connection advisories for logging.
			Username: eventsUser, Password: eventsToken, Account: sysAcc,
			AllowedConnectionTypes: connTypes(jwt.ConnectionTypeInProcess),
			Permissions: &natsserver.Permissions{
				Publish:   &natsserver.SubjectPermission{Deny: []string{">"}},
				Subscribe: &natsserver.SubjectPermission{Allow: []string{"$SYS.ACCOUNT.*.CONNECT", "$SYS.ACCOUNT.*.DISCONNECT", "$SYS.SERVER.*.CLIENT.AUTH.ERR"}},
			},
		}},
		Accounts:      []*natsserver.Account{clients, sysAcc},
		SystemAccount: natsserver.DEFAULT_SYSTEM_ACCOUNT,
		// Bridged API replies can be large (message lists).
		MaxPayload: maxMeshPayload,
		NoSigs:     true,
		// Device websocket: plain (no TLS) on loopback only. The public,
		// gated, TLS-terminating listener in front of it is the server's
		// job (websocket settings cannot be changed by config reload, so
		// gating happens there). Only device users may use it.
		Websocket: natsserver.WebsocketOpts{
			Host:             "127.0.0.1",
			Port:             natsserver.RANDOM_PORT,
			NoTLS:            true,
			HandshakeTimeout: wsHandshakeTimeout,
		},
		Cluster: natsserver.ClusterOpts{
			Name:       "crush-" + st.ClusterID,
			Host:       host,
			Port:       port,
			TLSConfig:  n.meshTLSConfig(),
			TLSTimeout: 5,
			// Members already know each other; gossiped peers could be
			// unapproved addresses.
			NoAdvertise:    true,
			ConnectRetries: -1,
		},
		Routes: m.routeURLs(st),
	}
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("create mesh server: %w", err)
	}
	srv.SetLoggerV2(newNATSLogger(nil, n.id.Name, false), false, false, false)
	go srv.Start()
	if !waitMeshReady(srv, 10*time.Second) {
		srv.Shutdown()
		return nil, errors.New("mesh server did not become ready")
	}
	nc, err := nats.Connect("", nats.InProcessServer(srv), nats.UserInfo(meshUser, clientToken), nats.Name("crush-"+n.id.Name), nats.MaxReconnects(-1))
	if err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("connect mesh: %w", err)
	}
	// The server writes the chosen websocket port back into opts (it
	// keeps our pointer); record it so later reloads compare equal.
	m.wsAddr = net.JoinHostPort(opts.Websocket.Host, strconv.Itoa(opts.Websocket.Port))
	bc, err := nats.Connect("", nats.InProcessServer(srv), nats.UserInfo(bridgeUser, bridgeToken), nats.Name("crush-bridge-"+n.id.Name), nats.MaxReconnects(-1), nats.ErrorHandler(slowConsumerHandler(n.id.Name, "device-bridge")))
	if err != nil {
		nc.Close()
		srv.Shutdown()
		return nil, fmt.Errorf("connect device bridge: %w", err)
	}
	ec, err := nats.Connect("", nats.InProcessServer(srv), nats.UserInfo(eventsUser, eventsToken), nats.Name("crush-events-"+n.id.Name), nats.MaxReconnects(-1))
	if err == nil {
		err = m.watchDeviceEvents(ec, srv.ID())
	}
	if err != nil {
		slog.Warn("Device connection events unavailable", "error", err)
	}
	m.ec = ec
	m.bc = bc
	m.srv, m.opts, m.nc = srv, opts, nc
	m.routes = urlStrings(opts.Routes)
	m.stop = make(chan struct{})
	m.redialed = map[string]time.Time{}
	go m.redialLoop(m.stop)
	n.store.Watch(func(State) { m.Reconcile() })
	slog.Info("Cluster mesh started", "routes", len(opts.Routes), "listen", net.JoinHostPort(host, portStr))
	return m, nil
}

// Conn returns the in-process NATS connection.
func (m *Mesh) Conn() *nats.Conn { return m.nc }

// Node returns the cluster node this mesh belongs to.
func (m *Mesh) Node() *Node { return m.node }

// NumRoutes reports how many peer routes are currently connected.
func (m *Mesh) NumRoutes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil {
		return 0
	}
	return m.srv.NumRoutes()
}

// Reconcile updates the route set after membership changes.
func (m *Mesh) Reconcile() {
	st, err := m.node.store.Snapshot()
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil {
		return
	}
	routes := m.routeURLs(st)
	if slices.Equal(urlStrings(routes), m.routes) {
		return
	}
	next := m.opts.Clone()
	next.Routes = routes
	if err := m.srv.ReloadOptions(next); err != nil {
		slog.Warn("Failed to update mesh routes", "error", err)
		return
	}
	m.opts = next
	m.routes = urlStrings(routes)
	slog.Info("Cluster mesh routes updated", "routes", len(routes))
}

// Close shuts the mesh down.
func (m *Mesh) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nc != nil {
		m.nc.Close()
	}
	if m.bc != nil {
		m.bc.Close()
	}
	if m.ec != nil {
		m.ec.Close()
	}
	if m.stop != nil {
		close(m.stop)
		m.stop = nil
	}
	if m.srv != nil {
		m.srv.Shutdown()
		m.srv.WaitForShutdown()
	}
	m.nc, m.srv = nil, nil
}

const (
	redialProbeInterval = 100 * time.Millisecond
	redialProbeTimeout  = 100 * time.Millisecond
	redialCooldown      = time.Second
)

// redialLoop shortens one-sided reconnects. nats-server redials a lost
// explicit route only once a second; when a member without a route is
// accepting connections again, re-soliciting its route URL dials it now.
func (m *Mesh) redialLoop(stop <-chan struct{}) {
	tick := time.NewTicker(redialProbeInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			m.redialMissing()
		}
	}
}

func (m *Mesh) missingRoutes() []string {
	st, err := m.node.store.Snapshot()
	if err != nil {
		return nil
	}
	m.mu.Lock()
	srv := m.srv
	routes := m.routes
	m.mu.Unlock()
	if srv == nil || len(routes) == 0 {
		return nil
	}
	rz, err := srv.Routez(nil)
	if err != nil {
		return nil
	}
	connected := make(map[string]bool, len(rz.Routes))
	for _, r := range rz.Routes {
		connected[r.RemoteName] = true
	}
	var out []string
	for _, mem := range st.Members {
		if mem.NodeID == m.node.id.ID || connected[mem.NodeID] {
			continue
		}
		if addr := MeshAddr(mem); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func (m *Mesh) redialMissing() {
	for _, addr := range m.missingRoutes() {
		m.mu.Lock()
		recent := time.Since(m.redialed[addr]) < redialCooldown
		m.mu.Unlock()
		if recent {
			continue
		}
		conn, err := (&net.Dialer{Timeout: redialProbeTimeout}).DialContext(context.Background(), "tcp", addr)
		if err != nil {
			continue
		}
		_ = conn.Close()
		m.resolicit(addr)
	}
}

// resolicit drops and re-adds the route URL for addr so the server starts
// a fresh, immediate dial instead of waiting out its reconnect delay.
func (m *Mesh) resolicit(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil {
		return
	}
	m.redialed[addr] = time.Now()
	var without []*url.URL
	found := false
	for _, u := range m.opts.Routes {
		if u.Host == addr {
			found = true
			continue
		}
		without = append(without, u)
	}
	if !found {
		return
	}
	tmp := m.opts.Clone()
	tmp.Routes = without
	if err := m.srv.ReloadOptions(tmp); err != nil {
		slog.Warn("Failed to re-solicit mesh route", "addr", addr, "error", err)
		return
	}
	next := m.opts.Clone()
	if err := m.srv.ReloadOptions(next); err != nil {
		slog.Warn("Failed to re-solicit mesh route", "addr", addr, "error", err)
		return
	}
	m.opts = next
}

// routeURLs returns one NATS route URL per other member, preferring an
// advertised nats endpoint and otherwise deriving one from the member's
// direct endpoint host and the default route port.
func (m *Mesh) routeURLs(st State) []*url.URL {
	var out []*url.URL
	for _, mem := range st.Members {
		if mem.NodeID == m.node.id.ID {
			continue
		}
		if addr := MeshAddr(mem); addr != "" {
			out = append(out, &url.URL{Scheme: "nats-route", Host: addr})
		}
	}
	slices.SortFunc(out, func(a, b *url.URL) int {
		switch {
		case a.Host < b.Host:
			return -1
		case a.Host > b.Host:
			return 1
		}
		return 0
	})
	return out
}

// MeshAddr returns the NATS route address for a member.
func MeshAddr(m Member) string {
	for _, e := range m.Endpoints {
		if e.Kind == EndpointNATS {
			return e.Addr
		}
	}
	for _, e := range m.Endpoints {
		if e.Kind == EndpointDirect {
			if host, _, err := net.SplitHostPort(e.Addr); err == nil {
				return net.JoinHostPort(host, strconv.Itoa(DefaultRoutePort))
			}
		}
	}
	return ""
}

func urlStrings(us []*url.URL) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.String()
	}
	return out
}

// meshTLSConfig authenticates NATS routes in both directions: the peer
// must present a certificate signed by the cluster key AND be a pinned
// member. Holding the join token alone cannot read mesh traffic.
func (n *Node) meshTLSConfig() *tls.Config {
	verify := func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("peer presented no certificate")
		}
		peer, err := verifyNodeCert(rawCerts[0], n.trustedKeys(), time.Now())
		if err != nil {
			return err
		}
		st, err := n.store.Snapshot()
		if err != nil {
			return err
		}
		mem, ok := st.Member(peer.NodeID)
		if !ok || !samePublicKey(mem.PublicKey, peer.PublicKey) {
			return fmt.Errorf("mesh peer %s is not a cluster member", peer.NodeID)
		}
		n.MarkSeen(peer.NodeID, time.Now().UTC())
		return nil
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAnyClientCert,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return n.currentCert()
		},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return n.currentCert()
		},
		// Verification is done by VerifyPeerCertificate against the
		// cluster CA and membership; there is no WebPKI chain.
		InsecureSkipVerify:    true, //nolint:gosec
		VerifyPeerCertificate: verify,
	}
}

// Subscribe is a convenience wrapper that logs handler panics instead of
// crashing the server.
func (m *Mesh) Subscribe(subject string, fn func(*nats.Msg)) (*nats.Subscription, error) {
	return m.nc.Subscribe(subject, func(msg *nats.Msg) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Mesh handler panicked", "subject", msg.Subject, "panic", r)
			}
		}()
		fn(msg)
	})
}

// Request sends a request and waits for one reply.
func (m *Mesh) Request(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	return m.nc.RequestWithContext(ctx, subject, data)
}

// WithMeshEndpoints adds a nats endpoint at meshPort for every direct
// endpoint host that lacks one.
func WithMeshEndpoints(eps []Endpoint, meshPort int) []Endpoint {
	out := slices.Clone(eps)
	have := map[string]bool{}
	for _, e := range eps {
		if e.Kind == EndpointNATS {
			if h, _, err := net.SplitHostPort(e.Addr); err == nil {
				have[h] = true
			}
		}
	}
	for _, e := range eps {
		if e.Kind != EndpointDirect {
			continue
		}
		h, _, err := net.SplitHostPort(e.Addr)
		if err != nil || have[h] {
			continue
		}
		have[h] = true
		out = append(out, Endpoint{Kind: EndpointNATS, Addr: net.JoinHostPort(h, strconv.Itoa(meshPort))})
	}
	return out
}

// waitMeshReady polls readiness in short steps. ReadyForConnections sleeps
// 25ms after a failed check when given a longer timeout, which would add
// that delay to every mesh start.
func waitMeshReady(srv *natsserver.Server, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if srv.ReadyForConnections(time.Millisecond) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Microsecond)
	}
}
