package pairing

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type vectors struct {
	Secret         string `json:"secret"`
	OfferID        string `json:"offer_id"`
	ClusterID      string `json:"cluster_id"`
	NodeID         string `json:"node_id"`
	NodePub        string `json:"node_pub"`
	CAFP           string `json:"ca_fp"`
	DevicePub      string `json:"device_pub"`
	NonceDevice    string `json:"nonce_device"`
	NonceNode      string `json:"nonce_node"`
	Kind           int    `json:"kind"`
	DeviceID       string `json:"device_id"`
	T0SHA256       string `json:"t0_sha256"`
	T1SHA256       string `json:"t1_sha256"`
	MACKey         string `json:"mac_key"`
	MACClaim       string `json:"mac_claim"`
	MACNode        string `json:"mac_node"`
	MACConfirm     string `json:"mac_confirm"`
	MACPoll        string `json:"mac_poll"`
	MACAbort       string `json:"mac_abort"`
	SASEmoji       [4]int `json:"sas_emoji"`
	SASDigits      string `json:"sas_digits"`
	DeviceSecret   string `json:"device_secret"`
	DeviceOfferID  string `json:"device_offer_id"`
	DeviceCodeText string `json:"device_code"`
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The vectors in testdata/vectors.json were computed with an independent
// Python implementation (hashlib/hmac) of the spec, and are meant to be
// vendored by rush.
func TestKnownAnswerVectors(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	require.NoError(t, err)
	var v vectors
	require.NoError(t, json.Unmarshal(raw, &v))

	secret, offerID := unhex(t, v.Secret), unhex(t, v.OfferID)
	tr := Transcript{
		Kind: Kind(v.Kind), OfferID: offerID, ClusterID: v.ClusterID, NodeID: v.NodeID,
		NodePub: unhex(t, v.NodePub), CAFP: unhex(t, v.CAFP), DevicePub: unhex(t, v.DevicePub),
		NonceDevice: unhex(t, v.NonceDevice), NonceNode: unhex(t, v.NonceNode),
	}
	require.Equal(t, v.DeviceID, DeviceID(tr.DevicePub))
	require.True(t, ValidDeviceID(v.DeviceID))
	require.Equal(t, v.T0SHA256, hexSum(tr.T0()))
	require.Equal(t, v.T1SHA256, hexSum(tr.T1()))
	key := MACKey(secret, offerID)
	require.Equal(t, v.MACKey, hex.EncodeToString(key))
	require.Equal(t, v.MACClaim, hex.EncodeToString(MAC(key, Message(LabelClaim, tr.T0()))))
	require.Equal(t, v.MACNode, hex.EncodeToString(MAC(key, Message(LabelNode, tr.T1()))))
	require.Equal(t, v.MACConfirm, hex.EncodeToString(MAC(key, Message(LabelConfirm, tr.T1()))))
	require.Equal(t, v.MACPoll, hex.EncodeToString(MAC(key, Message(LabelPoll, tr.T1()))))
	require.Equal(t, v.MACAbort, hex.EncodeToString(MAC(key, AbortMessage(offerID, ReasonSASMismatch))))

	sas := ComputeSAS(secret, tr.T1())
	require.Equal(t, v.SASEmoji, sas.Emoji)
	require.Equal(t, v.SASDigits[:3]+" "+v.SASDigits[3:], sas.DigitString())

	devSecret := unhex(t, v.DeviceSecret)
	require.Equal(t, v.DeviceOfferID, hex.EncodeToString(OfferIDFromSecret(devSecret)))
	code, err := EncodeCode(devSecret)
	require.NoError(t, err)
	require.Equal(t, v.DeviceCodeText, code)
}

func TestTranscriptLengthPrefixing(t *testing.T) {
	t.Parallel()
	a := Transcript{ClusterID: "ab", NodeID: "c"}
	b := Transcript{ClusterID: "a", NodeID: "bc"}
	require.NotEqual(t, a.T0(), b.T0())
}

func TestTypedCode(t *testing.T) {
	t.Parallel()
	secret := RandomBytes(DeviceSecretSize)
	code, err := EncodeCode(secret)
	require.NoError(t, err)
	require.Len(t, code, 29)

	got, err := DecodeCode(code)
	require.NoError(t, err)
	require.Equal(t, secret, got)

	// Lower case, spaces, and look-alikes are tolerated.
	loose := []byte(code)
	for i, c := range loose {
		switch c {
		case '1':
			loose[i] = 'l'
		case '0':
			loose[i] = 'o'
		case '-':
			loose[i] = ' '
		}
	}
	got, err = DecodeCode(string(loose))
	require.NoError(t, err)
	require.Equal(t, secret, got)

	// A single substituted symbol fails the check.
	bad := []byte(code)
	bad[0] = map[bool]byte{true: '2', false: '3'}[bad[0] != '2']
	_, err = DecodeCode(string(bad))
	require.ErrorIs(t, err, ErrBadCode)
	_, err = DecodeCode("short")
	require.ErrorIs(t, err, ErrBadCode)
}

func testSelf(t *testing.T) Self {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return Self{
		ClusterID: "cluster-1", NodeID: "node-1", NodeName: "laptop", NodeKey: key,
		CAFP:      RandomBytes(FingerprintSize),
		Endpoints: []Endpoint{{Kind: EndpointTailscale, Addr: "laptop.tailnet.ts.net:7733"}, {Kind: EndpointLAN, Addr: "192.168.1.20:7733"}},
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	self := testSelf(t)
	node := Payload{
		Kind: KindNode, ClusterID: self.ClusterID, NodeID: self.NodeID, NodeName: self.NodeName,
		NodePub: self.NodePub(), CAFP: self.CAFP, Endpoints: self.Endpoints,
		OfferID: RandomBytes(OfferIDSize), Secret: RandomBytes(NodeSecretSize),
		Expires: time.Unix(1_800_000_000, 0), Proto: ProtoVersion,
	}
	s, err := node.Encode()
	require.NoError(t, err)
	require.LessOrEqual(t, len(s), 400, "fits QR version 15-M")
	got, err := DecodePayload(s)
	require.NoError(t, err)
	require.Equal(t, node, got)

	info, err := self.InfoPayload()
	require.NoError(t, err)
	got, err = DecodePayload(info)
	require.NoError(t, err)
	require.Equal(t, KindNodeInfo, got.Kind)
	require.Empty(t, got.Secret)

	pub, _, _ := ed25519.GenerateKey(nil)
	dev := Payload{Kind: KindDevice, DevicePub: pub, DeviceName: "Tai's iPhone", Platform: "ios", Secret: RandomBytes(DeviceSecretSize), Expires: time.Unix(1_800_000_000, 0), Proto: ProtoVersion}
	s, err = dev.Encode()
	require.NoError(t, err)
	got, err = ParseDeviceInput(s)
	require.NoError(t, err)
	require.Equal(t, dev, got)

	_, err = DecodePayload("rushpair2:AAAA")
	require.ErrorIs(t, err, ErrNotPayload)
	_, err = DecodePayload(PayloadPrefix + "CQ")
	require.ErrorIs(t, err, ErrUnknownKind)

	// Out-of-order tags are rejected.
	bad := fieldWriter{byte(KindDevice)}
	bad.put(tagDeviceSecret, RandomBytes(DeviceSecretSize))
	bad.put(tagDevicePub, pub)
	bad.put(tagProto, []byte{ProtoVersion})
	_, err = DecodePayload(PayloadPrefix + b64(bad))
	require.ErrorIs(t, err, ErrPayloadInvalid)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

type harness struct {
	mgr      *Manager
	self     Self
	now      time.Time
	mu       sync.Mutex
	approved []Device
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{self: testSelf(t), now: time.Unix(1_800_000_000, 0)}
	h.mgr = NewManager(Config{
		Self: func() (Self, error) { return h.self, nil },
		Now: func() time.Time {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.now
		},
		OnApprove: func(d Device) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.approved = append(h.approved, d)
			return nil
		},
		Extras: func() Extras { return Extras{NATSURLs: []string{"wss://x/nats"}} },
	})
	return h
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

func newDevice(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return key
}

func TestFlowANodeOffer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, err := h.mgr.CreateNodeOffer(0, RoleOperator)
	require.NoError(t, err)
	p, err := DecodePayload(view.Payload)
	require.NoError(t, err)
	require.NoError(t, CheckInfo(p, h.self.Info()))

	dev, err := NewDeviceSessionFromPayload(newDevice(t), p)
	require.NoError(t, err)
	resp, err := h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "phone", "ios"))
	require.NoError(t, err)
	sas, err := dev.Accept(resp)
	require.NoError(t, err)

	got, err := h.mgr.Get(p.OfferID)
	require.NoError(t, err)
	require.Equal(t, StateClaimed, got.State)
	require.Equal(t, sas.DigitString(), got.SAS.Digits)
	require.Empty(t, got.Payload, "secret is not shown after the claim")

	st, err := h.mgr.Confirm(dev.Confirm())
	require.NoError(t, err)
	require.Equal(t, StateClaimed, st.State)

	pollDone := make(chan PollResponse, 1)
	go func() {
		r, err := h.mgr.Poll(context.Background(), dev.Poll(5000))
		require.NoError(t, err)
		pollDone <- r
	}()
	_, err = h.mgr.Approve(p.OfferID, RoleViewer)
	require.NoError(t, err)
	r := <-pollDone
	require.Equal(t, StateApproved, r.State)
	require.Equal(t, RoleViewer, r.Device.Role)
	require.Equal(t, DevicePermissions(r.Device.DeviceID), r.NATS.Permissions)
	require.Len(t, h.approved, 1)

	// A second claim of a used offer fails.
	_, err = h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "phone", "ios"))
	requireCode(t, err, ErrCodeOfferUsed)
}

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var pe *Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, code, pe.Code)
}

func TestOfferExpires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, err := h.mgr.CreateNodeOffer(time.Minute, RoleOperator)
	require.NoError(t, err)
	p, _ := DecodePayload(view.Payload)
	h.advance(time.Minute + time.Second)
	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	_, err = h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "phone", "ios"))
	requireCode(t, err, ErrCodeOfferExpired)

	_, err = h.mgr.CreateNodeOffer(MaxOfferTTL+time.Second, RoleOperator)
	requireCode(t, err, ErrCodeBadRequest)
}

func TestDoubleClaimAbortsOffer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	first, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	_, err := h.mgr.Claim("192.0.2.1", first.Claim(h.self.Info(), "a", "ios"))
	require.NoError(t, err)
	attacker, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	_, err = h.mgr.Claim("192.0.2.1", attacker.Claim(h.self.Info(), "b", "ios"))
	requireCode(t, err, ErrCodeOfferUsed)
	got, _ := h.mgr.Get(p.OfferID)
	require.Equal(t, StateAborted, got.State)
	require.Equal(t, ReasonDoubleClaim, got.Reason)
}

func TestBadMACThreeStrikes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	wrong := p
	wrong.Secret = RandomBytes(NodeSecretSize)
	for range 3 {
		dev, _ := NewDeviceSessionFromPayload(newDevice(t), wrong)
		_, err := h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "x", "ios"))
		requireCode(t, err, ErrCodeMACInvalid)
	}
	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	_, err := h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "x", "ios"))
	requireCode(t, err, ErrCodeOfferAborted)
}

func TestTamperedTranscriptRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	// A MITM node: the device saw a different CA fingerprint.
	info := h.self.Info()
	info.CAFP = RandomBytes(FingerprintSize)
	_, err := h.mgr.Claim("192.0.2.1", dev.Claim(info, "x", "ios"))
	requireCode(t, err, ErrCodeMACInvalid)

	// A response signed by another key fails on the device.
	dev2, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	resp, err := h.mgr.Claim("192.0.2.1", dev2.Claim(h.self.Info(), "x", "ios"))
	require.NoError(t, err)
	resp.Sig[0] ^= 1
	_, err = dev2.Accept(resp)
	require.ErrorIs(t, err, ErrNodeAuth)
}

func TestMismatchAbort(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	resp, err := h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "x", "ios"))
	require.NoError(t, err)
	_, err = dev.Accept(resp)
	require.NoError(t, err)

	bad := dev.Abort(ReasonSASMismatch)
	bad.MAC[0] ^= 1
	_, err = h.mgr.Abort(bad)
	requireCode(t, err, ErrCodeMACInvalid)

	st, err := h.mgr.Abort(dev.Abort(ReasonSASMismatch))
	require.NoError(t, err)
	require.Equal(t, StateAborted, st.State)
	_, err = h.mgr.Approve(p.OfferID, "")
	requireCode(t, err, ErrCodeOfferAborted)
	require.Empty(t, h.approved)
}

func TestFlowBDeviceCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := newDevice(t)
	secret := RandomBytes(DeviceSecretSize)
	code, _ := EncodeCode(secret)
	view, err := h.mgr.AcceptDevice(code, RoleOperator)
	require.NoError(t, err)
	require.NotEmpty(t, view.InfoPayload)
	_, err = h.mgr.AcceptDevice(code, RoleOperator)
	requireCode(t, err, ErrCodeOfferUsed)

	dev := NewDeviceSessionFromSecret(key, secret)
	resp, err := h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "phone", "android"))
	require.NoError(t, err)
	_, err = dev.Accept(resp)
	require.NoError(t, err)
	_, err = h.mgr.Approve(dev.OfferID, "")
	require.NoError(t, err)
	_, err = h.mgr.Confirm(dev.Confirm())
	require.NoError(t, err)
	r, err := h.mgr.Poll(context.Background(), dev.Poll(0))
	require.NoError(t, err)
	require.Equal(t, StateApproved, r.State)
	require.Equal(t, DeviceID(key.Public().(ed25519.PublicKey)), r.Device.DeviceID)
}

func TestFlowBPinnedKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := newDevice(t)
	p := Payload{Kind: KindDevice, DevicePub: key.Public().(ed25519.PublicKey), Secret: RandomBytes(DeviceSecretSize), Proto: ProtoVersion}
	s, _ := p.Encode()
	_, err := h.mgr.AcceptDevice(s, RoleOperator)
	require.NoError(t, err)
	// Someone who read the secret but holds another key cannot claim.
	other := NewDeviceSessionFromSecret(newDevice(t), p.Secret)
	_, err = h.mgr.Claim("192.0.2.1", other.Claim(h.self.Info(), "x", "ios"))
	requireCode(t, err, ErrCodeMACInvalid)
	dev := NewDeviceSessionFromSecret(key, p.Secret)
	_, err = h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "x", "ios"))
	require.NoError(t, err)
}

func TestClaimRateLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for i := range claimsPerWindow + 1 {
		_, err := h.mgr.Claim("192.0.2.1", ClaimRequest{
			OfferID: RandomBytes(OfferIDSize), DevicePub: RandomBytes(KeySize), NonceDevice: RandomBytes(NonceSize),
			MAC: RandomBytes(macSize), Sig: RandomBytes(ed25519.SignatureSize),
		})
		if i < claimsPerWindow {
			requireCode(t, err, ErrCodeOfferUnknown)
		} else {
			requireCode(t, err, ErrCodeRateLimited)
		}
	}
	h.advance(claimWindow)
	_, err := h.mgr.Claim("192.0.2.1", ClaimRequest{
		OfferID: RandomBytes(OfferIDSize), DevicePub: RandomBytes(KeySize), NonceDevice: RandomBytes(NonceSize),
		MAC: RandomBytes(macSize), Sig: RandomBytes(ed25519.SignatureSize),
	})
	requireCode(t, err, ErrCodeOfferUnknown)
}

func testDevice(t *testing.T, rev int) Device {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_800_000_000, 0).UTC()
	return Device{
		DeviceID: DeviceID(pub), Name: "phone", PublicKey: pub, Role: RoleOperator,
		Scopes: RoleOperator.Scopes(), PairedAt: now, Rev: rev, UpdatedAt: now, UpdatedBy: "n1",
	}
}

func TestRegistryMergeAndPersist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg, err := OpenRegistry(dir)
	require.NoError(t, err)
	var seen []Device
	reg.Watch(func(d Device) { seen = append(seen, d) })

	d := testDevice(t, 1)
	changed, err := reg.Merge(d)
	require.NoError(t, err)
	require.True(t, changed)
	changed, _ = reg.Merge(d)
	require.False(t, changed, "same record is a no-op")

	newerRec := d
	newerRec.Rev, newerRec.Name = 2, "renamed"
	changed, _ = reg.Merge(newerRec)
	require.True(t, changed)
	older := d
	older.Name = "stale"
	changed, _ = reg.Merge(older)
	require.False(t, changed)

	revoked, err := reg.Revoke(d.DeviceID, "n2", time.Now())
	require.NoError(t, err)
	require.True(t, revoked.Revoked())
	// Revocation wins even against a higher rev.
	resurrect := newerRec
	resurrect.Rev = 99
	changed, _ = reg.Merge(resurrect)
	require.False(t, changed)
	require.Len(t, seen, 3)

	// Inconsistent records are refused.
	forged := testDevice(t, 1)
	forged.DeviceID = d.DeviceID
	_, err = reg.Merge(forged)
	require.Error(t, err)

	reopened, err := OpenRegistry(dir)
	require.NoError(t, err)
	got, ok := reopened.Get(d.DeviceID)
	require.True(t, ok)
	require.True(t, got.Revoked())
	require.Equal(t, reg.Digest(), reopened.Digest())
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, devicesFile))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}

	_, err = reg.Revoke("dev_missing", "n1", time.Now())
	require.ErrorIs(t, err, ErrDeviceNotFound)
}

func TestNATSUser(t *testing.T) {
	t.Parallel()
	pub, _, _ := ed25519.GenerateKey(nil)
	user, err := NATSUser(pub)
	require.NoError(t, err)
	require.Equal(t, byte('U'), user[0])
	perms := DevicePermissions("dev_abc")
	require.Equal(t, []string{"crush.client.v1.dev_abc.>"}, perms.Publish)
	require.NotContains(t, perms.Subscribe, ">")
}

func TestAttackerCannotLockOutPhone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, err := h.mgr.CreateNodeOffer(0, RoleOperator)
	require.NoError(t, err)
	p, _ := DecodePayload(view.Payload)

	// A LAN attacker sprays random offer IDs from many addresses, enough
	// to overflow the source table.
	probe := ClaimRequest{
		DevicePub: RandomBytes(KeySize), NonceDevice: RandomBytes(NonceSize),
		MAC: RandomBytes(macSize), Sig: RandomBytes(ed25519.SignatureSize),
	}
	for i := range maxSources + 10 {
		for range claimsPerWindow + 1 {
			probe.OfferID = RandomBytes(OfferIDSize)
			_, _ = h.mgr.Claim(fmt.Sprintf("198.51.%d.%d", i/256, i%256), probe)
		}
	}
	require.LessOrEqual(t, len(h.mgr.sources), maxSources)

	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	_, err = h.mgr.Claim("192.0.2.77", dev.Claim(h.self.Info(), "phone", "ios"))
	require.NoError(t, err, "the real phone still claims")
}

func TestRegistryRejectsWidenedScopes(t *testing.T) {
	t.Parallel()
	reg, err := OpenRegistry(t.TempDir())
	require.NoError(t, err)
	d := testDevice(t, 1)
	d.Role, d.Scopes = RoleViewer, []string{"sessions:read", "sessions:move"}
	_, err = reg.Merge(d)
	require.ErrorContains(t, err, "not allowed")
	d.Scopes = []string{"sessions:read", "shell"}
	_, err = reg.Merge(d)
	require.NoError(t, err, "opt-in scopes are allowed")
	long := testDevice(t, 1)
	long.Model = strings.Repeat("x", maxTextBytes+1)
	_, err = reg.Merge(long)
	require.Error(t, err)
}

func TestClaimWithoutSecretCannotAbortClaimedOffer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	_, err := h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "a", "ios"))
	require.NoError(t, err)

	// Someone who only knows the offer ID gets a MAC error, not an abort.
	wrong := p
	wrong.Secret = RandomBytes(NodeSecretSize)
	attacker, _ := NewDeviceSessionFromPayload(newDevice(t), wrong)
	_, err = h.mgr.Claim("192.0.2.2", attacker.Claim(h.self.Info(), "b", "ios"))
	requireCode(t, err, ErrCodeMACInvalid)
	got, _ := h.mgr.Get(p.OfferID)
	require.Equal(t, StateClaimed, got.State)
}

func TestWaitReturnsForTerminalState(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(time.Second, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	h.advance(2 * time.Second)
	start := time.Now()
	got, err := h.mgr.Wait(context.Background(), p.OfferID, StateExpired, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, StateExpired, got.State)
	require.Less(t, time.Since(start), time.Second, "no busy wait on terminal offers")
}

func TestSourceKey(t *testing.T) {
	t.Parallel()
	require.Equal(t, "192.0.2.1", SourceKey("192.0.2.1"))
	require.Equal(t, "192.0.2.1", SourceKey("::ffff:192.0.2.1"))
	require.Equal(t, SourceKey("2001:db8::1"), SourceKey("2001:db8::ffff:2"))
	require.NotEqual(t, SourceKey("2001:db8::1"), SourceKey("2001:db8:0:1::1"))
}

func TestSharedSourceCannotBlockValidClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	// The attacker shares the phone's IPv6 /64 and exhausts its budget.
	src := SourceKey("2001:db8::bad")
	for range claimsPerWindow + 2 {
		_, _ = h.mgr.Claim(src, ClaimRequest{
			OfferID: RandomBytes(OfferIDSize), DevicePub: RandomBytes(KeySize), NonceDevice: RandomBytes(NonceSize),
			MAC: RandomBytes(macSize), Sig: RandomBytes(ed25519.SignatureSize),
		})
	}
	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	_, err := h.mgr.Claim(SourceKey("2001:db8::1"), dev.Claim(h.self.Info(), "phone", "ios"))
	require.NoError(t, err)
}

func TestClaimRetryIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	dev, _ := NewDeviceSessionFromPayload(newDevice(t), p)
	req := dev.Claim(h.self.Info(), "phone", "ios")
	first, err := h.mgr.Claim("192.0.2.1", req)
	require.NoError(t, err)
	again, err := h.mgr.Claim("192.0.2.1", req)
	require.NoError(t, err, "a lost response can be retried")
	require.Equal(t, first, again)
	_, err = dev.Accept(again)
	require.NoError(t, err)
	got, _ := h.mgr.Get(p.OfferID)
	require.Equal(t, StateClaimed, got.State)
}

func TestRevokedKeyCannotClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := newDevice(t)
	revoked := DeviceID(key.Public().(ed25519.PublicKey))
	h.mgr.cfg.Revoked = func(id string) bool { return id == revoked }
	view, _ := h.mgr.CreateNodeOffer(0, RoleOperator)
	p, _ := DecodePayload(view.Payload)
	dev, _ := NewDeviceSessionFromPayload(key, p)
	_, err := h.mgr.Claim("192.0.2.1", dev.Claim(h.self.Info(), "phone", "ios"))
	requireCode(t, err, ErrCodeBadRequest)
}

func TestRegistryBindCluster(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg, err := OpenRegistry(dir)
	require.NoError(t, err)
	require.NoError(t, reg.BindCluster("c1"))
	_, err = reg.Merge(testDevice(t, 1))
	require.NoError(t, err)
	require.NoError(t, reg.BindCluster("c1"))
	require.Len(t, reg.List(), 1)

	reopened, err := OpenRegistry(dir)
	require.NoError(t, err)
	require.NoError(t, reopened.BindCluster("c2"))
	require.Empty(t, reopened.List(), "trust does not carry across clusters")
}
