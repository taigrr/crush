package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
)

func TestRemoteViewCollect_ReportsFailedNodes(t *testing.T) {
	t.Parallel()
	v := &remoteView{nodes: map[string]*nodeView{
		"ok":      {ok: true, tried: true, ovs: []proto.WorkspaceOverview{{Root: "/p", NodeID: "ok"}}},
		"down":    {tried: true, err: "connection refused"},
		"pending": {loading: true},
	}}
	members := []cluster.Member{
		{NodeID: "ok", Name: "a"},
		{NodeID: "down", Name: "b"},
		{NodeID: "pending", Name: "c"},
		{NodeID: "unknown", Name: "d"},
	}
	out := v.collect(members)
	require.Len(t, out, 3)
	require.Equal(t, "/p", out[0].Root)
	require.False(t, out[0].Unavailable)
	require.Equal(t, proto.WorkspaceOverview{NodeID: "down", NodeName: "b", Unavailable: true, Error: "connection refused"}, out[1])
	require.True(t, out[2].Unavailable)
	require.Equal(t, "c", out[2].NodeName)
	require.NotEmpty(t, out[2].Error)
}
