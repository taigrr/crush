package cluster

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/pairing"
)

func TestClientUsersAreScopedAndRevocable(t *testing.T) {
	t.Parallel()
	nodes, addrs := pairedTrio(t)
	mesh, err := nodes[0].StartMesh(addrs[0])
	require.NoError(t, err)
	t.Cleanup(mesh.Close)

	fps, err := nodes[0].CAFingerprints()
	require.NoError(t, err)
	require.Len(t, fps, 1)
	require.Len(t, fps[0], 32)

	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	kp, err := nkeys.FromRawSeed(nkeys.PrefixByteUser, priv.Seed())
	require.NoError(t, err)
	user, err := kp.PublicKey()
	require.NoError(t, err)
	encoded, err := nkeys.Encode(nkeys.PrefixByteUser, pub)
	require.NoError(t, err)
	require.Equal(t, string(encoded), user)

	own := "crush.client.v1.dev_x.>"
	require.NoError(t, mesh.SetClientUsers([]ClientUser{{Nkey: user, Publish: []string{own}, Subscribe: []string{own}, ConnectionTypes: []string{jwt.ConnectionTypeInProcess}}}))

	// The mesh connection still works after the auth reload.
	got := make(chan string, 4)
	_, err = mesh.Subscribe("crush.creds", func(m *nats.Msg) { got <- string(m.Data) })
	require.NoError(t, err)
	require.NoError(t, mesh.Conn().Flush())

	errs := make(chan error, 4)
	closed := make(chan struct{})
	dev, err := nats.Connect("", nats.InProcessServer(mesh.Server()),
		nats.Nkey(user, func(nonce []byte) ([]byte, error) { return kp.Sign(nonce) }),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errs <- err }),
		nats.ClosedHandler(func(*nats.Conn) { close(closed) }),
		nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(dev.Close)

	// Own subjects work.
	echo := make(chan struct{}, 1)
	_, err = dev.Subscribe("crush.client.v1.dev_x.ping", func(*nats.Msg) { echo <- struct{}{} })
	require.NoError(t, err)
	require.NoError(t, dev.Publish("crush.client.v1.dev_x.ping", nil))
	select {
	case <-echo:
	case <-time.After(5 * time.Second):
		t.Fatal("device did not receive its own message")
	}

	// Mesh subjects are out of reach: denied and, being another account,
	// never delivered.
	require.NoError(t, dev.Publish("crush.creds", []byte("forged")))
	_ = dev.Flush()
	select {
	case err := <-errs:
		require.ErrorContains(t, err, "Permissions Violation")
	case <-time.After(5 * time.Second):
		t.Fatal("expected a permissions violation")
	}
	select {
	case msg := <-got:
		t.Fatalf("mesh received device message %q", msg)
	case <-time.After(200 * time.Millisecond):
	}

	// Removing the user disconnects it.
	require.NoError(t, mesh.SetClientUsers(nil))
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked device stayed connected")
	}
	require.NoError(t, mesh.Conn().Publish("crush.creds", []byte("ok")))
	select {
	case msg := <-got:
		require.Equal(t, "ok", msg)
	case <-time.After(5 * time.Second):
		t.Fatal("mesh connection broken after reload")
	}
}

func TestCAFingerprintOrder(t *testing.T) {
	t.Parallel()
	cur, prev := []byte("current-key-current-key-current!"), []byte("previous-key-previous-key-previo")
	st := State{Key: cur, PrevKey: prev}
	keys := st.trustedKeys()
	require.Equal(t, cur, keys[0], "the current key must come first: pairing pins fps[0]")
	fpCur, err := CAFingerprint(cur)
	require.NoError(t, err)
	fpPrev, err := CAFingerprint(prev)
	require.NoError(t, err)
	require.NotEqual(t, fpCur, fpPrev)
}

func TestClientChainIsP256AndPinned(t *testing.T) {
	t.Parallel()
	n := newTestNode(t, "v1")
	_, err := n.Init(nil)
	require.NoError(t, err)
	st, err := n.store.Snapshot()
	require.NoError(t, err)

	c, err := n.currentClientChain()
	require.NoError(t, err)
	again, err := n.currentClientChain()
	require.NoError(t, err)
	require.Same(t, c, again, "handshakes reuse the cached chain")

	require.Len(t, c.cert.Certificate, 2)
	leaf, err := x509.ParseCertificate(c.cert.Certificate[0])
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(c.cert.Certificate[1])
	require.NoError(t, err)
	require.Equal(t, x509.ECDSA, leaf.PublicKeyAlgorithm, "phones need P-256, not ed25519")
	require.Equal(t, x509.ECDSA, ca.PublicKeyAlgorithm)
	require.True(t, ca.IsCA)
	require.NoError(t, leaf.CheckSignatureFrom(ca))
	require.Equal(t, n.Identity().ID, leaf.Subject.CommonName)
	require.Contains(t, leaf.DNSNames, pairing.NodeServerName)

	fp, err := CAFingerprint(st.Key)
	require.NoError(t, err)
	sum := sha256.Sum256(ca.RawSubjectPublicKeyInfo)
	require.Equal(t, fp, sum[:], "ca_fp is the SPKI hash of the presented CA")
	other, err := CACertificate(st.ClusterID, st.Key)
	require.NoError(t, err)
	otherCA, err := x509.ParseCertificate(other)
	require.NoError(t, err)
	require.Equal(t, ca.RawSubjectPublicKeyInfo, otherCA.RawSubjectPublicKeyInfo, "every node derives the same CA key")
}
