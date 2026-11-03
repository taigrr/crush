package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"

	"github.com/taigrr/crush/internal/pairing"
)

// Device-facing PKI. Phones' TLS stacks do not reliably support ed25519,
// so devices see a separate ECDSA P-256 "client CA", derived from the
// cluster key like the member CA, and a per-node P-256 leaf it signs.
// The client CA's SPKI hash is the pin (ca_fp) in pairing codes.

const (
	clientCAInfo = "crush-client-ca-v1"

	clientLeafValidity = 30 * 24 * time.Hour
	clientCAValidity   = 100 * 365 * 24 * time.Hour
)

// clientCAKey derives the P-256 client CA key from the cluster key.
func clientCAKey(clusterKey []byte) (*ecdsa.PrivateKey, error) {
	n := elliptic.P256().Params().N
	// 48 bytes reduced mod n-1 gives a uniform scalar in [1, n-1].
	raw, err := hkdf.Key(sha256.New, clusterKey, nil, clientCAInfo, 48)
	if err != nil {
		return nil, err
	}
	d := new(big.Int).SetBytes(raw)
	d.Mod(d, new(big.Int).Sub(n, big.NewInt(1)))
	d.Add(d, big.NewInt(1))
	return ecdsa.ParseRawPrivateKey(elliptic.P256(), d.FillBytes(make([]byte, 32)))
}

// CAFingerprint returns the device pin for clusterKey: SHA-256 of the DER
// SubjectPublicKeyInfo of the client CA.
func CAFingerprint(clusterKey []byte) ([]byte, error) {
	ca, err := clientCAKey(clusterKey)
	if err != nil {
		return nil, err
	}
	spki, err := x509.MarshalPKIXPublicKey(&ca.PublicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(spki)
	return sum[:], nil
}

// CACertificate returns a self-signed client CA certificate for
// clusterKey. ECDSA signatures are randomized, so the bytes differ per
// call, but the public key (and therefore the pin) is stable.
func CACertificate(clusterID string, clusterKey []byte) ([]byte, error) {
	ca, err := clientCAKey(clusterKey)
	if err != nil {
		return nil, err
	}
	// Each node signs its own copy, so serials are random like any other
	// certificate's; only the public key (the pin) is shared.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	name := clientCAName(clusterID)
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               name,
		Issuer:                name,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(clientCAValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	return x509.CreateCertificate(rand.Reader, tmpl, tmpl, &ca.PublicKey, ca)
}

func clientCAName(clusterID string) pkix.Name {
	return pkix.Name{CommonName: "crush-client-ca " + clusterID}
}

// clientChain is a node's device-facing certificate chain for one
// cluster key: a fresh P-256 leaf for this node and the client CA.
type clientChain struct {
	clusterID string
	keyVer    int
	expires   time.Time
	cert      *tls.Certificate
	caDER     []byte
}

func (n *Node) issueClientChain(clusterID string, clusterKey []byte) (*clientChain, error) {
	ca, err := clientCAKey(clusterKey)
	if err != nil {
		return nil, err
	}
	caDER, err := CACertificate(clusterID, clusterKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: n.id.ID, Organization: []string{n.id.Name}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(clientLeafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{pairing.NodeServerName},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, ca)
	if err != nil {
		return nil, fmt.Errorf("issue client certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &clientChain{
		cert:  &tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: leafKey, Leaf: leaf},
		caDER: caDER, expires: leaf.NotAfter,
	}, nil
}

// currentClientChain returns the cached chain for the current cluster
// key, reissuing after a rotation or near expiry. Handshakes (which
// unauthenticated clients trigger) never sign anything.
func (n *Node) currentClientChain() (*clientChain, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil, err
	}
	if st.ClusterID == "" {
		return nil, ErrNotInCluster
	}
	n.certMu.Lock()
	defer n.certMu.Unlock()
	c := n.client
	if c != nil && c.clusterID == st.ClusterID && c.keyVer == st.KeyVersion && time.Until(c.expires) > clientLeafValidity/2 {
		return c, nil
	}
	c, err = n.issueClientChain(st.ClusterID, st.Key)
	if err != nil {
		return nil, err
	}
	c.clusterID, c.keyVer = st.ClusterID, st.KeyVersion
	n.client = c
	return c, nil
}

// ClientTLSConfig serves the node's device-facing chain [leaf, client CA]
// (TLS 1.3, P-256). Devices are not members, so no client certificate is
// requested; they authenticate inside the pairing protocol and with
// their nkey.
func (n *Node) ClientTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			c, err := n.currentClientChain()
			if err != nil {
				return nil, err
			}
			return c.cert, nil
		},
	}
}

// CAFingerprints returns the device pins devices may trust: only the
// current cluster key's. TLS serves only the current chain, so a previous
// pin would not verify; carrying devices across rotate-key is rollout
// phase 3.
func (n *Node) CAFingerprints() ([][]byte, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil, err
	}
	if st.ClusterID == "" {
		return nil, ErrNotInCluster
	}
	fp, err := CAFingerprint(st.Key)
	if err != nil {
		return nil, err
	}
	return [][]byte{fp}, nil
}

// CACertificates returns the client CA certificate devices see, in
// CAFingerprints order.
func (n *Node) CACertificates() ([][]byte, error) {
	cur, err := n.currentClientChain()
	if err != nil {
		return nil, err
	}
	return [][]byte{cur.caDER}, nil
}
