package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	subjCreds = "crush.creds"
	// subjCredsRequest asks every peer to announce its credentials, so a
	// freshly installed or joined node converges without waiting for an
	// edit somewhere.
	subjCredsRequest = "crush.creds.request"

	credsPollInterval = 5 * time.Second
	maxCredsSize      = 1 << 20
)

// credsSync keeps the global data config (provider API keys and OAuth
// tokens) identical on every node, newest write wins. It travels only
// over the member-authenticated, TLS-encrypted mesh.
type credsSync struct {
	bridge *meshBridge
	path   string
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	lastSum string
	lastMod time.Time
}

type credsMsg struct {
	NodeID  string `json:"node_id"`
	ModTime int64  `json:"mod_time"` // Unix nanoseconds
	Sum     string `json:"sum"`
	Content []byte `json:"content"`
}

func newCredsSync(br *meshBridge, path string) *credsSync {
	return &credsSync{bridge: br, path: path}
}

func (c *credsSync) start() error {
	if _, err := c.bridge.mesh.Subscribe(subjCreds, c.receive); err != nil {
		return err
	}
	if _, err := c.bridge.mesh.Subscribe(subjCredsRequest, func(m *nats.Msg) {
		if string(m.Data) != c.bridge.NodeID() {
			c.announce(true)
		}
	}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.wg.Go(func() { c.loop(ctx) })
	return nil
}

func (c *credsSync) stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

func (c *credsSync) read() ([]byte, time.Time, error) {
	fi, err := os.Stat(c.path)
	if err != nil {
		return nil, time.Time{}, err
	}
	if fi.Size() > maxCredsSize {
		return nil, time.Time{}, errors.New("credentials file too large to sync")
	}
	data, err := os.ReadFile(c.path)
	return data, fi.ModTime(), err
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// announce publishes the local file when it changed (or always, when
// force is set).
func (c *credsSync) announce(force bool) {
	data, mod, err := c.read()
	if err != nil {
		return
	}
	s := sum(data)
	c.mu.Lock()
	changed := s != c.lastSum || !mod.Equal(c.lastMod)
	c.lastSum, c.lastMod = s, mod
	c.mu.Unlock()
	if !changed && !force {
		return
	}
	msg, _ := json.Marshal(credsMsg{NodeID: c.bridge.NodeID(), ModTime: mod.UnixNano(), Sum: s, Content: data})
	_ = c.bridge.mesh.Conn().Publish(subjCreds, msg)
}

// loop shares this node's state, asks peers for theirs (a few times, as
// routes take a moment to form after start or join), then announces
// local edits.
func (c *credsSync) loop(ctx context.Context) {
	for _, wait := range []time.Duration{2 * time.Second, 8 * time.Second, 20 * time.Second} {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		c.announce(true)
		_ = c.bridge.mesh.Conn().Publish(subjCredsRequest, []byte(c.bridge.NodeID()))
	}
	tick := time.NewTicker(credsPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c.announce(false)
		}
	}
}

// receive adopts a peer's file when it is newer and different.
func (c *credsSync) receive(m *nats.Msg) {
	var msg credsMsg
	if json.Unmarshal(m.Data, &msg) != nil || msg.NodeID == c.bridge.NodeID() || len(msg.Content) > maxCredsSize {
		return
	}
	if sum(msg.Content) != msg.Sum || !json.Valid(msg.Content) {
		return
	}
	incoming := time.Unix(0, msg.ModTime)
	data, mod, err := c.read()
	switch {
	case err == nil && sum(data) == msg.Sum:
		return
	case err == nil && !incoming.After(mod):
		return // ours is newer; peers will adopt it from our next announce
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return
	}
	if err := writeAtomic0600(c.path, msg.Content); err != nil {
		slog.Warn("Failed to write synced credentials", "error", err)
		return
	}
	_ = os.Chtimes(c.path, incoming, incoming)
	c.mu.Lock()
	c.lastSum, c.lastMod = msg.Sum, incoming
	c.mu.Unlock()
	slog.Info("Adopted credentials from cluster peer", "peer", c.bridge.MemberName(msg.NodeID))
	c.bridge.backend.ReloadAllConfigs()
}

func writeAtomic0600(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".creds-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
