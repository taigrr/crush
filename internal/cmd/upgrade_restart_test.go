package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

func TestClientIsNewer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		client, server string
		want           bool
	}{
		{"v0.95.23", "v0.95.22", true},
		{"0.95.23", "v0.95.23", true},
		// An old `crush cluster upgrade` polling the daemon it just
		// upgraded must not restart it on every poll.
		{"v0.95.22", "v0.95.23", false},
		{"v0.95.9", "v0.95.23", false},
		{"devel", "v0.95.23", true},
		{"v0.95.23", "unknown", true},
	} {
		require.Equal(t, tc.want, clientIsNewer(tc.client, tc.server), "%s vs %s", tc.client, tc.server)
	}
}

func TestUpgradeProgress(t *testing.T) {
	t.Parallel()
	done := map[string]bool{}
	arrived, behind := upgradeProgress([]proto.ClusterPingResult{
		{NodeID: "a", Name: "mini1", Version: "v2"},
		{NodeID: "b", Name: "laptop", Error: "unreachable"},
	}, "v2", done)
	require.Equal(t, []string{"mini1"}, arrived)
	require.Equal(t, []string{"laptop (unreachable)"}, behind)

	// mini1 restarts after arriving; it must neither block completion
	// nor vanish from an otherwise empty "behind" list.
	arrived, behind = upgradeProgress([]proto.ClusterPingResult{
		{NodeID: "a", Name: "mini1", Error: "connection refused"},
		{NodeID: "b", Name: "laptop", Version: "v2"},
	}, "v2", done)
	require.Equal(t, []string{"laptop"}, arrived)
	require.Empty(t, behind)
}
