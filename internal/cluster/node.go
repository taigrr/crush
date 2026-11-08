package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultPort is the default TCP port for the cluster listener.
const DefaultPort = 7731

// Node is the cluster runtime of one Crush server: its identity, its
// membership store, and the mTLS listener peers talk to.
type Node struct {
	id      *Identity
	store   *Store
	version string

	certMu     sync.Mutex
	cert       *tls.Certificate
	certKeyVer int
	certCID    string
	client     *clientChain

	deniedMu sync.Mutex
	denied   map[string]bool

	seenMu sync.Mutex
	seen   map[string]time.Time

	// deviceSeq numbers this node's device endpoint advertisements.
	deviceSeq deviceSeq

	apiMu sync.Mutex
	api   http.Handler

	endpoints endpointCache
	proxyOnce sync.Once
	proxyRT   http.RoundTripper

	// peerClient is the shared client for cluster RPCs, rebuilt when the
	// trust generation (cluster and key version) changes.
	peerMu     sync.Mutex
	peerClient *http.Client
	peerGen    string

	srvMu    sync.Mutex
	srv      *http.Server
	listenOn string

	// OnUpgrade is invoked when a member asks this node to upgrade. It
	// should install that release (empty means latest) and restart
	// the process.
	OnUpgrade func(version string) error
	// OnJoinRequest is invoked when a new join request is recorded.
	OnJoinRequest func(JoinRequest)
}

// NewNode loads (or creates) the identity and membership in dir.
// version is this build's release version.
func NewNode(dir, version string) (*Node, error) {
	id, err := LoadOrCreateIdentity(dir)
	if err != nil {
		return nil, err
	}
	store, err := OpenStore(dir)
	if err != nil {
		return nil, err
	}
	return &Node{id: id, store: store, version: version, denied: map[string]bool{}, seen: map[string]time.Time{}}, nil
}

// Identity returns this node's identity.
func (n *Node) Identity() *Identity { return n.id }

// Store returns the membership store.
func (n *Node) Store() *Store { return n.store }

// Version returns this node's release version.
func (n *Node) Version() string { return n.version }

// currentCert returns this node's certificate for the current cluster
// key, re-minting it after a rotation.
func (n *Node) currentCert() (*tls.Certificate, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil, err
	}
	return n.certFor(st.ClusterID, st.Key, st.KeyVersion)
}

func (n *Node) certFor(clusterID string, key []byte, keyVer int) (*tls.Certificate, error) {
	n.certMu.Lock()
	defer n.certMu.Unlock()
	if n.cert != nil && n.certKeyVer == keyVer && n.certCID == clusterID {
		return n.cert, nil
	}
	c, err := IssueNodeCert(clusterID, key, n.id)
	if err != nil {
		return nil, err
	}
	n.cert, n.certKeyVer, n.certCID = &c, keyVer, clusterID
	return n.cert, nil
}

func (n *Node) trustedKeys() [][]byte {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil
	}
	return st.trustedKeys()
}

// httpClient returns an mTLS client for talking to members.
// It is shared so keep-alive connections are reused across calls (a new
// TCP and TLS handshake per call is slow over a high-latency tunnel), and
// rebuilt after a key rotation so pooled connections never outlive the
// trust they were verified under.
func (n *Node) httpClient() *http.Client {
	gen := n.trustGeneration()
	n.peerMu.Lock()
	defer n.peerMu.Unlock()
	if n.peerClient != nil && n.peerGen == gen {
		return n.peerClient
	}
	if n.peerClient != nil {
		n.peerClient.CloseIdleConnections()
	}
	_, clientTLS := tlsConfigs(n.currentCert, n.trustedKeys)
	n.peerClient, n.peerGen = newPeerHTTPClient(clientTLS), gen
	return n.peerClient
}

// trustGeneration identifies the cluster key generation peers are
// verified against.
func (n *Node) trustGeneration() string {
	st, err := n.store.Snapshot()
	if err != nil {
		return ""
	}
	return st.ClusterID + ":" + strconv.Itoa(st.KeyVersion)
}

func newPeerHTTPClient(cfg *tls.Config) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = cfg
	tr.ForceAttemptHTTP2 = true
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

// Listening returns the address the cluster listener is bound to, or ""
// when it is not running.
func (n *Node) Listening() string {
	n.srvMu.Lock()
	defer n.srvMu.Unlock()
	return n.listenOn
}

// Start begins serving cluster RPCs on addr in the background. It is a
// no-op when already listening or when the node is not in a cluster.
func (n *Node) Start(addr string) error {
	if !n.store.InCluster() {
		return nil
	}
	n.srvMu.Lock()
	defer n.srvMu.Unlock()
	if n.srv != nil {
		return nil
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}
	serverTLS, _ := tlsConfigs(n.currentCert, n.trustedKeys)
	srv := &http.Server{
		Handler:           n.Handler(),
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 10 * time.Second,
		// Reachability probes (plain TCP connect+close) surface as TLS
		// handshake EOFs; keep them out of the log.
		ErrorLog: log.New(handshakeFilter{}, "", 0),
	}
	n.srv = srv
	n.listenOn = ln.Addr().String()
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Cluster listener stopped", "error", err)
		}
	}()
	slog.Info("Cluster listener started", "addr", n.listenOn, "node", n.id.Name)
	return nil
}

// Stop shuts the cluster listener down.
func (n *Node) Stop(ctx context.Context) error {
	n.srvMu.Lock()
	srv := n.srv
	n.srv, n.listenOn = nil, ""
	n.srvMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// LocalEndpoints returns direct endpoints at port for this host's
// advertisable addresses, best first (see OrderAdvertisedAddrs): loopback,
// link-local, and down interfaces are skipped; tunnel and point-to-point
// addresses are kept last.
func LocalEndpoints(port int) []Endpoint {
	ifs, err := systemInterfaces()
	if err != nil {
		return nil
	}
	return localEndpointsFrom(ifs, port)
}

// handshakeFilter forwards http.Server errors to slog, dropping the
// handshake noise caused by endpoint reachability probes.
type handshakeFilter struct{}

func (handshakeFilter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if strings.Contains(msg, "TLS handshake error") && strings.HasSuffix(msg, "EOF") {
		return len(p), nil
	}
	slog.Debug("Cluster listener", "msg", msg)
	return len(p), nil
}
