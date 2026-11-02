package cluster

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

// ClientsAccount is the NATS account paired devices live in. Mesh
// traffic stays in the global account, so no device permission can reach
// a mesh subject.
const ClientsAccount = "CLIENTS"

// meshUser is the in-process mesh connection's user name.
const meshUser = "crush-mesh"

// CAFingerprint returns SHA-256 of the DER SubjectPublicKeyInfo of the
// CA derived from clusterKey.
func CAFingerprint(clusterKey []byte) ([]byte, error) {
	ca, err := caKey(clusterKey)
	if err != nil {
		return nil, err
	}
	spki, err := x509.MarshalPKIXPublicKey(ca.Public())
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(spki)
	return sum[:], nil
}

// CAFingerprints returns the current and (after a rotation) previous CA
// fingerprints.
func (n *Node) CAFingerprints() ([][]byte, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil, err
	}
	if st.ClusterID == "" {
		return nil, ErrNotInCluster
	}
	var out [][]byte
	for _, key := range st.trustedKeys() {
		fp, err := CAFingerprint(key)
		if err != nil {
			return nil, err
		}
		out = append(out, fp)
	}
	return out, nil
}

// ClientTLSConfig serves the node's leaf certificate, followed by the
// cluster CA certificate, to devices. Sending the CA lets a phone check
// the pin (SHA-256 of the CA's SubjectPublicKeyInfo, the QR's ca_fp) and
// verify the leaf's signature with standard tooling. Devices are not
// members, so no client certificate is requested; they authenticate
// inside the pairing protocol and with their nkey.
func (n *Node) ClientTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return n.clientCert()
		},
	}
}

func (n *Node) clientCert() (*tls.Certificate, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil, err
	}
	leaf, err := n.certFor(st.ClusterID, st.Key, st.KeyVersion)
	if err != nil {
		return nil, err
	}
	ca, err := n.caCertFor(st.ClusterID, st.Key, st.KeyVersion)
	if err != nil {
		return nil, err
	}
	chain := *leaf
	chain.Certificate = append([][]byte{leaf.Certificate[0]}, ca)
	return &chain, nil
}

// caCertFor caches the CA certificate per cluster key, so TLS handshakes
// (which unauthenticated clients can trigger) never re-sign it.
func (n *Node) caCertFor(clusterID string, key []byte, keyVer int) ([]byte, error) {
	n.certMu.Lock()
	defer n.certMu.Unlock()
	if n.caDER != nil && n.caKeyVer == keyVer && n.caCID == clusterID {
		return n.caDER, nil
	}
	der, err := CACertificate(clusterID, key)
	if err != nil {
		return nil, err
	}
	n.caDER, n.caKeyVer, n.caCID = der, keyVer, clusterID
	return der, nil
}

// caEpoch fixes the CA certificate's validity start so its DER is
// deterministic for a given cluster key.
var caEpoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// CACertificate returns the self-signed DER certificate of the CA derived
// from clusterKey. It is deterministic (ed25519 signatures are), so every
// node presents the same bytes; its SPKI hash is the device pin.
func CACertificate(clusterID string, clusterKey []byte) ([]byte, error) {
	ca, err := caKey(clusterKey)
	if err != nil {
		return nil, err
	}
	fp, err := CAFingerprint(clusterKey)
	if err != nil {
		return nil, err
	}
	name := pkix.Name{CommonName: "crush-cluster " + clusterID}
	tmpl := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(fp[:16]),
		Subject:               name,
		Issuer:                name,
		NotBefore:             caEpoch,
		NotAfter:              caEpoch.AddDate(100, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	return x509.CreateCertificate(nil, tmpl, tmpl, ca.Public(), ca)
}

const (
	wsHandshakeTimeout = 5 * time.Second
	// responseTTL bounds how long a device may answer a request it
	// received (allow_responses); it may answer each one once.
	responseTTL = time.Minute
)

// ClientUser is a device NATS user: its nkey and allowed subjects.
// Everything not listed is denied, for both publish and subscribe.
type ClientUser struct {
	Nkey      string
	Publish   []string
	Subscribe []string
	// ConnectionTypes overrides the allowed connection types (tests);
	// devices connect over the websocket only.
	ConnectionTypes []string
}

func connTypes(types ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(types))
	for _, t := range types {
		out[t] = struct{}{}
	}
	return out
}

// WebsocketAddr is the loopback address of the embedded server's device
// websocket listener (plain; front it with TLS).
func (m *Mesh) WebsocketAddr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wsAddr
}

// SetClientUsers replaces the device users in the CLIENTS account.
// Removed users are disconnected by the server on reload.
func (m *Mesh) SetClientUsers(users []ClientUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil {
		return errors.New("mesh is not running")
	}
	next := m.opts.Clone()
	acc := clientsAccount(next)
	if acc == nil {
		return errors.New("mesh has no clients account")
	}
	next.Nkeys = make([]*natsserver.NkeyUser, 0, len(users))
	for _, u := range users {
		types := u.ConnectionTypes
		if len(types) == 0 {
			types = []string{jwt.ConnectionTypeWebsocket}
		}
		next.Nkeys = append(next.Nkeys, &natsserver.NkeyUser{
			Nkey:                   u.Nkey,
			Account:                acc,
			AllowedConnectionTypes: connTypes(types...),
			Permissions: &natsserver.Permissions{
				Publish:   scoped(u.Publish),
				Subscribe: scoped(u.Subscribe),
				Response:  &natsserver.ResponsePermission{MaxMsgs: 1, Expires: responseTTL},
			},
		})
	}
	if err := m.srv.ReloadOptions(next); err != nil {
		return fmt.Errorf("reload mesh users: %w", err)
	}
	m.opts = next
	slog.Info("Mesh device users updated", "devices", len(users))
	return nil
}

// scoped is a deny-by-default subject permission: only allow is
// permitted, and an empty allow list (which NATS reads as "allow all")
// becomes an explicit deny of everything.
func scoped(allow []string) *natsserver.SubjectPermission {
	if len(allow) == 0 {
		return &natsserver.SubjectPermission{Deny: []string{">"}}
	}
	return &natsserver.SubjectPermission{Allow: allow}
}

func clientsAccount(opts *natsserver.Options) *natsserver.Account {
	for _, a := range opts.Accounts {
		if a.Name == ClientsAccount {
			return a
		}
	}
	return nil
}

// Server returns the embedded NATS server (tests and in-process device
// bridges).
func (m *Mesh) Server() *natsserver.Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srv
}
