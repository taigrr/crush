package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BenchmarkClusterJoinApprovalLatency measures the time from an approval
// on the inviter to Join returning on the joiner, using the default poll
// interval. Approval happens a little after the request is pending, as a
// human would approve it.
func BenchmarkClusterJoinApprovalLatency(b *testing.B) {
	quietLogs(b)
	a := newTestNode(b, "v1.0.0")
	aAddr := freeAddr(b)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(b, err)
	require.NoError(b, a.Start(aAddr))
	ctx := context.Background()

	var total time.Duration
	for b.Loop() {
		b.StopTimer()
		joiner := newTestNode(b, "v1.0.0")
		approved := make(chan time.Time, 1)
		done := make(chan error, 1)
		go func() {
			done <- joiner.Join(ctx, a.Listening(), token, JoinOptions{
				OnPending: func(string) {
					go func() {
						time.Sleep(300 * time.Millisecond)
						_, err := a.Approve(ctx, joiner.Identity().ID)
						if err != nil {
							b.Errorf("approve: %v", err)
						}
						approved <- time.Now()
					}()
				},
			})
		}()
		require.NoError(b, <-done)
		total += time.Since(<-approved)
		b.StartTimer()
	}
	b.ReportMetric(float64(total.Microseconds())/float64(b.N)/1000, "ms-approve-to-joined")
}

func TestClusterJoinDenyWakesLongPoll(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	a := newTestNode(t, "v1.0.0")
	j := newTestNode(t, "v1.0.0")
	aAddr := freeAddr(t)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))

	denied := make(chan time.Time, 1)
	err = j.Join(ctx, a.Listening(), token, JoinOptions{
		PollInterval: time.Minute,
		OnPending: func(string) {
			go func() {
				time.Sleep(100 * time.Millisecond)
				_, err := a.Deny(j.Identity().ID)
				if err != nil {
					t.Errorf("deny: %v", err)
				}
				denied <- time.Now()
			}()
		},
	})
	require.ErrorContains(t, err, "denied")
	require.Less(t, time.Since(<-denied), 5*time.Second)
}
