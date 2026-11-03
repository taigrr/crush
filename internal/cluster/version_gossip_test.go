package cluster

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMemberVersionsSurviveStaleGossip covers `cluster status` showing a
// pre-upgrade version: a node's stored self row keeps the version it
// joined with, and gossiping that row used to overwrite the version peers
// had observed first-hand.
func TestMemberVersionsSurviveStaleGossip(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a, b, c := newTestNode(t, "v1.0.0"), newTestNode(t, "v1.0.0"), newTestNode(t, "v1.0.0")
	aAddr := freeAddr(t)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))
	joinAndApprove(t, ctx, b, a, token)
	joinAndApprove(t, ctx, c, a, token)
	for _, n := range []*Node{a, b, c} {
		require.Eventually(t, func() bool { return len(mustSnapshot(t, n).Members) == 3 }, 10*time.Second, 10*time.Millisecond)
	}
	bID := b.Identity().ID
	versionOf := func(n *Node, id string) string {
		m, _ := mustSnapshot(t, n).Member(id)
		return m.Version
	}

	// B's stored self row predates its upgrade.
	require.NoError(t, b.store.Update(func(st *State) error {
		for i := range st.Members {
			if st.Members[i].NodeID == bID {
				st.Members[i].Version = "v0.9.0"
			}
		}
		return nil
	}))
	for _, m := range b.Status().Members {
		if m.NodeID == bID {
			require.Equal(t, "v1.0.0", m.Version, "status reports the running version for self")
		}
	}

	_, errs, err := b.AdvertiseDevice(ctx, DeviceEndpoints{WS: DeviceListener{State: DeviceListenerOff}})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, "v1.0.0", versionOf(a, bID))

	// C relays a stale row for B; A keeps what it saw from B itself.
	stale, _ := mustSnapshot(t, c).Member(bID)
	stale.Version = "v0.9.0"
	am, _ := mustSnapshot(t, c).Member(a.Identity().ID)
	require.NoError(t, c.callMember(ctx, am, http.MethodPost, "/cluster/v1/members", membersBody{Members: []Member{stale}}, nil))
	require.Equal(t, "v1.0.0", versionOf(a, bID))
}

func TestGossipVersion(t *testing.T) {
	t.Parallel()
	require.Equal(t, "v2", gossipVersion("v2", "v1"))
	require.Equal(t, "v1", gossipVersion("", "v1"))
}
