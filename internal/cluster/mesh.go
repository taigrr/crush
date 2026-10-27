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

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// DefaultRoutePort is the default TCP port for the NATS mesh routes.
const DefaultRoutePort = DefaultPort + 1

// EndpointNATS is a host:port accepting NATS cluster routes.
const EndpointNATS EndpointKind = "nats"

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
	routes []string
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

	m := &Mesh{node: n}
	clientToken := rand.Text()
	opts := &natsserver.Options{
		ServerName: n.id.ID,
		// Routing only starts once the client listener is up, so bind it to
		// loopback on a random port and lock it with a random token. Local
		// code still connects in-process.
		Host:          "127.0.0.1",
		Port:          natsserver.RANDOM_PORT,
		Authorization: clientToken,
		NoSigs:        true,
		NoLog:         true,
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
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		return nil, errors.New("mesh server did not become ready")
	}
	nc, err := nats.Connect("", nats.InProcessServer(srv), nats.Token(clientToken), nats.Name("crush-"+n.id.Name), nats.MaxReconnects(-1))
	if err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("connect mesh: %w", err)
	}
	m.srv, m.opts, m.nc = srv, opts, nc
	m.routes = urlStrings(opts.Routes)
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
	if m.srv != nil {
		m.srv.Shutdown()
		m.srv.WaitForShutdown()
	}
	m.nc, m.srv = nil, nil
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
