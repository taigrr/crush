package pairing

import (
	"bytes"
	"crypto/ed25519"
	"errors"
)

// DeviceSession is the phone side of the handshake. rush implements the
// same steps; Crush uses it in tests and as executable documentation.
type DeviceSession struct {
	Key     ed25519.PrivateKey
	Kind    Kind
	OfferID []byte
	Secret  []byte

	info       Info
	transcript Transcript
	t1         []byte
	mac        []byte
}

// NewDeviceSessionFromPayload starts a session from a scanned kind 1
// payload.
func NewDeviceSessionFromPayload(key ed25519.PrivateKey, p Payload) (*DeviceSession, error) {
	if p.Kind != KindNode {
		return nil, errors.New("not a node offer")
	}
	return &DeviceSession{Key: key, Kind: KindNode, OfferID: p.OfferID, Secret: p.Secret, mac: MACKey(p.Secret, p.OfferID)}, nil
}

// NewDeviceSessionFromSecret starts a flow (b) session from the secret
// the phone displayed.
func NewDeviceSessionFromSecret(key ed25519.PrivateKey, secret []byte) *DeviceSession {
	id := OfferIDFromSecret(secret)
	return &DeviceSession{Key: key, Kind: KindDevice, OfferID: id, Secret: secret, mac: MACKey(secret, id)}
}

// ErrPinMismatch is returned when /client/v1/info disagrees with the QR.
var ErrPinMismatch = errors.New("node does not match the scanned code")

// CheckInfo verifies /client/v1/info against a scanned kind 1 or 3
// payload (flow a).
func CheckInfo(p Payload, info Info) error {
	if info.ClusterID != p.ClusterID || info.NodeID != p.NodeID ||
		!bytes.Equal(info.NodePub, p.NodePub) || !bytes.Equal(info.CAFP, p.CAFP) {
		return ErrPinMismatch
	}
	return nil
}

// Claim builds the claim request for the node described by info.
func (d *DeviceSession) Claim(info Info, name, platform string) ClaimRequest {
	d.info = info
	pub := d.Key.Public().(ed25519.PublicKey)
	d.transcript = Transcript{
		Kind: d.Kind, OfferID: d.OfferID, ClusterID: info.ClusterID, NodeID: info.NodeID,
		NodePub: ed25519.PublicKey(info.NodePub), CAFP: info.CAFP,
		DevicePub: pub, NonceDevice: RandomBytes(NonceSize),
	}
	msg := Message(LabelClaim, d.transcript.T0())
	return ClaimRequest{
		OfferID: d.OfferID, DevicePub: Bytes(pub), DeviceName: name, Platform: platform,
		NonceDevice: d.transcript.NonceDevice, MAC: MAC(d.mac, msg), Sig: ed25519.Sign(d.Key, msg),
	}
}

// ErrNodeAuth is returned when the claim response does not prove
// knowledge of the secret or possession of the node key.
var ErrNodeAuth = errors.New("node failed pairing authentication")

// Accept verifies the claim response and returns the SAS to display.
func (d *DeviceSession) Accept(resp ClaimResponse) (SAS, error) {
	if len(resp.NonceNode) != NonceSize {
		return SAS{}, ErrNodeAuth
	}
	d.transcript.NonceNode = resp.NonceNode
	d.t1 = d.transcript.T1()
	msg := Message(LabelNode, d.t1)
	if !CheckMAC(d.mac, msg, resp.MAC) || !ed25519.Verify(d.transcript.NodePub, msg, resp.Sig) {
		return SAS{}, ErrNodeAuth
	}
	return ComputeSAS(d.Secret, d.t1), nil
}

// Confirm builds the confirm request (the user tapped "matches").
func (d *DeviceSession) Confirm() ConfirmRequest {
	msg := Message(LabelConfirm, d.t1)
	return ConfirmRequest{OfferID: d.OfferID, MAC: MAC(d.mac, msg), Sig: ed25519.Sign(d.Key, msg)}
}

// Poll builds a poll request.
func (d *DeviceSession) Poll(waitMS int) PollRequest {
	return PollRequest{OfferID: d.OfferID, MAC: MAC(d.mac, Message(LabelPoll, d.t1)), WaitMS: waitMS}
}

// Abort builds an abort request.
func (d *DeviceSession) Abort(reason string) AbortRequest {
	return AbortRequest{OfferID: d.OfferID, Reason: reason, MAC: MAC(d.mac, AbortMessage(d.OfferID, reason))}
}
