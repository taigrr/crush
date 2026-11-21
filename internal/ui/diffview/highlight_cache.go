package diffview

import (
	"sync"

	"github.com/alecthomas/chroma/v2"
)

// sharedHighlightLimit bounds the process-wide highlight cache. When full the
// cache is reset rather than tracking recency, which keeps lookups cheap.
const sharedHighlightLimit = 8192

// sharedHighlightKey identifies a highlighted line independent of the
// DiffView instance, so re-rendering the same diff (scrolling, resizing,
// re-drawing tool results) reuses earlier lexing and formatting work.
type sharedHighlightKey struct {
	style      *chroma.Style
	lexer      string
	r, g, b, a uint32
	source     string
}

type highlightCache struct {
	mu      sync.RWMutex
	entries map[sharedHighlightKey]string
}

var sharedHighlights = &highlightCache{entries: make(map[sharedHighlightKey]string)}

func (c *highlightCache) get(k sharedHighlightKey) (string, bool) {
	c.mu.RLock()
	v, ok := c.entries[k]
	c.mu.RUnlock()
	return v, ok
}

func (c *highlightCache) put(k sharedHighlightKey, v string) {
	c.mu.Lock()
	if len(c.entries) >= sharedHighlightLimit {
		clear(c.entries)
	}
	c.entries[k] = v
	c.mu.Unlock()
}

func lexerName(l chroma.Lexer) string {
	if cfg := l.Config(); cfg != nil {
		return cfg.Name
	}
	return ""
}
