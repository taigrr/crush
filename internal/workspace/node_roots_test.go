package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/proto"
)

func TestNodeRootQualification(t *testing.T) {
	t.Parallel()
	q := QualifyNodeRoot("n1", "/a/b")
	ref, root, ok := ParseNodeRoot(q)
	require.True(t, ok)
	require.Equal(t, "n1", ref)
	require.Equal(t, "/a/b", root)
	require.Equal(t, "/a/b", DisplayRoot(q))
	_, root, ok = ParseNodeRoot("/plain")
	require.False(t, ok)
	require.Equal(t, "/plain", root)
}

func TestQualifyOverviewsRelativeToTarget(t *testing.T) {
	t.Parallel()
	home, err := client.NewClient("", "tcp", "127.0.0.1:1")
	require.NoError(t, err)
	w := NewClientWorkspace(home, proto.Workspace{ID: "w"})

	ovs := func() []proto.WorkspaceOverview {
		return []proto.WorkspaceOverview{{Root: "/home/p"}, {Root: "/mini/p", NodeID: "n1"}}
	}
	got := w.qualifyOverviews(ovs())
	require.Equal(t, "/home/p", got[0].Root)
	require.Equal(t, "crush+node://n1/mini/p", got[1].Root)

	// After retargeting at n1, its roots are plain and home's qualified.
	w.target.Store(home.ForNode("n1"))
	got = w.qualifyOverviews(ovs())
	require.Equal(t, "crush+node://home/home/p", got[0].Root)
	require.Equal(t, "/mini/p", got[1].Root)

	nodeID, c, plain := w.resolveRoot(got[0].Root)
	require.Empty(t, nodeID)
	require.Empty(t, c.NodeID())
	require.Equal(t, "/home/p", plain)
}
