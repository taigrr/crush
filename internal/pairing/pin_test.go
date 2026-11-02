package pairing

import (
	"crypto/ed25519"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testChain(t *testing.T, cn string) (leaf, ca []byte, caFP []byte) {
	t.Helper()
	caPub, caKey, _ := ed25519.GenerateKey(nil)
	name := pkix.Name{CommonName: "crush-cluster test"}
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: name, Issuer: name, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	ca, err := x509.CreateCertificate(nil, caTmpl, caTmpl, caPub, caKey)
	require.NoError(t, err)
	caCert, _ := x509.ParseCertificate(ca)
	leafPub, _, _ := ed25519.GenerateKey(nil)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: cn}, Issuer: name, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leaf, err = x509.CreateCertificate(nil, leafTmpl, caTmpl, leafPub, caKey)
	require.NoError(t, err)
	return leaf, ca, SPKIFingerprint(caCert)
}

func TestVerifyPinnedChain(t *testing.T) {
	t.Parallel()
	leaf, ca, fp := testChain(t, "node-1")
	now := time.Now()
	require.NoError(t, VerifyPinnedChain([][]byte{leaf, ca}, fp, "node-1", now))
	require.ErrorIs(t, VerifyPinnedChain([][]byte{leaf, ca}, fp, "", now), ErrPin, "the node ID is required")
	require.ErrorIs(t, VerifyPinnedChain([][]byte{leaf, ca}, fp, "node-2", now), ErrPin)
	require.ErrorIs(t, VerifyPinnedChain([][]byte{leaf}, fp, "node-1", now), ErrPin, "the CA must be presented")
	require.ErrorIs(t, VerifyPinnedChain([][]byte{leaf, ca}, RandomBytes(FingerprintSize), "node-1", now), ErrPin)
	require.ErrorIs(t, VerifyPinnedChain([][]byte{leaf, ca}, fp, "node-1", now.Add(2*time.Hour)), ErrPin, "expired")
	// The CA itself is not acceptable as the leaf.
	require.ErrorIs(t, VerifyPinnedChain([][]byte{ca, ca}, fp, "crush-cluster test", now), ErrPin)

	// A leaf from another CA, even next to the pinned CA, is refused.
	otherLeaf, _, _ := testChain(t, "node-1")
	require.ErrorIs(t, VerifyPinnedChain([][]byte{otherLeaf, ca}, fp, "node-1", now), ErrPin)
}
