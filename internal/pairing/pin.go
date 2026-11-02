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
// currently valid, allows server authentication, and its common name is
// nodeID. Host names are not checked; the pin replaces them.
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
	if leaf.Subject.CommonName != nodeID {
		return fmt.Errorf("%w: leaf is for another node", ErrPin)
	}
	return nil
}

// PinnedTLSConfig is a client TLS config that trusts exactly one node
// certified by the pinned cluster CA. It is the Go reference for rush.
func PinnedTLSConfig(caFP []byte, nodeID string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Standard verification needs a trusted root and a host name;
		// the pin replaces both, checked in VerifyConnection.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			raw := make([][]byte, 0, len(cs.PeerCertificates))
			for _, c := range cs.PeerCertificates {
				raw = append(raw, c.Raw)
			}
			return VerifyPinnedChain(raw, caFP, nodeID, time.Now())
		},
	}
}
