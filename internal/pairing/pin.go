package pairing

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrPin is returned when a node's TLS certificates do not match the pin.
var ErrPin = errors.New("node certificate does not match the pairing pin")

// SPKIFingerprint is SHA-256 of a certificate's DER SubjectPublicKeyInfo:
// the format of ca_fp.
func SPKIFingerprint(cert *x509.Certificate) []byte {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return sum[:]
}

// VerifyPinnedChain checks a node's presented chain the way rush must:
// some presented certificate is a currently valid CA whose SPKI hashes to
// caFP; the leaf (first certificate) is not a CA, is signed by it, is
// currently valid, allows server authentication, carries the DNS name
// NodeServerName, and its common name is nodeID. The address the device
// dialed is not checked; the pin replaces it.
func VerifyPinnedChain(rawCerts [][]byte, caFP []byte, nodeID string, now time.Time) error {
	if len(rawCerts) < 2 || len(caFP) != FingerprintSize || nodeID == "" {
		return ErrPin
	}
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for _, raw := range rawCerts {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPin, err)
		}
		certs = append(certs, c)
	}
	leaf := certs[0]
	var ca *x509.Certificate
	for _, c := range certs[1:] {
		if c.IsCA && subtle.ConstantTimeCompare(SPKIFingerprint(c), caFP) == 1 {
			ca = c
			break
		}
	}
	if ca == nil {
		return fmt.Errorf("%w: no CA with the pinned fingerprint", ErrPin)
	}
	if now.Before(ca.NotBefore) || now.After(ca.NotAfter) {
		return fmt.Errorf("%w: CA certificate expired or not yet valid", ErrPin)
	}
	if leaf.IsCA || !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		return fmt.Errorf("%w: leaf is not a server certificate", ErrPin)
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		return fmt.Errorf("%w: leaf not signed by the pinned CA", ErrPin)
	}
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("%w: leaf certificate expired or not yet valid", ErrPin)
	}
	if !slices.Contains(leaf.DNSNames, NodeServerName) {
		return fmt.Errorf("%w: leaf does not carry the %s name", ErrPin, NodeServerName)
	}
	if leaf.Subject.CommonName != nodeID {
		return fmt.Errorf("%w: leaf is for another node", ErrPin)
	}
	return nil
}

// NodeServerName is the DNS name every node certificate carries; clients
// verifying against the cluster CA use it as the TLS server name, since
// the pin, not the host name, identifies the node.
const NodeServerName = "crush-node"

// TrustedTLSConfig builds a standard, fully verifying client TLS config
// for one node after pairing: the trust roots are the cluster CA
// certificates from the poll response, each accepted only if its SPKI
// hash is a pinned fingerprint, and the leaf's common name must be
// nodeID. It is the Go reference for rush.
func TrustedTLSConfig(caCerts [][]byte, caFPs [][]byte, nodeID string) (*tls.Config, error) {
	if nodeID == "" {
		return nil, ErrPin
	}
	pool := x509.NewCertPool()
	added := 0
	for _, der := range caCerts {
		ca, err := x509.ParseCertificate(der)
		if err != nil || !ca.IsCA {
			continue
		}
		fp := SPKIFingerprint(ca)
		if !slices.ContainsFunc(caFPs, func(pin []byte) bool { return subtle.ConstantTimeCompare(pin, fp) == 1 }) {
			continue
		}
		pool.AddCert(ca)
		added++
	}
	if added == 0 {
		return nil, fmt.Errorf("%w: no CA certificate matches a pin", ErrPin)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: NodeServerName,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || cs.PeerCertificates[0].Subject.CommonName != nodeID {
				return fmt.Errorf("%w: leaf is for another node", ErrPin)
			}
			return nil
		},
	}, nil
}
