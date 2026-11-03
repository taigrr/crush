package server

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
)

func TestRemoteViewCollect_ReportsFailedNodes(t *testing.T) {
	t.Parallel()
	v := &remoteView{nodes: map[string]*nodeView{
		"ok":      {ok: true, tried: true, at: time.Now(), ovs: []proto.WorkspaceOverview{{Root: "/p", NodeID: "ok"}}},
		"down":    {tried: true, err: "connection refused"},
		"pending": {loading: true},
	}}
	members := []cluster.Member{
		{NodeID: "ok", Name: "a"},
		{NodeID: "down", Name: "b"},
		{NodeID: "pending", Name: "c"},
		{NodeID: "unknown", Name: "d"},
	}
	res := v.collect(members)
	out := res.Overviews
	require.Len(t, out, 3)
	require.Equal(t, "/p", out[0].Root)
	require.False(t, out[0].Unavailable)
	require.False(t, out[0].Stale)
	require.Equal(t, proto.WorkspaceOverview{NodeID: "down", NodeName: "b", Unavailable: true, Error: "connection refused"}, out[1])
	require.True(t, out[2].Unavailable)
	require.Equal(t, "c", out[2].NodeName)
	require.NotEmpty(t, out[2].Error)
	require.Equal(t, map[string]bool{"ok": true}, res.Reachable)
	require.Contains(t, res.Down, "down")
	require.Contains(t, res.Down, "pending")
}

func TestRemoteView_StaleWhileError(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1_700_000_000_000)
	v := &remoteView{nodes: map[string]*nodeView{}, now: func() time.Time { return now }}
	members := []cluster.Member{{NodeID: "n", Name: "mini2"}}
	good := []proto.WorkspaceOverview{{Root: "/p", NodeID: "n", Sessions: []proto.SessionOverview{{ID: "s"}}}}

	require.True(t, v.apply("n", good, nil))
	fetched := now

	// A failed refresh within the grace keeps serving the last good
	// data, flagged stale, and the node stays reachable.
	now = now.Add(30 * time.Second)
	require.True(t, v.apply("n", nil, errors.New("context deadline exceeded")))
	res := v.collect(members)
	require.True(t, res.Reachable["n"])
	require.Empty(t, res.Down)
	require.Len(t, res.Overviews, 1)
	ov := res.Overviews[0]
	require.Equal(t, "/p", ov.Root)
	require.True(t, ov.Stale)
	require.Equal(t, fetched.UnixMilli(), ov.StaleSince)
	require.Equal(t, "context deadline exceeded", ov.LastError)
	failedSince := now

	// Repeated failures do not re-announce a change.
	now = now.Add(30 * time.Second)
	require.False(t, v.apply("n", nil, errors.New("context deadline exceeded")))
	require.True(t, v.collect(members).Reachable["n"])

	// Past the grace the node is down and replicas take over.
	now = fetched.Add(staleGrace + time.Second)
	require.True(t, v.apply("n", nil, errors.New("context deadline exceeded")))
	res = v.collect(members)
	require.False(t, res.Reachable["n"])
	require.Equal(t, failedSince, res.Down["n"].Since)
	require.Len(t, res.Overviews, 1)
	require.True(t, res.Overviews[0].Unavailable)
	require.Equal(t, fetched.UnixMilli(), res.Overviews[0].StaleSince)
	require.False(t, v.apply("n", nil, errors.New("again")))

	// Recovery clears staleness.
	now = now.Add(time.Second)
	require.True(t, v.apply("n", good, nil))
	res = v.collect(members)
	require.True(t, res.Reachable["n"])
	require.False(t, res.Overviews[0].Stale)
	require.Empty(t, res.Overviews[0].LastError)
}

func TestRemoteView_GraceExpiresWithoutNewResult(t *testing.T) {
	t.Parallel()
	now := time.Now()
	v := &remoteView{nodes: map[string]*nodeView{}, now: func() time.Time { return now }}
	v.apply("n", []proto.WorkspaceOverview{{Root: "/p"}}, nil)
	v.apply("n", nil, errors.New("boom"))
	v.nodes["n"].loading = true
	now = now.Add(staleGrace + time.Second)
	res := v.collect([]cluster.Member{{NodeID: "n"}})
	require.False(t, res.Reachable["n"])
	require.True(t, res.Overviews[0].Unavailable)
}
