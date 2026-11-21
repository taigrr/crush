package backend

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

func TestLimitOverviews(t *testing.T) {
	t.Parallel()
	mk := func(n int) []proto.SessionOverview {
		out := make([]proto.SessionOverview, n)
		for i := range out {
			out[i].ID = string(rune('a' + i))
		}
		return out
	}
	ovs := []proto.WorkspaceOverview{{Root: "/a", Sessions: mk(5)}, {Root: "/b", Sessions: mk(2)}}

	got := limitOverviews(ovs, 0)
	require.Len(t, got[0].Sessions, 5)
	require.Zero(t, got[0].SessionCount)

	got = limitOverviews(ovs, 3)
	require.Equal(t, []string{"a", "b", "c"}, []string{got[0].Sessions[0].ID, got[0].Sessions[1].ID, got[0].Sessions[2].ID})
	require.Len(t, got[0].Sessions, 3)
	require.Equal(t, 5, got[0].SessionCount)
	require.Len(t, got[1].Sessions, 2)
	require.Zero(t, got[1].SessionCount)
}

func TestLimitOverviewsKeepsBusy(t *testing.T) {
	t.Parallel()
	sessions := make([]proto.SessionOverview, 6)
	for i := range sessions {
		sessions[i].ID = string(rune('a' + i))
		sessions[i].IsBusy = i < 4
	}
	got := limitOverviews([]proto.WorkspaceOverview{{Root: "/a", Sessions: sessions}}, 2)
	require.Len(t, got[0].Sessions, 4)
	require.Equal(t, 6, got[0].SessionCount)
	for _, s := range got[0].Sessions {
		require.True(t, s.IsBusy)
	}
}
