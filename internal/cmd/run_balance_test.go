package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
)

type fakeBalanceAPI struct {
	nodes      []cluster.NodeDashboard
	dashErr    error
	moveErr    error
	configErr  error
	moveCalled []proto.MoveSessionRequest
}

func (f *fakeBalanceAPI) ClusterDashboard(context.Context) ([]cluster.NodeDashboard, error) {
	return f.nodes, f.dashErr
}

func (f *fakeBalanceAPI) MoveSession(_ context.Context, req proto.MoveSessionRequest) (proto.MoveSessionResponse, error) {
	f.moveCalled = append(f.moveCalled, req)
	return proto.MoveSessionResponse{TargetNode: req.TargetNode}, f.moveErr
}

func (f *fakeBalanceAPI) NodeConfigured(context.Context, string) error { return f.configErr }

func twoNodes() []cluster.NodeDashboard {
	return []cluster.NodeDashboard{
		{Member: cluster.Member{NodeID: "self", Name: "here"}, Self: true, Online: true, Stats: &cluster.NodeStats{CPUPercent: 95, MemPercent: 90, CPUCores: 4, Load1: 6}},
		{Member: cluster.Member{NodeID: "idle", Name: "there"}, Online: true, Stats: &cluster.NodeStats{CPUPercent: 2, MemPercent: 20, CPUCores: 8, Load1: 0.2}},
	}
}

func TestBalanceSessionMoves(t *testing.T) {
	t.Parallel()
	api := &fakeBalanceAPI{nodes: twoNodes()}
	var out bytes.Buffer
	target, ok := balanceSession(t.Context(), api, "ws", "/p", "s1", cluster.DefaultBalanceMargin, &out)
	require.True(t, ok)
	require.Equal(t, "idle", target.Member.NodeID)
	require.Len(t, api.moveCalled, 1)
	require.Equal(t, proto.MoveSessionRequest{WorkspaceID: "ws", SessionID: "s1", TargetNode: "idle"}, api.moveCalled[0])
	require.Contains(t, out.String(), "moving to there")
}

func TestBalanceSessionFallsBack(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		api  *fakeBalanceAPI
		want string
		move bool
	}{
		"move fails": {
			api:  &fakeBalanceAPI{nodes: twoNodes(), moveErr: errors.New("mkdir /Volumes/Store1: permission denied")},
			want: "move to there failed, running locally: mkdir /Volumes/Store1: permission denied",
			move: true,
		},
		"no cluster":      {api: &fakeBalanceAPI{dashErr: errors.New("cluster is not enabled")}, want: "cluster unavailable"},
		"target unusable": {api: &fakeBalanceAPI{nodes: twoNodes(), configErr: errors.New("it has no AI provider configured")}, want: "not moving to there"},
		"no other node":   {api: &fakeBalanceAPI{nodes: twoNodes()[:1]}, want: "no other node is online"},
		"local within margin": {api: &fakeBalanceAPI{nodes: []cluster.NodeDashboard{
			{Member: cluster.Member{NodeID: "self"}, Self: true, Online: true, Stats: &cluster.NodeStats{CPUPercent: 30, MemPercent: 30}},
			{Member: cluster.Member{NodeID: "b"}, Online: true, Stats: &cluster.NodeStats{CPUPercent: 25, MemPercent: 30}},
		}}, want: "within the margin"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			_, ok := balanceSession(t.Context(), tc.api, "ws", "/p", "s1", cluster.DefaultBalanceMargin, &out)
			require.False(t, ok)
			require.Contains(t, out.String(), tc.want)
			require.Equal(t, tc.move, len(tc.api.moveCalled) == 1)
		})
	}
}

func TestResolveAutobalance(t *testing.T) {
	t.Parallel()
	on := &config.ClusterConfig{Autobalance: true}
	require.False(t, resolveAutobalance(false, false, "", nil))
	require.True(t, resolveAutobalance(false, false, "", on))
	require.False(t, resolveAutobalance(true, false, "1", on), "an explicit --balance=false wins")
	require.True(t, resolveAutobalance(true, true, "0", nil))
	require.True(t, resolveAutobalance(false, false, "true", nil))
	require.False(t, resolveAutobalance(false, false, "0", on))
	require.True(t, resolveAutobalance(false, false, "bogus", on), "unparsable env falls back to config")

	require.InDelta(t, cluster.DefaultBalanceMargin, autobalanceMargin(nil), 1e-9)
	big := 3.0
	require.InDelta(t, 1.0, autobalanceMargin(&config.ClusterConfig{AutobalanceMargin: &big}), 1e-9)
}

// TestClientBalanceAPINodeConfiguredDecodedConfig drives NodeConfigured
// through a real client against an HTTP server, so the config is decoded
// exactly as autobalance sees a remote node's config. An empty object
// (what older servers send) must report "not configured" instead
// of panicking on the nil providers map.
func TestClientBalanceAPINodeConfiguredDecodedConfig(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		payload string
		wantErr bool
	}{
		"empty object":   {payload: "{}", wantErr: true},
		"null":           {payload: "null", wantErr: true},
		"configured":     {payload: `{"configured":true}`, wantErr: false},
		"not configured": {payload: `{"configured":false}`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.payload))
			}))
			defer srv.Close()
			c, err := client.NewClient("", "tcp", srv.Listener.Addr().String())
			require.NoError(t, err)

			err = clientBalanceAPI{c}.NodeConfigured(t.Context(), "node-b")
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
