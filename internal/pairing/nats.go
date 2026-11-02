package pairing

import (
	"crypto/ed25519"
	"errors"

	"github.com/nats-io/nkeys"
)

// NATSUser encodes a device public key as a NATS user nkey ("U...").
func NATSUser(pub ed25519.PublicKey) (string, error) {
	if len(pub) != ed25519.PublicKeySize {
		return "", errors.New("bad device public key")
	}
	user, err := nkeys.Encode(nkeys.PrefixByteUser, pub)
	if err != nil {
		return "", err
	}
	return string(user), nil
}
