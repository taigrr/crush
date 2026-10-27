package model

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/workspace"
)

// bundleCacheSize bounds how many session bundles stay in memory.
const bundleCacheSize = 32

// bundleCache keeps recently loaded session bundles so moving through the
// session list, returning from a preview, and committing a previewed
// session do not refetch what the client already has. It is only touched
// on the Update goroutine. Entries are dropped when an event reports a
// change to the session or one of its nested agent-tool sessions.
type bundleCache struct {
	entries map[string]workspace.SessionBundle
	order   []string
}

func (c *bundleCache) get(id string) (workspace.SessionBundle, bool) {
	b, ok := c.entries[id]
	return b, ok
}

func (c *bundleCache) put(id string, b workspace.SessionBundle) {
	if c.entries == nil {
		c.entries = map[string]workspace.SessionBundle{}
	}
	if _, ok := c.entries[id]; !ok {
		c.order = append(c.order, id)
	}
	c.entries[id] = b
	for len(c.order) > bundleCacheSize {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
}

// invalidate drops the bundle for sessionID and any bundle that embeds it
// as a nested agent-tool session.
func (c *bundleCache) invalidate(sessionID string) {
	if sessionID == "" || len(c.entries) == 0 {
		return
	}
	for id, b := range c.entries {
		_, nested := b.Children[sessionID]
		if id == sessionID || nested || (strings.Contains(sessionID, "$$") && nestedOwner(b, sessionID)) {
			delete(c.entries, id)
		}
	}
	kept := c.order[:0]
	for _, id := range c.order {
		if _, ok := c.entries[id]; ok {
			kept = append(kept, id)
		}
	}
	c.order = kept
}

// nestedOwner reports whether a not-yet-cached nested session (message
// "<msgID>$$<tool>") belongs to a message in b.
func nestedOwner(b workspace.SessionBundle, sessionID string) bool {
	msgID, _, _ := strings.Cut(sessionID, "$$")
	for _, m := range b.Messages {
		if m.ID == msgID {
			return true
		}
	}
	return false
}

// bundleLoadedMsg carries a prefetched bundle into the cache.
type bundleLoadedMsg struct {
	id     string
	bundle workspace.SessionBundle
}

// prefetchBundle loads id in the background unless it is cached.
func (m *UI) prefetchBundle(id, root string) tea.Cmd {
	if id == "" {
		return nil
	}
	if _, ok := m.bundles.get(id); ok {
		return nil
	}
	ws := m.com.Workspace
	return func() tea.Msg {
		b, err := ws.SessionBundle(context.Background(), root, id)
		if err != nil {
			return nil
		}
		return bundleLoadedMsg{id: id, bundle: b}
	}
}

// previewRoot normalizes a sidebar root for loading: "" for the current
// workspace.
func (m *UI) previewRoot(root string) string {
	if root != "" && m.isCurrentWorkspace(root) {
		return ""
	}
	return root
}
