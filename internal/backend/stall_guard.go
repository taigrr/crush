package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrDataDirUnavailable reports that reading a workspace's data directory
// did not finish in time, or that an earlier read of it is still stuck.
var ErrDataDirUnavailable = errors.New("workspace data directory unavailable")

const (
	// defaultPeekTimeout bounds a read-only session listing of one
	// detached workspace.
	defaultPeekTimeout = 3 * time.Second
	// defaultReplicaReadTimeout bounds one replication read of a
	// detached workspace's database.
	defaultReplicaReadTimeout = 10 * time.Second
)

// stallGuard runs filesystem-bound work with a deadline the caller always
// gets back from, even when the underlying syscall never returns (macOS
// can block open(2) on a removable volume indefinitely while it waits on
// a privacy prompt). At most one call per key is in flight: callers queue
// behind it until their own deadline, and once it has outlived the
// timeout the key counts as stuck and callers fail fast until it finishes.
// So a wedged path costs one goroutine, not one per request.
type stallGuard struct {
	timeout  time.Duration
	mu       sync.Mutex
	inflight map[string]*guardCall
}

type guardCall struct {
	start time.Time
	done  chan struct{}
}

type guardResult[T any] struct {
	val T
	err error
}

func (g *stallGuard) limit() time.Duration {
	if g.timeout > 0 {
		return g.timeout
	}
	return defaultPeekTimeout
}

// acquire claims key's slot, waiting for an in-flight call to finish
// until ctx expires. It fails fast when the in-flight call is stuck.
func (g *stallGuard) acquire(ctx context.Context, key string) (*guardCall, error) {
	limit := g.limit()
	for {
		g.mu.Lock()
		cur, busy := g.inflight[key]
		if !busy {
			if g.inflight == nil {
				g.inflight = map[string]*guardCall{}
			}
			call := &guardCall{start: time.Now(), done: make(chan struct{})}
			g.inflight[key] = call
			g.mu.Unlock()
			return call, nil
		}
		stuckFor := time.Since(cur.start)
		g.mu.Unlock()
		if stuckFor >= limit {
			return nil, fmt.Errorf("%w: %s: still blocked after %s", ErrDataDirUnavailable, key, stuckFor.Round(time.Second))
		}
		select {
		case <-cur.done:
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %s: timed out after %s", ErrDataDirUnavailable, key, limit)
		}
	}
}

func (g *stallGuard) release(key string, call *guardCall) {
	g.mu.Lock()
	if g.inflight[key] == call {
		delete(g.inflight, key)
	}
	g.mu.Unlock()
	close(call.done)
}

// guarded runs fn for key under g. fn gets a context that expires with
// the caller's deadline; when it ignores it, the caller still returns.
func guarded[T any](ctx context.Context, g *stallGuard, key string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	limit := g.limit()
	ctx, cancel := context.WithTimeout(ctx, limit)
	call, err := g.acquire(ctx, key)
	if err != nil {
		cancel()
		return zero, err
	}
	res := make(chan guardResult[T], 1)
	go func() {
		defer cancel()
		defer g.release(key, call)
		val, err := fn(ctx)
		res <- guardResult[T]{val: val, err: err}
	}()
	select {
	case r := <-res:
		return r.val, r.err
	case <-ctx.Done():
		return zero, fmt.Errorf("%w: %s: timed out after %s", ErrDataDirUnavailable, key, limit)
	}
}
