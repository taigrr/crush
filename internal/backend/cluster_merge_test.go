package backend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

func TestMergeReplicas(t *testing.T) {
	t.Parallel()
	since := time.UnixMilli(1_700_000_000_000)
	replicas := func() map[string][]proto.WorkspaceOverview {
		return map[string][]proto.WorkspaceOverview{
			"up":   {{Root: "/a", Sessions: []proto.SessionOverview{{ID: "u1", OwnerNode: "up"}}}},
			"down": {{Root: "/a", Sessions: []proto.SessionOverview{{ID: "d1", OwnerNode: "down"}}}},
			"gone": {{Root: "/g", Sessions: []proto.SessionOverview{{ID: "g1", OwnerNode: "gone"}}}},
		}
	}
	local := []proto.WorkspaceOverview{{Root: "/a", Sessions: []proto.SessionOverview{{ID: "l1"}}}}
	remote := RemoteOverviews{
		Overviews: []proto.WorkspaceOverview{
			{NodeID: "down", NodeName: "mini2", Unavailable: true, Error: "timeout"},
		},
		// "up" answered with no workspaces at all; its replicas must
		// still stay hidden.
		Reachable: map[string]bool{"up": true},
		Down:      map[string]NodeDown{"down": {Since: since, Error: "timeout"}},
	}
	names := map[string]string{"down": "mini2", "gone": "old"}
	out := mergeReplicas(local, replicas(), remote, func(id string) string { return names[id] })

	require.Len(t, out, 3)
	require.Equal(t, "/a", out[0].Root)
	ids := []string{}
	for _, s := range out[0].Sessions {
		ids = append(ids, s.ID)
	}
	require.Equal(t, []string{"l1", "d1"}, ids)
	d1 := out[0].Sessions[1]
	require.True(t, d1.ReadOnly)
	require.Equal(t, "mini2", d1.OwnerName)
	require.Equal(t, "owner unreachable: timeout", d1.ReplicaReason)
	require.Equal(t, since.UnixMilli(), d1.OwnerUnreachableSince)

	require.Equal(t, "/g", out[1].Root)
	require.True(t, out[1].Sessions[0].ReadOnly)
	require.Equal(t, "owner not in cluster", out[1].Sessions[0].ReplicaReason)

	require.True(t, out[2].Unavailable)
}

func TestMergeReplicas_UnavailableMarkerIsNotReachable(t *testing.T) {
	t.Parallel()
	remote := RemoteOverviews{
		Overviews: []proto.WorkspaceOverview{{NodeID: "n", Unavailable: true}},
	}
	reps := map[string][]proto.WorkspaceOverview{"n": {{Root: "/r", Sessions: []proto.SessionOverview{{ID: "s"}}}}}
	out := mergeReplicas(nil, reps, remote, func(string) string { return "n" })
	require.Len(t, out, 2)
	require.True(t, out[0].Sessions[0].ReadOnly)
}
