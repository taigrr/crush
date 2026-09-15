package agent

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReadyBarrierConcurrentRebuildAndWait is a regression test for the
// daemon crash "panic: sync: WaitGroup is reused before previous Wait
// has returned". The coordinator used to keep a single errgroup.Group
// as a persistent readiness latch: buildAgent called readyWg.Go (an
// Add) while every in-flight run blocked on readyWg.Wait. A burst of
// swarm messages could rebuild a busy coordinator, racing an Add
// against a Wait once the counter had hit zero, panicking the whole
// process.
//
// It exercises the real installReadyBarrier (the rebuild path) against
// awaitReady (the run path) under heavy concurrency. With the shared-
// group implementation this panics; with the atomically-swapped
// barrier it must complete cleanly.
func TestReadyBarrierConcurrentRebuildAndWait(t *testing.T) {
	t.Parallel()

	c := &coordinator{}
	// Publish an initial barrier so awaitReady has something to load.
	c.installReadyBarrier(func() error { return nil })

	const (
		rebuilders = 8
		waiters    = 32
		iterations = 200
	)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Rebuilders continuously install fresh barriers, mirroring
	// concurrent buildAgent/InitAgent calls triggered by a swarm burst.
	for range rebuilders {
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				c.installReadyBarrier(
					func() error { return nil },
					func() error { return nil },
				)
			}
		})
	}

	// Waiters continuously load-and-wait, mirroring in-flight runs
	// blocking on readiness at the top of run().
	for range waiters {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				require.NoError(t, c.awaitReady())
			}
		})
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestReadyBarrierPropagatesError verifies awaitReady surfaces the
// barrier's error (a build failure) and that a subsequent successful
// rebuild clears it, so a transient failure does not permanently wedge
// the coordinator.
func TestReadyBarrierPropagatesError(t *testing.T) {
	t.Parallel()

	c := &coordinator{}

	buildErr := errors.New("build failed")
	c.installReadyBarrier(func() error { return buildErr })
	require.ErrorIs(t, c.awaitReady(), buildErr)

	// A fresh, successful barrier replaces the failed one.
	c.installReadyBarrier(func() error { return nil })
	require.NoError(t, c.awaitReady())
}

// TestReadyBarrierNilIsReady verifies awaitReady treats an unbuilt
// coordinator (no barrier ever installed) as ready rather than
// panicking on a nil load.
func TestReadyBarrierNilIsReady(t *testing.T) {
	t.Parallel()

	c := &coordinator{}
	require.NoError(t, c.awaitReady())
}
