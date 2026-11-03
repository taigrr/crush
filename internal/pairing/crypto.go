// Package pairing implements mobile device pairing for a Crush cluster:
// rushpair1 payloads, the v1 handshake (transcript, MAC, signatures,
// SAS), single-use offers, and the replicated device registry. The
// normative description is docs/specs/MOBILE_PAIRING.md.
package pairing

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
)

// Labels from the spec. They are used verbatim as length-prefixed
// transcript components and HKDF info strings.
const (
	LabelTranscript = "crush-pair-v1 transcript"
	LabelMACKey     = "crush-pair-v1 mac key"
	LabelSAS        = "crush-pair-v1 sas"
	LabelClaim      = "crush-pair-v1 claim"
	LabelNode       = "crush-pair-v1 node"
	LabelConfirm    = "crush-pair-v1 confirm"
	LabelPoll       = "crush-pair-v1 poll"
	LabelAbort      = "crush-pair-v1 abort"
	LabelOfferID    = "crush-pair-v1 offer id"
)

// Sizes of protocol values, in bytes.
const (
	OfferIDSize      = 16
	NodeSecretSize   = 32
	DeviceSecretSize = 15
	NonceSize        = 32
	KeySize          = ed25519.PublicKeySize
	FingerprintSize  = sha256.Size
	SASSize          = 6

	deviceIDHashBytes = 16
	deviceIDPrefix    = "dev_"
	sasDigitsModulus  = 1_000_000
)

var lowerBase32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// LP length-prefixes x with its big-endian uint32 length.
func LP(x []byte) []byte {
	out := make([]byte, 4, 4+len(x))
	binary.BigEndian.PutUint32(out, uint32(len(x)))
	return append(out, x...)
}

func lpAll(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, LP(p)...)
	}
	return out
}

// DeviceID derives the stable device ID from its public key.
func DeviceID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return deviceIDPrefix + lowerBase32.EncodeToString(sum[:deviceIDHashBytes])
}

// ValidDeviceID reports whether id has the shape DeviceID produces.
func ValidDeviceID(id string) bool {
	rest, ok := strings.CutPrefix(id, deviceIDPrefix)
	if !ok {
		return false
	}
	raw, err := lowerBase32.DecodeString(rest)
	return err == nil && len(raw) == deviceIDHashBytes
}

// OfferIDFromSecret derives the offer ID of a device (kind 2) offer.
func OfferIDFromSecret(secret []byte) []byte {
	h := sha256.New()
	h.Write([]byte(LabelOfferID))
	h.Write(secret)
	return h.Sum(nil)[:OfferIDSize]
}

// Transcript holds the values bound by the handshake.
type Transcript struct {
	Kind        Kind
	OfferID     []byte
	ClusterID   string
	NodeID      string
	NodePub     ed25519.PublicKey
	CAFP        []byte
	DevicePub   ed25519.PublicKey
	NonceDevice []byte
	NonceNode   []byte
}

// T0 is the claim transcript.
func (t Transcript) T0() []byte {
	return lpAll(
		[]byte(LabelTranscript),
		[]byte{byte(t.Kind)},
		t.OfferID,
		[]byte(t.ClusterID),
		[]byte(t.NodeID),
		t.NodePub,
		t.CAFP,
		t.DevicePub,
		[]byte(DeviceID(t.DevicePub)),
		t.NonceDevice,
	)
}

// T1 is the full transcript, including the node nonce.
func (t Transcript) T1() []byte {
	return append(t.T0(), LP(t.NonceNode)...)
}

// MACKey derives K from the pairing secret.
func MACKey(secret, offerID []byte) []byte {
	key, err := hkdf.Key(sha256.New, secret, offerID, LabelMACKey, 32)
	if err != nil {
		panic(fmt.Sprintf("hkdf: %v", err)) // Only fails for oversized output.
	}
	return key
}

// Message returns M = LP(label) || transcript.
func Message(label string, transcript []byte) []byte {
	return append(LP([]byte(label)), transcript...)
}

// MAC computes HMAC-SHA256(key, msg).
func MAC(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// CheckMAC compares a MAC in constant time.
func CheckMAC(key, msg, mac []byte) bool {
	return hmac.Equal(MAC(key, msg), mac)
}

// AbortMessage is the MAC input for an abort.
func AbortMessage(offerID []byte, reason string) []byte {
	return append(LP([]byte(LabelAbort)), lpAll(offerID, []byte(reason))...)
}

// SAS is the short authentication string both screens show.
type SAS struct {
	Emoji  [4]int
	Digits int
}

// ComputeSAS derives the SAS from the secret and the full transcript.
func ComputeSAS(secret, t1 []byte) SAS {
	salt := sha256.Sum256(t1)
	raw, err := hkdf.Key(sha256.New, secret, salt[:], LabelSAS, SASSize)
	if err != nil {
		panic(fmt.Sprintf("hkdf: %v", err))
	}
	a := int(raw[0])<<16 | int(raw[1])<<8 | int(raw[2])
	b := int(raw[3])<<16 | int(raw[4])<<8 | int(raw[5])
	return SAS{
		Emoji:  [4]int{a >> 18, (a >> 12) & 63, (a >> 6) & 63, a & 63},
		Digits: b % sasDigitsModulus,
	}
}

// DigitString formats the digits as "123 456".
func (s SAS) DigitString() string {
	d := fmt.Sprintf("%06d", s.Digits)
	return d[:3] + " " + d[3:]
}

// EmojiString renders the emoji separated by spaces.
func (s SAS) EmojiString() string {
	parts := make([]string, len(s.Emoji))
	for i, idx := range s.Emoji {
		parts[i] = EmojiTable[idx].Glyph
	}
	return strings.Join(parts, " ")
}

// Names returns the emoji names, for screen readers and plain terminals.
func (s SAS) Names() []string {
	out := make([]string, len(s.Emoji))
	for i, idx := range s.Emoji {
		out[i] = EmojiTable[idx].Name
	}
	return out
}

// String renders the full SAS.
func (s SAS) String() string {
	return s.EmojiString() + "  " + s.DigitString() + "  (" + strings.Join(s.Names(), ", ") + ")"
}

// RandomBytes returns n bytes from the system CSPRNG.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never fails.
	return b
}
