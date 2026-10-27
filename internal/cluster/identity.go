// Package cluster implements peer-to-peer clustering of Crush servers:
// stable node identity, key-based pairing, mutual-TLS transport between
// nodes, and membership. See docs/cluster.md for the design.
package cluster

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

const (
	nodeFile    = "node.json"
	nodeKeyFile = "node.key"
)

// Identity is this node's stable identity. The ID never changes once
// created; the name defaults to the hostname and is display-only.
type Identity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`

	key ed25519.PrivateKey
}

// PrivateKey returns the node's private key.
func (i *Identity) PrivateKey() ed25519.PrivateKey { return i.key }

// PublicKey returns the node's public key.
func (i *Identity) PublicKey() ed25519.PublicKey {
	return i.key.Public().(ed25519.PublicKey)
}

// LoadOrCreateIdentity loads the node identity from dir, creating a new
// one (with a fresh keypair) on first use.
func LoadOrCreateIdentity(dir string) (*Identity, error) {
	id, err := loadIdentity(dir)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return createIdentity(dir)
}

func loadIdentity(dir string) (*Identity, error) {
	data, err := os.ReadFile(filepath.Join(dir, nodeFile))
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("parse %s: %w", nodeFile, err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, nodeKeyFile))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("parse %s: no PEM block", nodeKeyFile)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", nodeKeyFile, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("parse %s: not an ed25519 key", nodeKeyFile)
	}
	if id.ID == "" {
		return nil, fmt.Errorf("parse %s: missing id", nodeFile)
	}
	id.key = key
	return &id, nil
}

func createIdentity(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	name, _ := os.Hostname()
	if name == "" {
		name = "node"
	}
	id := &Identity{
		ID:        uuid.NewString(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
		key:       key,
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := writeFileAtomic(filepath.Join(dir, nodeKeyFile), keyPEM); err != nil {
		return nil, err
	}
	if err := id.save(dir); err != nil {
		return nil, err
	}
	return id, nil
}

// SetName updates the node's display name and persists it.
func (i *Identity) SetName(dir, name string) error {
	i.Name = name
	return i.save(dir)
}

func (i *Identity) save(dir string) error {
	data, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, nodeFile), data)
}

// writeFileAtomic writes data to path with 0600 permissions via a
// temporary file and rename so readers never observe a partial file.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
