package workspace

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStateCacheServesStaleAndRefreshesInBackground(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var value atomic.Int32
	value.Store(1)
	notified := make(chan struct{}, 4)
	c := &stateCache{notify: func() { notified <- struct{}{} }}
	fetch := func() (any, error) {
		calls.Add(1)
		return int(value.Load()), nil
	}

	v, ok := c.get("k", fetch)
	require.True(t, ok)
	require.Equal(t, 1, v)

	// Fresh: no network.
	v, _ = c.get("k", fetch)
	require.Equal(t, 1, v)
	require.EqualValues(t, 1, calls.Load())

	// After an event the old value is served immediately while a
	// background refresh fetches the new one and pings the UI.
	value.Store(2)
	c.invalidate()
	start := time.Now()
	v, _ = c.get("k", fetch)
	require.Equal(t, 1, v)
	require.Less(t, time.Since(start), 50*time.Millisecond)
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("no refresh notification")
	}
	v, _ = c.get("k", fetch)
	require.Equal(t, 2, v)

	// A failing first fetch reports !ok and is retried next time.
	_, ok = c.get("bad", func() (any, error) { return nil, errors.New("down") })
	require.False(t, ok)
	v, ok = c.get("bad", func() (any, error) { return true, nil })
	require.True(t, ok)
	require.Equal(t, true, v)
}
