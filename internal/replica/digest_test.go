package replica

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

func TestApplyLeavesLocalChangelogAlone(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	a, b := open(t), open(t)
	addSession(t, b, "mine", "B")
	before := MaxSeq(ctx, b)

	addSession(t, a, "s1", "A")
	addMessage(t, a, "m1", "s1")
	batch, err := Serve(ctx, a, "nodeA", -1, 0, false)
	require.NoError(t, err)
	st, err := Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)
	require.Positive(t, st.Applied)
	require.Equal(t, before, MaxSeq(ctx, b), "replicated rows must not be announced as local changes")

	// The owner's timestamps survive the apply, even though inserting
	// messages updates the session row on this node.
	exec(t, a, `UPDATE sessions SET updated_at = 1000 WHERE id = 's1'`)
	batch, err = Serve(ctx, a, "nodeA", -1, 0, false)
	require.NoError(t, err)
	_, err = Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)
	require.Equal(t, 1, count(t, b, `SELECT COUNT(*) FROM sessions WHERE id = 's1' AND updated_at = 1000`))
	require.Equal(t, before, MaxSeq(ctx, b))

	// Reapplying the same rows changes nothing.
	st, err = Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)
	require.Zero(t, st.Applied)
	require.Positive(t, st.Unchanged)

	// Local writes keep sequencing after the restored counter.
	addMessage(t, b, "bm", "mine")
	require.Greater(t, MaxSeq(ctx, b), before)
}

func TestDigestRepairsOnlyDriftedSessions(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	a, b := open(t), open(t)
	addSession(t, a, "s1", "one")
	addMessage(t, a, "m1", "s1")
	addSession(t, a, "s2", "two")

	batch, err := Serve(ctx, a, "nodeA", -1, 0, false)
	require.NoError(t, err)
	_, err = Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)

	owner, err := OwnedDigest(ctx, a, "nodeA")
	require.NoError(t, err)
	local, err := PeerDigest(ctx, b, "nodeA")
	require.NoError(t, err)
	require.Empty(t, Drifted(owner, local))

	// Drift s2 behind the changelog's back.
	exec(t, a, `UPDATE sessions SET title='drifted' WHERE id='s2'`)
	owner, err = OwnedDigest(ctx, a, "nodeA")
	require.NoError(t, err)
	drifted := Drifted(owner, local)
	require.Equal(t, []string{"s2"}, drifted)

	repair, err := ServeSessions(ctx, a, "nodeA", drifted)
	require.NoError(t, err)
	require.True(t, repair.Repair)
	for _, c := range repair.Changes {
		require.NotEqual(t, "s1", c.Row["id"].V)
		require.NotEqual(t, "s1", c.Row["session_id"].V)
	}
	_, err = Apply(ctx, b, "nodeA", roundTrip(t, repair))
	require.NoError(t, err)
	require.Equal(t, 1, count(t, b, `SELECT COUNT(*) FROM sessions WHERE id='s2' AND title='drifted'`))

	local, err = PeerDigest(ctx, b, "nodeA")
	require.NoError(t, err)
	require.Empty(t, Drifted(owner, local))
}
