package server

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
)

func newClusterTestServer(t *testing.T) (*Server, *client.Client) {
	t.Helper()
	srv := &Server{}
	srv.backend = backend.New(t.Context(), nil, func() {})
	// Never touch the real user registry or credentials.
	srv.backend.SetRegistryPathForTest(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	srv.credsPath = filepath.Join(t.TempDir(), "crush.json")
	srv.installHandler()
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	node, err := cluster.NewNode(t.TempDir(), "v1.2.3")
	require.NoError(t, err)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	require.NoError(t, srv.EnableCluster(node, addr, freeTCPAddr(t)))
	t.Cleanup(srv.stopMesh)
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	c, err := client.NewClient("", "tcp", hs.Listener.Addr().String())
	require.NoError(t, err)
	return srv, c
}

func TestClusterControlAPIPairing(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	a, ac := newClusterTestServer(t)
	b, bc := newClusterTestServer(t)

	st, err := ac.ClusterStatus(ctx)
	require.NoError(t, err)
	require.False(t, st.InCluster)

	aEP := []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: a.clusterAddr}}
	token, err := ac.ClusterInit(ctx, proto.ClusterInitRequest{Endpoints: aEP})
	require.NoError(t, err)
	require.True(t, a.backend.Persistent(), "cluster nodes must not auto-exit")

	// Approve from A as soon as the request shows up.
	go func() {
		for ctx.Err() == nil {
			st, err := ac.ClusterStatus(ctx)
			if err == nil && len(st.Pending) == 1 {
				if _, err := ac.ClusterApprove(ctx, st.Pending[0].Name); err != nil {
					t.Errorf("approve: %v", err)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	bEP := []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: b.clusterAddr}}
	joined, err := bc.ClusterJoin(ctx, proto.ClusterJoinRequest{
		Addr:           a.clusterAddr,
		Token:          token,
		Endpoints:      bEP,
		TimeoutSeconds: 15,
	})
	require.NoError(t, err)
	require.True(t, joined.InCluster)
	require.Len(t, joined.Members, 2)
	require.NotEmpty(t, b.Cluster().Listening())

	pings, err := ac.ClusterPing(ctx)
	require.NoError(t, err)
	require.Len(t, pings, 1)
	require.Empty(t, pings[0].Error, fmt.Sprintf("%+v", pings[0]))
	require.Equal(t, "v1.2.3", pings[0].Version)

	_, err = ac.ClusterInit(ctx, proto.ClusterInitRequest{})
	require.ErrorContains(t, err, "already belongs")
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func TestClusterMeshBridgesAttentionProxyAndOverviews(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	a, ac := newClusterTestServer(t)
	b, bc := newClusterTestServer(t)
	token, err := ac.ClusterInit(ctx, proto.ClusterInitRequest{
		Endpoints: []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: a.clusterAddr}},
	})
	require.NoError(t, err)
	go func() {
		for ctx.Err() == nil {
			if st, err := ac.ClusterStatus(ctx); err == nil && len(st.Pending) == 1 {
				_, _ = ac.ClusterApprove(ctx, st.Pending[0].ID)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	_, err = bc.ClusterJoin(ctx, proto.ClusterJoinRequest{
		Addr:           a.clusterAddr,
		Token:          token,
		Endpoints:      []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: b.clusterAddr}},
		TimeoutSeconds: 20,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return a.mesh != nil && b.mesh != nil && a.mesh.NumRoutes() > 0 && b.mesh.NumRoutes() > 0
	}, 15*time.Second, 50*time.Millisecond)

	// Attention raised on B reaches A's local attention channel, tagged.
	events := a.backend.AttentionEvents(ctx)
	require.Eventually(t, func() bool {
		b.backend.PublishClusterJoin("req-1", "laptop")
		select {
		case ev := <-events:
			return ev.Payload.NodeID == b.cluster.Identity().ID && ev.Payload.Detail == "laptop"
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, 10*time.Second, 10*time.Millisecond)

	// A client attached to A can call B's API through the node proxy.
	vi, err := ac.ForNode(b.cluster.Identity().ID).VersionInfo(ctx)
	require.NoError(t, err)
	require.Equal(t, proto.ProtocolVersion, vi.Protocol)

	// Heartbeats populate the dashboard with the peer online.
	require.Eventually(t, func() bool {
		rows, err := ac.ClusterDashboard(ctx)
		if err != nil || len(rows) != 2 || !rows[0].Self {
			return false
		}
		peer := rows[1]
		return peer.Online && peer.Stats != nil && peer.Stats.MemTotal > 0 && peer.RTTMillis >= 0
	}, 20*time.Second, 200*time.Millisecond)

	// Cluster-wide overviews do not fail when the peer has no workspaces.
	_, err = ac.ListWorkspaceOverviews(ctx)
	require.NoError(t, err)
}

// pairServers forms a two-node cluster of a and b and waits for the mesh.
func pairServers(t *testing.T, ctx context.Context, a, b *Server, ac, bc *client.Client) {
	t.Helper()
	token, err := ac.ClusterInit(ctx, proto.ClusterInitRequest{
		Endpoints: []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: a.clusterAddr}},
	})
	require.NoError(t, err)
	go func() {
		for ctx.Err() == nil {
			if st, err := ac.ClusterStatus(ctx); err == nil && len(st.Pending) == 1 {
				_, _ = ac.ClusterApprove(ctx, st.Pending[0].ID)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	_, err = bc.ClusterJoin(ctx, proto.ClusterJoinRequest{
		Addr:           a.clusterAddr,
		Token:          token,
		Endpoints:      []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: b.clusterAddr}},
		TimeoutSeconds: 20,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return a.mesh != nil && b.mesh != nil && a.mesh.NumRoutes() > 0 && b.mesh.NumRoutes() > 0
	}, 15*time.Second, 50*time.Millisecond)
}

// TestClusterMoveRefusesSecondOwner drives a real move through the
// source API, the mesh, and the destination's preflight. Both test nodes
// share one filesystem, so the destination sees the session already
// active at the same path and must refuse rather than create a second
// owner.
func TestClusterMoveRefusesSecondOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	a, ac := newClusterTestServer(t)
	b, bc := newClusterTestServer(t)
	pairServers(t, ctx, a, b, ac, bc)

	project := t.TempDir()
	ws, err := ac.CreateWorkspace(ctx, proto.Workspace{Path: project, ClientID: "c1"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ac.DeleteWorkspace(context.Background(), ws.ID) })
	sess, err := ac.CreateSession(ctx, ws.ID, "offload me")
	require.NoError(t, err)

	_, err = ac.MoveSession(ctx, proto.MoveSessionRequest{
		WorkspaceID: ws.ID,
		SessionID:   sess.ID,
		TargetNode:  b.cluster.Identity().ID,
	})
	require.ErrorContains(t, err, "already exists and is active")

	// The source copy is untouched.
	got, err := ac.GetSession(ctx, ws.ID, sess.ID)
	require.NoError(t, err)
	require.Equal(t, "offload me", got.Title)
}

func TestRemoteViewAnswersFromMemoryAfterFirstLoad(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	a, ac := newClusterTestServer(t)
	b, bc := newClusterTestServer(t)
	pairServers(t, ctx, a, b, ac, bc)

	// First load may wait (bounded); later loads answer from cache even
	// when the peer is slow or gone.
	start := time.Now()
	_, err := ac.ListWorkspaceOverviews(ctx)
	require.NoError(t, err)
	require.Less(t, time.Since(start), firstLoadWait+2*time.Second)

	require.NoError(t, b.cluster.Stop(ctx)) // peer API unreachable now
	start = time.Now()
	_, err = ac.ListWorkspaceOverviews(ctx)
	require.NoError(t, err)
	require.Less(t, time.Since(start), 300*time.Millisecond, "cached view must not wait on the network")
}
