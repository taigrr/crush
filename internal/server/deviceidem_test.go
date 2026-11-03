package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIdemCache(t *testing.T) {
	t.Parallel()
	c := newIdemCache()
	now := time.Now()
	c.now = func() time.Time { return now }
	fp := idemFingerprint("POST", "/v1/x", []byte("a"))

	e, leader, conflict := c.begin("dev1", "k", fp)
	require.True(t, leader)
	require.False(t, conflict)
	again, leader, _ := c.begin("dev1", "k", fp)
	require.False(t, leader, "a retry waits for the first attempt")
	require.Same(t, e, again)

	// Same key, different request: conflict.
	_, _, conflict = c.begin("dev1", "k", idemFingerprint("POST", "/v1/x", []byte("b")))
	require.True(t, conflict)
	_, _, conflict = c.begin("dev1", "k", idemFingerprint("POST", "/v1/y", []byte("a")))
	require.True(t, conflict)

	// Keys are per device: another device's identical key is independent.
	_, leader, conflict = c.begin("dev2", "k", idemFingerprint("POST", "/v1/z", nil))
	require.True(t, leader)
	require.False(t, conflict)

	c.finish("dev1", "k", e, bridgeResponse{Status: 201}, true)
	<-e.done
	require.Equal(t, 201, e.resp.Status)

	// Entries expire after the TTL.
	now = now.Add(idempotencyTTL + time.Second)
	_, leader, _ = c.begin("dev1", "k", fp)
	require.True(t, leader)
}

func TestIdemCacheForgetsFailuresAndBounds(t *testing.T) {
	t.Parallel()
	c := newIdemCache()
	fp := idemFingerprint("POST", "/v1/x", nil)
	e, _, _ := c.begin("dev", "k", fp)
	c.finish("dev", "k", e, bridgeResponse{}, false)
	_, leader, _ := c.begin("dev", "k", fp)
	require.True(t, leader, "an attempt that never completed is not remembered")

	for i := range maxIdempotencyPerDevice * 2 {
		key := "key" + time.Duration(i).String()
		e, leader, _ := c.begin("dev", key, fp)
		require.True(t, leader)
		c.finish("dev", key, e, bridgeResponse{}, true)
	}
	require.LessOrEqual(t, len(c.entries["dev"]), maxIdempotencyPerDevice)
	c.forget("dev")
	require.Empty(t, c.entries["dev"])
}

func TestValidIdemKey(t *testing.T) {
	t.Parallel()
	require.True(t, validIdemKey("p-5b1e2c_X9"))
	for _, bad := range []string{"", "a b", "a.b", "é", string(make([]byte, 65))} {
		require.False(t, validIdemKey(bad), bad)
	}
}
