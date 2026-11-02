package cluster

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"

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

// ClientTLSConfig serves the node's leaf certificate to devices. Devices
// are not members, so no client certificate is requested; they
// authenticate inside the pairing protocol and with their nkey.
func (n *Node) ClientTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return n.currentCert()
		},
	}
}

// ClientUser is a device NATS user: its nkey and allowed subjects.
type ClientUser struct {
	Nkey      string
	Publish   []string
	Subscribe []string
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
		next.Nkeys = append(next.Nkeys, &natsserver.NkeyUser{
			Nkey:    u.Nkey,
			Account: acc,
			Permissions: &natsserver.Permissions{
				Publish:   &natsserver.SubjectPermission{Allow: u.Publish},
				Subscribe: &natsserver.SubjectPermission{Allow: u.Subscribe},
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
