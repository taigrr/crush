package cluster

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// KeySize is the size in bytes of a cluster key.
const KeySize = 32

// tokenPrefix versions the join token format.
const tokenPrefix = "crushc1_"

// ErrInvalidToken is returned for malformed join tokens.
var ErrInvalidToken = errors.New("invalid cluster join token")

// NewKey returns a fresh random cluster key.
func NewKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// EncodeToken encodes a cluster ID and key into a join token.
func EncodeToken(clusterID string, key []byte) string {
	payload := append([]byte(clusterID+":"), key...)
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeToken parses a join token into its cluster ID and key.
func DecodeToken(token string) (clusterID string, key []byte, err error) {
	token = strings.TrimSpace(token)
	raw, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok {
		return "", nil, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", nil, ErrInvalidToken
	}
	idx := bytes.IndexByte(payload, ':')
	if idx <= 0 || len(payload)-idx-1 != KeySize {
		return "", nil, ErrInvalidToken
	}
	return string(payload[:idx]), payload[idx+1:], nil
}

// caKey deterministically derives the cluster CA signing key from the
// cluster key, so any holder of the key can verify node certificates
// without distributing a CA certificate.
func caKey(clusterKey []byte) (ed25519.PrivateKey, error) {
	seed, err := hkdf.Key(sha256.New, clusterKey, nil, "crush-cluster-ca-v1", ed25519.SeedSize)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// certValidity bounds node certificates. They are re-minted on every
// start and on key rotation, so a long lifetime is only a safety net.
const certValidity = 365 * 24 * time.Hour

// IssueNodeCert mints a TLS certificate for id signed by the CA derived
// from clusterKey.
func IssueNodeCert(clusterID string, clusterKey []byte, id *Identity) (tls.Certificate, error) {
	ca, err := caKey(clusterKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	issuer := pkix.Name{CommonName: "crush-cluster " + clusterID}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: id.ID, Organization: []string{id.Name}},
		Issuer:       issuer,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"crush-node"},
	}
	parent := &x509.Certificate{Subject: issuer}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, id.PublicKey(), ca)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  id.PrivateKey(),
		Leaf:        leaf,
	}, nil
}

// PeerInfo is the identity proven by a peer's certificate.
type PeerInfo struct {
	NodeID    string
	Name      string
	PublicKey ed25519.PublicKey
}

// verifyNodeCert checks that raw is a currently valid node certificate
// signed by the CA derived from one of keys, and returns the peer it
// identifies.
func verifyNodeCert(raw []byte, keys [][]byte, now time.Time) (PeerInfo, error) {
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return PeerInfo{}, fmt.Errorf("parse peer certificate: %w", err)
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return PeerInfo{}, errors.New("peer certificate expired or not yet valid")
	}
	if cert.SignatureAlgorithm != x509.PureEd25519 {
		return PeerInfo{}, errors.New("peer certificate has unexpected signature algorithm")
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return PeerInfo{}, errors.New("peer certificate is not ed25519")
	}
	for _, k := range keys {
		if len(k) == 0 {
			continue
		}
		ca, err := caKey(k)
		if err != nil {
			continue
		}
		caPub := ca.Public().(ed25519.PublicKey)
		if ed25519.Verify(caPub, cert.RawTBSCertificate, cert.Signature) {
			name := ""
			if len(cert.Subject.Organization) > 0 {
				name = cert.Subject.Organization[0]
			}
			return PeerInfo{NodeID: cert.Subject.CommonName, Name: name, PublicKey: pub}, nil
		}
	}
	return PeerInfo{}, errors.New("peer certificate not signed by this cluster")
}

// tlsConfigs builds the server and client TLS configs for cluster
// traffic. certFn returns this node's current certificate and keysFn
// the cluster keys currently trusted (current and, during rotation,
// previous), so rotation takes effect without rebuilding the configs.
func tlsConfigs(certFn func() (*tls.Certificate, error), keysFn func() [][]byte) (server, client *tls.Config) {
	verify := func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("peer presented no certificate")
		}
		_, err := verifyNodeCert(rawCerts[0], keysFn(), time.Now())
		return err
	}
	server = &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAnyClientCert,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return certFn()
		},
		VerifyPeerCertificate: verify,
	}
	client = &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Chain verification is replaced by VerifyPeerCertificate, which
		// checks the cluster CA signature; there is no WebPKI chain.
		InsecureSkipVerify: true, //nolint:gosec
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return certFn()
		},
		VerifyPeerCertificate: verify,
	}
	return server, client
}

// peerFromState extracts the verified peer from a TLS connection state.
func peerFromState(cs *tls.ConnectionState, keys [][]byte) (PeerInfo, error) {
	if cs == nil || len(cs.PeerCertificates) == 0 {
		return PeerInfo{}, errors.New("no peer certificate")
	}
	return verifyNodeCert(cs.PeerCertificates[0].Raw, keys, time.Now())
}
