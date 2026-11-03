package cluster

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdvertisedAddrs(t *testing.T) {
	t.Parallel()
	eps := []Endpoint{
		{Kind: EndpointDirect, Addr: "10.0.0.2:7731"},
		{Kind: EndpointDirect, Addr: "box.ts.net:7731"},
		{Kind: EndpointNATS, Addr: "10.0.0.2:7732"},
		{Kind: EndpointDirect, Addr: "10.0.0.2:9999"},
	}
	want := []string{"10.0.0.2:41234", "box.ts.net:41234"}
	for _, bound := range []string{":41234", "0.0.0.0:41234", "[::]:41234"} {
		require.Equal(t, want, AdvertisedAddrs(bound, eps), "a wildcard bind expands to the member's hosts: %s", bound)
	}
	require.Equal(t, []string{"10.0.0.2:41234"}, AdvertisedAddrs("10.0.0.2:41234", eps), "a specific bind is its host only")
	require.Equal(t, []string{"192.168.9.9:41234"}, AdvertisedAddrs("192.168.9.9:41234", eps), "even one the member does not list")
	require.Empty(t, AdvertisedAddrs(":0", eps), "an unbound ephemeral port is not advertised")
	require.Empty(t, AdvertisedAddrs("", eps))
	require.Empty(t, AdvertisedAddrs("garbage", eps))
}

func TestMergeDevice(t *testing.T) {
	t.Parallel()
	old := &DeviceEndpoints{V: 1, Seq: 10, Client: DeviceListener{State: DeviceListenerOn, Addrs: []string{"a:1"}}}
	newer := &DeviceEndpoints{V: 1, Seq: 11, Client: DeviceListener{State: DeviceListenerOff}}
	stale := &DeviceEndpoints{V: 1, Seq: 9, Client: DeviceListener{State: DeviceListenerOff}}

	require.Equal(t, newer, mergeDevice("b", "b", old, newer), "the member itself moves its record forward")
	require.Equal(t, old, mergeDevice("b", "b", old, stale), "a reordered older push never rolls back")
	require.Equal(t, old, mergeDevice("b", "b", old, old))
	require.Equal(t, old, mergeDevice("b", "c", old, newer), "another member cannot change b's record")
	require.Equal(t, old, mergeDevice("b", "b", old, nil), "a push without a record keeps the stored one")
	require.Nil(t, mergeDevice("b", "c", nil, newer), "nor create it")
	require.Equal(t, newer, mergeDevice("b", "b", nil, newer), "a member sets its own first record")

	got := mergeDevice("b", "b", old, newer)
	got.Client.Addrs = append(got.Client.Addrs, "x")
	require.Empty(t, newer.Client.Addrs, "records are copied")
}

// TestDeviceAdvertisementPropagates: a member's advertisement reaches the
// others through the members push, later ones replace earlier ones, and
// a push from a third member cannot overwrite it. In-process nodes on
// loopback only.
func TestDeviceAdvertisementPropagates(t *testing.T) {
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
	deviceOf := func(n *Node) *DeviceEndpoints {
		m, _ := mustSnapshot(t, n).Member(bID)
		return m.Device
	}

	first, errs, err := b.AdvertiseDevice(ctx, DeviceEndpoints{
		Client: DeviceListener{State: DeviceListenerOn, Addrs: []string{"10.0.0.2:41000"}},
		WS:     DeviceListener{State: DeviceListenerOff},
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, DeviceEndpointsVersion, first.V)
	for _, n := range []*Node{a, b, c} {
		require.Equal(t, &first, deviceOf(n), n.Identity().ID)
	}

	second, errs, err := b.AdvertiseDevice(ctx, DeviceEndpoints{Client: DeviceListener{State: DeviceListenerIdle, Addrs: []string{"10.0.0.2:41000"}}, WS: DeviceListener{State: DeviceListenerOff}})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Greater(t, second.Seq, first.Seq)
	require.Equal(t, &second, deviceOf(a))

	// C pushes a spoofed, "newer" record for B; A keeps B's own.
	bm, _ := mustSnapshot(t, c).Member(bID)
	bm.Device = &DeviceEndpoints{V: 1, Seq: second.Seq + 1000, Client: DeviceListener{State: DeviceListenerOn, Addrs: []string{"6.6.6.6:1"}}}
	am, _ := mustSnapshot(t, c).Member(a.Identity().ID)
	require.NoError(t, c.callMember(ctx, am, http.MethodPost, "/cluster/v1/members", membersBody{Members: []Member{bm}}, nil))
	require.Equal(t, &second, deviceOf(a), "only B may change B's record")

	// A relayed record for a member the receiver does not hold a record
	// for is ignored as well: only the member itself sets it.
	cm2, _ := mustSnapshot(t, a).Member(c.Identity().ID)
	require.Nil(t, cm2.Device)
	fake := cm2
	fake.Device = &DeviceEndpoints{V: 1, Seq: 1 << 62, WS: DeviceListener{State: DeviceListenerOn, Addrs: []string{"6.6.6.6:1"}}}
	require.NoError(t, b.callMember(ctx, am, http.MethodPost, "/cluster/v1/members", membersBody{Members: []Member{fake}}, nil))
	cm2, _ = mustSnapshot(t, a).Member(c.Identity().ID)
	require.Nil(t, cm2.Device, "B cannot plant a record for C")

	// B replaying its own older record is ignored too.
	bm.Device = first.Clone()
	bself, _ := mustSnapshot(t, b).Member(bID)
	bself.Device = first.Clone()
	require.NoError(t, b.callMember(ctx, am, http.MethodPost, "/cluster/v1/members", membersBody{Members: []Member{bself}}, nil))
	require.Equal(t, &second, deviceOf(a), "an older record never replaces a newer one")
}
