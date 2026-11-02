package pairing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"
)

// PayloadPrefix starts every pairing payload string.
const PayloadPrefix = "rushpair1:"

// ProtoVersion is the client protocol version carried in payloads and
// served by /client/v1/info.
const ProtoVersion = 1

const (
	maxPayloadBytes = 512
	maxTextBytes    = 64
	maxEndpoints    = 8
)

// Kind is the payload kind; it is also bound into the transcript.
type Kind uint8

// Payload kinds.
const (
	KindNode     Kind = 1 // Node shows the code (flow a).
	KindDevice   Kind = 2 // Phone shows the code (flow b).
	KindNodeInfo Kind = 3 // Secretless node locator.
)

// Payload field tags.
const (
	tagClusterID  = 1
	tagNodeID     = 2
	tagNodeName   = 3
	tagNodePub    = 4
	tagCAFP       = 5
	tagEndpoint   = 6
	tagOfferID    = 7
	tagNodeSecret = 8
	tagExpires    = 9
	tagProto      = 10

	tagDevicePub    = 1
	tagDeviceName   = 2
	tagPlatform     = 3
	tagDeviceSecret = 4
)

// EndpointKind is the typed reachability of a client endpoint.
type EndpointKind uint8

// Endpoint kinds.
const (
	EndpointLAN       EndpointKind = 1
	EndpointWireGuard EndpointKind = 2
	EndpointTailscale EndpointKind = 3
	EndpointDirect    EndpointKind = 4
)

var endpointNames = map[EndpointKind]string{
	EndpointLAN:       "lan",
	EndpointWireGuard: "wg",
	EndpointTailscale: "ts",
	EndpointDirect:    "direct",
}

// String returns the spec name (lan, wg, ts, direct).
func (k EndpointKind) String() string {
	if n, ok := endpointNames[k]; ok {
		return n
	}
	return fmt.Sprintf("kind%d", k)
}

// ParseEndpointKind maps a spec name to its kind.
func ParseEndpointKind(name string) (EndpointKind, bool) {
	for k, n := range endpointNames {
		if n == name {
			return k, true
		}
	}
	return 0, false
}

// Endpoint is a client listener address.
type Endpoint struct {
	Kind EndpointKind
	Addr string
}

// Payload is a decoded rushpair1 payload of any kind.
type Payload struct {
	Kind Kind

	// Node fields (kinds 1 and 3).
	ClusterID string
	NodeID    string
	NodeName  string
	NodePub   ed25519.PublicKey
	CAFP      []byte
	Endpoints []Endpoint
	OfferID   []byte

	// Device fields (kind 2).
	DevicePub  ed25519.PublicKey
	DeviceName string
	Platform   string

	Secret  []byte
	Expires time.Time
	Proto   uint8
}

// Payload errors.
var (
	ErrNotPayload     = errors.New("not a rushpair1 payload")
	ErrPayloadInvalid = errors.New("invalid pairing payload")
	ErrUnknownKind    = errors.New("unknown pairing payload kind (update rush / Crush)")
)

type fieldWriter []byte

func (w *fieldWriter) put(tag uint8, value []byte) {
	*w = append(*w, tag)
	*w = binary.BigEndian.AppendUint16(*w, uint16(len(value)))
	*w = append(*w, value...)
}

func (w *fieldWriter) putText(tag uint8, value string) {
	if value != "" {
		w.put(tag, []byte(value))
	}
}

func (w *fieldWriter) putBytes(tag uint8, value []byte) {
	if len(value) > 0 {
		w.put(tag, value)
	}
}

// Encode serializes the payload to its rushpair1 string.
func (p Payload) Encode() (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	w := fieldWriter{byte(p.Kind)}
	switch p.Kind {
	case KindNode, KindNodeInfo:
		w.putText(tagClusterID, p.ClusterID)
		w.putText(tagNodeID, p.NodeID)
		w.putText(tagNodeName, p.NodeName)
		w.putBytes(tagNodePub, p.NodePub)
		w.putBytes(tagCAFP, p.CAFP)
		for _, ep := range p.Endpoints {
			w.put(tagEndpoint, append([]byte{byte(ep.Kind)}, ep.Addr...))
		}
		if p.Kind == KindNode {
			w.putBytes(tagOfferID, p.OfferID)
			w.putBytes(tagNodeSecret, p.Secret)
		}
	case KindDevice:
		w.putBytes(tagDevicePub, p.DevicePub)
		w.putText(tagDeviceName, p.DeviceName)
		w.putText(tagPlatform, p.Platform)
		w.putBytes(tagDeviceSecret, p.Secret)
	}
	if !p.Expires.IsZero() {
		w.put(tagExpires, binary.BigEndian.AppendUint64(nil, uint64(p.Expires.Unix())))
	}
	w.put(tagProto, []byte{p.Proto})
	if len(w) > maxPayloadBytes {
		return "", fmt.Errorf("%w: %d bytes exceeds %d", ErrPayloadInvalid, len(w), maxPayloadBytes)
	}
	return PayloadPrefix + base64.RawURLEncoding.EncodeToString(w), nil
}

// DecodePayload parses a rushpair1 string.
func DecodePayload(s string) (Payload, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), PayloadPrefix)
	if !ok {
		return Payload{}, ErrNotPayload
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return Payload{}, fmt.Errorf("%w: %v", ErrPayloadInvalid, err)
	}
	if len(raw) == 0 || len(raw) > maxPayloadBytes {
		return Payload{}, fmt.Errorf("%w: bad length", ErrPayloadInvalid)
	}
	p := Payload{Kind: Kind(raw[0])}
	if p.Kind != KindNode && p.Kind != KindDevice && p.Kind != KindNodeInfo {
		return Payload{}, ErrUnknownKind
	}
	device := p.Kind == KindDevice
	lastTag := -1
	for buf := raw[1:]; len(buf) > 0; {
		if len(buf) < 3 {
			return Payload{}, fmt.Errorf("%w: truncated field", ErrPayloadInvalid)
		}
		tag := int(buf[0])
		n := int(binary.BigEndian.Uint16(buf[1:3]))
		if len(buf) < 3+n {
			return Payload{}, fmt.Errorf("%w: truncated field", ErrPayloadInvalid)
		}
		value := buf[3 : 3+n]
		buf = buf[3+n:]
		repeatable := !device && tag == tagEndpoint
		if tag < lastTag || (tag == lastTag && !repeatable) {
			return Payload{}, fmt.Errorf("%w: fields out of order", ErrPayloadInvalid)
		}
		lastTag = tag
		if err := p.setField(device, tag, value); err != nil {
			return Payload{}, err
		}
	}
	if err := p.validate(); err != nil {
		return Payload{}, err
	}
	return p, nil
}

func text(value []byte) (string, error) {
	if len(value) > maxTextBytes || !utf8.Valid(value) {
		return "", fmt.Errorf("%w: bad text field", ErrPayloadInvalid)
	}
	return string(value), nil
}

func (p *Payload) setField(device bool, tag int, value []byte) error {
	var err error
	switch {
	case tag == tagExpires:
		if len(value) != 8 {
			return fmt.Errorf("%w: bad expiry", ErrPayloadInvalid)
		}
		p.Expires = time.Unix(int64(binary.BigEndian.Uint64(value)), 0)
	case tag == tagProto:
		if len(value) != 1 {
			return fmt.Errorf("%w: bad proto", ErrPayloadInvalid)
		}
		p.Proto = value[0]
	case device && tag == tagDevicePub:
		p.DevicePub = clone(value)
	case device && tag == tagDeviceName:
		p.DeviceName, err = text(value)
	case device && tag == tagPlatform:
		p.Platform, err = text(value)
	case device && tag == tagDeviceSecret:
		p.Secret = clone(value)
	case !device && tag == tagClusterID:
		p.ClusterID, err = text(value)
	case !device && tag == tagNodeID:
		p.NodeID, err = text(value)
	case !device && tag == tagNodeName:
		p.NodeName, err = text(value)
	case !device && tag == tagNodePub:
		p.NodePub = clone(value)
	case !device && tag == tagCAFP:
		p.CAFP = clone(value)
	case !device && tag == tagEndpoint:
		if len(value) < 2 || len(p.Endpoints) >= maxEndpoints {
			return fmt.Errorf("%w: bad endpoint", ErrPayloadInvalid)
		}
		addr, terr := text(value[1:])
		if terr != nil {
			return terr
		}
		p.Endpoints = append(p.Endpoints, Endpoint{Kind: EndpointKind(value[0]), Addr: addr})
	case !device && tag == tagOfferID:
		p.OfferID = clone(value)
	case !device && tag == tagNodeSecret:
		p.Secret = clone(value)
	}
	// Unknown tags are skipped for forward compatibility.
	return err
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

func (p Payload) validate() error {
	bad := func(what string) error { return fmt.Errorf("%w: %s", ErrPayloadInvalid, what) }
	if p.Proto != ProtoVersion {
		return fmt.Errorf("%w: protocol %d (update rush / Crush)", ErrPayloadInvalid, p.Proto)
	}
	switch p.Kind {
	case KindNode, KindNodeInfo:
		if p.ClusterID == "" || p.NodeID == "" {
			return bad("missing cluster or node ID")
		}
		if len(p.NodePub) != KeySize || len(p.CAFP) != FingerprintSize {
			return bad("bad node key or CA fingerprint")
		}
		if len(p.Endpoints) > maxEndpoints {
			return bad("too many endpoints")
		}
		if p.Kind == KindNode && (len(p.OfferID) != OfferIDSize || len(p.Secret) != NodeSecretSize) {
			return bad("bad offer ID or secret")
		}
		if p.Kind == KindNodeInfo && (len(p.OfferID) != 0 || len(p.Secret) != 0) {
			return bad("node-info payload must not carry a secret")
		}
	case KindDevice:
		if len(p.Secret) != DeviceSecretSize {
			return bad("bad device secret")
		}
		if len(p.DevicePub) != 0 && len(p.DevicePub) != KeySize {
			return bad("bad device key")
		}
	default:
		return ErrUnknownKind
	}
	for _, s := range []string{p.ClusterID, p.NodeID, p.NodeName, p.DeviceName, p.Platform} {
		if len(s) > maxTextBytes {
			return bad("text too long")
		}
	}
	return nil
}

// Crockford base32 alphabet and its check-symbol extension.
const (
	crockford      = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	crockfordCheck = crockford + "*~$=U"

	codeSymbols   = DeviceSecretSize * 8 / 5
	codeGroupSize = 5
)

// ErrBadCode is returned for a malformed or mistyped typed code.
var ErrBadCode = errors.New("invalid pairing code")

// EncodeCode renders a 15-byte device secret as a typed code
// "XXXXX-XXXXX-XXXXX-XXXXX-XXXXX" (24 symbols plus a mod-37 check).
func EncodeCode(secret []byte) (string, error) {
	if len(secret) != DeviceSecretSize {
		return "", ErrBadCode
	}
	n := new(big.Int).SetBytes(secret)
	symbols := make([]byte, codeSymbols+1)
	for i := codeSymbols - 1; i >= 0; i-- {
		symbols[i] = crockford[new(big.Int).And(n, big.NewInt(31)).Int64()]
		n.Rsh(n, 5)
	}
	check := new(big.Int).Mod(new(big.Int).SetBytes(secret), big.NewInt(37)).Int64()
	symbols[codeSymbols] = crockfordCheck[check]
	var sb strings.Builder
	for i, c := range symbols {
		if i > 0 && i%codeGroupSize == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(c)
	}
	return sb.String(), nil
}

// DecodeCode parses a typed code, tolerating case, spaces, dashes, and
// the I/L/O look-alikes, and verifies the check symbol.
func DecodeCode(code string) ([]byte, error) {
	var clean []byte
	for _, r := range strings.ToUpper(code) {
		switch r {
		case '-', ' ':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		if r > 0x7f {
			return nil, ErrBadCode
		}
		clean = append(clean, byte(r))
	}
	if len(clean) != codeSymbols+1 {
		return nil, ErrBadCode
	}
	n := new(big.Int)
	for _, c := range clean[:codeSymbols] {
		idx := strings.IndexByte(crockford, c)
		if idx < 0 {
			return nil, ErrBadCode
		}
		n.Lsh(n, 5).Or(n, big.NewInt(int64(idx)))
	}
	secret := n.FillBytes(make([]byte, DeviceSecretSize))
	want := crockfordCheck[new(big.Int).Mod(n, big.NewInt(37)).Int64()]
	if clean[codeSymbols] != want {
		return nil, ErrBadCode
	}
	return secret, nil
}

// ParseDeviceInput accepts either a kind 2 payload string or a typed code
// and returns the device payload (DevicePub is empty for typed codes).
func ParseDeviceInput(input string) (Payload, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, PayloadPrefix) {
		p, err := DecodePayload(input)
		if err != nil {
			return Payload{}, err
		}
		if p.Kind != KindDevice {
			return Payload{}, fmt.Errorf("%w: expected a device payload", ErrPayloadInvalid)
		}
		return p, nil
	}
	secret, err := DecodeCode(input)
	if err != nil {
		return Payload{}, err
	}
	return Payload{Kind: KindDevice, Secret: secret, Proto: ProtoVersion}, nil
}
