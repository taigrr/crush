package workspace

import (
	"reflect"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// StateRefreshedMsg is sent to the TUI when a cached workspace value
// changed after a background refresh, so it re-renders with fresh data.
type StateRefreshedMsg struct{}

// stateTTL bounds how long a cached value is served before a background
// refresh. Events mark everything stale immediately, so this only matters
// for changes that produce no event.
const stateTTL = 2 * time.Second

type stateEntry struct {
	val        any
	at         time.Time
	stale      bool
	refreshing bool
}

// stateCache serves frequently polled workspace state (agent busy/ready,
// queue length, permission mode) without a network round trip on every
// render. The first read of a key blocks; later reads return the cached
// value at once and refresh it in the background when it is stale. That
// keeps typing responsive when the workspace lives on another machine,
// where every RPC crosses the network.
type stateCache struct {
	mu      sync.Mutex
	entries map[string]*stateEntry
	notify  func()
}

// get returns the value for key, fetching it with fetch on first use and
// refreshing it asynchronously once stale. ok is false when the first
// fetch failed.
func (c *stateCache) get(key string, fetch func() (any, error)) (any, bool) {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*stateEntry{}
	}
	e, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		v, err := fetch()
		if err != nil {
			return nil, false
		}
		c.mu.Lock()
		c.entries[key] = &stateEntry{val: v, at: time.Now()}
		c.mu.Unlock()
		return v, true
	}
	val := e.val
	if (e.stale || time.Since(e.at) > stateTTL) && !e.refreshing {
		e.refreshing = true
		go c.refresh(key, fetch)
	}
	c.mu.Unlock()
	return val, true
}

func (c *stateCache) refresh(key string, fetch func() (any, error)) {
	v, err := fetch()
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		c.mu.Unlock()
		return
	}
	e.refreshing = false
	changed := false
	if err == nil {
		changed = !reflect.DeepEqual(e.val, v)
		e.val, e.at, e.stale = v, time.Now(), false
	}
	notify := c.notify
	c.mu.Unlock()
	if changed && notify != nil {
		notify()
	}
}

// set stores a value known to be current (e.g. after a local write).
func (c *stateCache) set(key string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*stateEntry{}
	}
	c.entries[key] = &stateEntry{val: v, at: time.Now()}
}

// invalidate marks every entry stale; the next read refreshes it.
func (c *stateCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		e.stale = true
	}
}

// clear drops everything, e.g. after switching workspace or node.
func (c *stateCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

// cachedState wires the cache to the workspace: keys are scoped to the
// targeted node and workspace, and refreshes ping the TUI.
func (w *ClientWorkspace) cachedState(name string, fetch func() (any, error)) (any, bool) {
	w.state.notifyOnce.Do(func() {
		w.state.cache.notify = func() {
			w.subMu.Lock()
			p := w.program
			w.subMu.Unlock()
			if p != nil {
				p.Send(StateRefreshedMsg{})
			}
		}
	})
	return w.state.cache.get(w.NodeID()+"|"+w.workspaceID()+"|"+name, fetch)
}

func (w *ClientWorkspace) setCachedState(name string, v any) {
	w.state.cache.set(w.NodeID()+"|"+w.workspaceID()+"|"+name, v)
}

// workspaceState holds the cache plus one-time wiring.
type workspaceState struct {
	cache      stateCache
	notifyOnce sync.Once
}

var _ tea.Msg = StateRefreshedMsg{}
