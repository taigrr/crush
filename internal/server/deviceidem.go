package server

import (
	"crypto/sha256"
	"sync"
	"time"
)

const (
	idempotencyTTL = 10 * time.Minute
	// maxIdempotencyPerDevice bounds the cache per device; the oldest
	// completed entries go first.
	maxIdempotencyPerDevice = 256
	maxIdempotencyKey       = 64
)

// idemEntry is one remembered POST.
type idemEntry struct {
	fp      [sha256.Size]byte
	created time.Time
	done    chan struct{}
	resp    bridgeResponse
	ok      bool
}

// idemCache remembers device POST responses by (device, key) so a retry
// after a timeout or a lost reply does not repeat the action. Keys are
// scoped by the authenticated device ID, never by anything the device
// sends, so devices cannot collide with or read each other's entries.
type idemCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]map[string]*idemEntry
}

func newIdemCache() *idemCache {
	return &idemCache{now: time.Now, entries: map[string]map[string]*idemEntry{}}
}

func idemFingerprint(method, path string, body []byte) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(body)
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// begin looks key up for deviceID. It returns the entry and whether the
// caller is the first (and must run the request and call finish), or
// conflict when the key was used for a different request.
func (c *idemCache) begin(deviceID, key string, fp [sha256.Size]byte) (e *idemEntry, leader, conflict bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	dev := c.entries[deviceID]
	if dev == nil {
		dev = map[string]*idemEntry{}
		c.entries[deviceID] = dev
	}
	for k, old := range dev {
		if now.Sub(old.created) > idempotencyTTL && isDone(old) {
			delete(dev, k)
		}
	}
	if old, ok := dev[key]; ok {
		if old.fp != fp {
			return nil, false, true
		}
		return old, false, false
	}
	if len(dev) >= maxIdempotencyPerDevice {
		c.evictOldest(dev)
	}
	e = &idemEntry{fp: fp, created: now, done: make(chan struct{})}
	dev[key] = e
	return e, true, false
}

// evictOldest drops the oldest completed entry (in-flight ones stay, and
// the device's in-flight requests are bounded anyway).
func (c *idemCache) evictOldest(dev map[string]*idemEntry) {
	var oldestKey string
	var oldest time.Time
	for k, e := range dev {
		if !isDone(e) {
			continue
		}
		if oldestKey == "" || e.created.Before(oldest) {
			oldestKey, oldest = k, e.created
		}
	}
	if oldestKey != "" {
		delete(dev, oldestKey)
	}
}

func isDone(e *idemEntry) bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// finish records the leader's outcome. ok=false forgets the key (the
// request never ran to completion), so a retry runs it again.
func (c *idemCache) finish(deviceID, key string, e *idemEntry, resp bridgeResponse, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.resp, e.ok = resp, ok
	if !ok {
		if dev := c.entries[deviceID]; dev != nil && dev[key] == e {
			delete(dev, key)
		}
	}
	close(e.done)
}

// forget drops every entry of a device (revocation).
func (c *idemCache) forget(deviceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, deviceID)
}
