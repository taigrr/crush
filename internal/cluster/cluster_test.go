package cluster

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestNode(t *testing.T, version string) *Node {
	t.Helper()
	n, err := NewNode(t.TempDir(), version)
	require.NoError(t, err)
	t.Cleanup(func() { _ = n.Stop(context.Background()) })
	return n
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// joinAndApprove runs a join from joiner against inviter, approving it on
// inviter once pending, then starts joiner's listener.
func joinAndApprove(t *testing.T, ctx context.Context, joiner, inviter *Node, token string) {
	t.Helper()
	addr := freeAddr(t)
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	eps := []Endpoint{{Kind: EndpointDirect, Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}}

	done := make(chan error, 1)
	go func() {
		done <- joiner.Join(ctx, inviter.Listening(), token, JoinOptions{
			Endpoints:    eps,
			PollInterval: 20 * time.Millisecond,
			OnPending: func(string) {
				go func() {
					_, err := inviter.Approve(ctx, joiner.Identity().ID)
					if err != nil {
						t.Errorf("approve: %v", err)
					}
				}()
			},
		})
	}()
	require.NoError(t, <-done)
	require.NoError(t, joiner.Start(addr))
}

func TestClusterPairingRotationAndLeave(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	a := newTestNode(t, "v1.0.0")
	b := newTestNode(t, "v1.0.0")
	c := newTestNode(t, "v1.0.0")

	aAddr := freeAddr(t)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))

	joinAndApprove(t, ctx, b, a, token)
	joinAndApprove(t, ctx, c, b, token)

	// A learns about C through B's broadcast, which B sends after C's
	// approval is recorded, so C's join can return first; wait for it.
	for _, n := range []*Node{a, b, c} {
		require.Eventually(t, func() bool {
			st, err := n.Store().Snapshot()
			return err == nil && len(st.Members) == 3 && len(st.Pending) == 0
		}, 10*time.Second, 10*time.Millisecond, "node %s never saw all three members", n.Identity().ID)
	}

	// Rotation reaches every member and they keep talking afterwards.
	newToken, errs, err := a.RotateKey(ctx)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.NotEqual(t, token, newToken)
	for _, n := range []*Node{b, c} {
		tok, err := n.Token()
		require.NoError(t, err)
		require.Equal(t, newToken, tok)
	}
	cm, ok := mustSnapshot(t, a).Member(c.Identity().ID)
	require.True(t, ok)
	info, _, err := a.Ping(ctx, cm)
	require.NoError(t, err)
	require.Equal(t, c.Identity().ID, info.NodeID)

	// The old token can no longer start a join.
	d := newTestNode(t, "v1.0.0")
	jctx, jcancel := context.WithTimeout(ctx, 2*time.Second)
	err = d.Join(jctx, a.Listening(), token, JoinOptions{PollInterval: 20 * time.Millisecond})
	jcancel()
	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, mustSnapshot(t, a).Pending)

	// Leaving removes the node from everyone else.
	require.Empty(t, c.Leave(ctx))
	require.False(t, c.Store().InCluster())
	for _, n := range []*Node{a, b} {
		_, ok := mustSnapshot(t, n).Member(c.Identity().ID)
		require.False(t, ok)
	}
}

func mustSnapshot(t *testing.T, n *Node) State {
	t.Helper()
	st, err := n.Store().Snapshot()
	require.NoError(t, err)
	return st
}

func TestClusterDenyAndVersionMismatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	a := newTestNode(t, "v1.0.0")
	aAddr := freeAddr(t)
	token, err := a.Init(nil)
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))

	old := newTestNode(t, "v0.9.0")
	err = old.Join(ctx, a.Listening(), token, JoinOptions{})
	require.ErrorContains(t, err, "version mismatch")

	b := newTestNode(t, "v1.0.0")
	done := make(chan error, 1)
	go func() {
		done <- b.Join(ctx, a.Listening(), token, JoinOptions{
			PollInterval: 20 * time.Millisecond,
			OnPending: func(string) {
				go func() {
					_, err := a.Deny(b.Identity().Name)
					if err != nil {
						t.Errorf("deny: %v", err)
					}
				}()
			},
		})
	}()
	require.ErrorContains(t, <-done, "denied")
	require.False(t, b.Store().InCluster())
}

func TestTokenRoundTrip(t *testing.T) {
	t.Parallel()
	key, err := NewKey()
	require.NoError(t, err)
	tok := EncodeToken("abc-123", key)
	id, got, err := DecodeToken(tok)
	require.NoError(t, err)
	require.Equal(t, "abc-123", id)
	require.Equal(t, key, got)

	_, _, err = DecodeToken("nope")
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestCheckCompatibleRejectsDevBuilds(t *testing.T) {
	require.ErrorContains(t, CheckCompatible("devel", "devel", ProtocolVersion), "released build")
	t.Setenv("CRUSH_CLUSTER_ALLOW_DEV", "1")
	require.NoError(t, CheckCompatible("devel", "devel", ProtocolVersion))
	require.ErrorContains(t, CheckCompatible("v1", "v1", ProtocolVersion+1), "protocol")
}

func TestDevVersionDetection(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "devel", "unknown", "v0.95.4+dirty", "v0.0.0-0.20251231235959-06c807842604"} {
		require.True(t, isDevVersion(v), v)
	}
	require.False(t, isDevVersion("v0.95.4"))
}
