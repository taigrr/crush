package backend

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/registry"
)

func syncRow(id, title string, updatedAt int64) proto.DeviceSessionOverview {
	return proto.DeviceSessionOverview{ID: id, Title: title, UpdatedAt: updatedAt, NodeID: "n1", Root: "/proj", DataDir: "/proj/.crush"}
}

func TestSessionChangeLog(t *testing.T) {
	t.Parallel()
	base := []proto.DeviceSessionOverview{syncRow("a", "A", 1), syncRow("b", "B", 2)}
	tests := []struct {
		name        string
		next        []proto.DeviceSessionOverview
		keepRoots   map[string]bool
		wantRev     uint64
		wantChanged []string
		wantDeleted []string
	}{
		{name: "unchanged", next: base, wantRev: 0},
		{name: "title change", next: []proto.DeviceSessionOverview{syncRow("a", "A2", 1), base[1]}, wantRev: 1, wantChanged: []string{"a"}},
		{name: "busy change", next: []proto.DeviceSessionOverview{func() proto.DeviceSessionOverview { r := base[0]; r.IsBusy = true; return r }(), base[1]}, wantRev: 1, wantChanged: []string{"a"}},
		{name: "created", next: append(append([]proto.DeviceSessionOverview{}, base...), syncRow("c", "C", 3)), wantRev: 1, wantChanged: []string{"c"}},
		{name: "deleted", next: base[:1], wantRev: 1, wantDeleted: []string{"b"}},
		{name: "unreadable root keeps rows", next: nil, keepRoots: map[string]bool{"/proj": true}, wantRev: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := newSessionChangeLog()
			start, _, ok := log.observeSince(base, nil, nil)
			require.True(t, ok)
			require.Zero(t, start.Rev)
			pos, delta, ok := log.observeSince(tc.next, tc.keepRoots, &start)
			require.True(t, ok)
			require.Equal(t, tc.wantRev, pos.Rev)
			require.Equal(t, start.Epoch, pos.Epoch)
			require.ElementsMatch(t, tc.wantChanged, overviewIDs(delta.sessions))
			var deleted []string
			for _, tomb := range delta.deleted {
				require.Equal(t, "n1", tomb.NodeID)
				require.Equal(t, "/proj", tomb.Root)
				deleted = append(deleted, tomb.ID)
			}
			require.ElementsMatch(t, tc.wantDeleted, deleted)

			// Asking again from the new position is empty.
			_, again, ok := log.observeSince(tc.next, tc.keepRoots, &pos)
			require.True(t, ok)
			require.Empty(t, again.sessions)
			require.Empty(t, again.deleted)
		})
	}
}

func TestSessionChangeLogResets(t *testing.T) {
	t.Parallel()
	log := newSessionChangeLog()
	log.maxChanges = 2
	start, _, _ := log.observeSince([]proto.DeviceSessionOverview{syncRow("a", "A", 1)}, nil, nil)

	t.Run("epoch mismatch", func(t *testing.T) {
		_, _, ok := log.observeSince([]proto.DeviceSessionOverview{syncRow("a", "A", 1)}, nil, &NodeSyncPos{Epoch: "other", Rev: 0})
		require.False(t, ok)
	})
	t.Run("future revision", func(t *testing.T) {
		_, _, ok := log.observeSince([]proto.DeviceSessionOverview{syncRow("a", "A", 1)}, nil, &NodeSyncPos{Epoch: start.Epoch, Rev: 99})
		require.False(t, ok)
	})
	t.Run("window overflow", func(t *testing.T) {
		for i := range 3 {
			log.observeSince([]proto.DeviceSessionOverview{syncRow("a", fmt.Sprint(i), 1)}, nil, nil)
		}
		_, _, ok := log.observeSince([]proto.DeviceSessionOverview{syncRow("a", "2", 1)}, nil, &start)
		require.False(t, ok)
	})
	t.Run("age overflow", func(t *testing.T) {
		aged := newSessionChangeLog()
		now := time.Unix(1000, 0)
		aged.now = func() time.Time { return now }
		pos, _, _ := aged.observeSince([]proto.DeviceSessionOverview{syncRow("a", "A", 1)}, nil, nil)
		aged.observeSince([]proto.DeviceSessionOverview{syncRow("a", "B", 1)}, nil, nil)
		now = now.Add(sessionChangeLogMaxAge + time.Minute)
		_, _, ok := aged.observeSince([]proto.DeviceSessionOverview{syncRow("a", "C", 1)}, nil, &pos)
		require.False(t, ok)
	})
}

func TestSyncCursorRoundTrip(t *testing.T) {
	t.Parallel()
	nodes := map[string]NodeSyncPos{"n1": {Epoch: "e1", Rev: 3}, "n2": {Epoch: "e2", Rev: 9}}
	got, err := DecodeSyncCursor(EncodeSyncCursor(nodes))
	require.NoError(t, err)
	require.Equal(t, nodes, got)
	for _, bad := range []string{"!!", "e30", EncodeSyncCursor(nil)} {
		_, err := DecodeSyncCursor(bad)
		require.Error(t, err, bad)
	}
}

func updateSession(t *testing.T, dataDir, sql string, args ...any) {
	t.Helper()
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	_, err = conn.ExecContext(t.Context(), sql, args...)
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))
	db.ResetPool()
}

func TestSessionOverviewDeltaLocal(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	a := newDeviceTestDB(t, []string{"a1", "a2", "a3"}, 0)
	b := newDeviceTestBackend(t, map[string]string{"/proj/a": a})

	full, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Local: true, Fields: proto.SessionOverviewFieldsRow})
	require.NoError(t, err)
	require.Len(t, full.Sessions, 3)
	require.NotEmpty(t, full.SyncCursor)
	for _, s := range full.Sessions {
		require.Empty(t, s.DataDir, "fields=row drops data_dir")
	}

	empty, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Local: true, Since: full.SyncCursor})
	require.NoError(t, err)
	require.False(t, empty.Reset)
	require.Empty(t, empty.Sessions)
	require.NotNil(t, empty.Deleted)
	require.Empty(t, empty.Deleted)

	updateSession(t, a, "UPDATE sessions SET title = 'renamed' WHERE id = 'a1'")
	updateSession(t, a, "DELETE FROM sessions WHERE id = 'a2'")
	b.peekCache.reset()
	delta, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Local: true, Since: empty.SyncCursor})
	require.NoError(t, err)
	require.Equal(t, []string{"a1"}, overviewIDs(delta.Sessions))
	require.Equal(t, "renamed", delta.Sessions[0].Title)
	require.NotEmpty(t, delta.Sessions[0].DataDir, "full rows keep data_dir")
	require.Equal(t, []proto.SessionTombstone{{ID: "a2", Root: "/proj/a"}}, delta.Deleted)

	// Positions from the empty delta and a fresh one agree; the old one
	// still yields the same changes.
	again, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Local: true, Since: empty.SyncCursor})
	require.NoError(t, err)
	require.Equal(t, overviewIDs(delta.Sessions), overviewIDs(again.Sessions))

	for _, since := range []string{"garbage!", EncodeSyncCursor(map[string]NodeSyncPos{"": {Epoch: "restarted", Rev: 1}})} {
		reset, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Local: true, Since: since})
		require.NoError(t, err)
		require.True(t, reset.Reset, since)
		require.Empty(t, reset.Sessions)
		require.Empty(t, reset.Deleted)
	}
}

func TestSessionOverviewDeltaPaging(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	a := newDeviceTestDB(t, []string{"a1", "a2", "a3"}, 0)
	b := newDeviceTestBackend(t, map[string]string{"/proj/a": a})
	full, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Local: true})
	require.NoError(t, err)
	updateSession(t, a, "UPDATE sessions SET title = 'x'")
	b.peekCache.reset()

	var got []string
	q := SessionOverviewQuery{Local: true, Limit: 2, Since: full.SyncCursor}
	for range 5 {
		page, err := b.ListSessionOverviews(ctx, q)
		require.NoError(t, err)
		got = append(got, overviewIDs(page.Sessions)...)
		if page.NextCursor == nil {
			require.False(t, page.HasMore)
			break
		}
		require.True(t, page.HasMore)
		q.Cursor = *page.NextCursor
	}
	require.Equal(t, []string{"a3", "a2", "a1"}, got)
}

// deltaBridge is a cluster bridge whose members answer deltas from
// canned results.
type deltaBridge struct {
	fakeBridge
	results map[string]NodeSessionDelta
	asked   map[string]*NodeSyncPos
}

func (d *deltaBridge) NodeID() string { return "self" }

func (d *deltaBridge) RemoteOverviews(context.Context) RemoteOverviews {
	return RemoteOverviews{Reachable: map[string]bool{}, SyncPositions: map[string]NodeSyncPos{"up": {Epoch: "eu", Rev: 4}}}
}

func (d *deltaBridge) SessionOverviewDeltas(_ context.Context, since map[string]*NodeSyncPos, _ proto.SessionOverviewFields) map[string]NodeSessionDelta {
	d.asked = since
	return d.results
}

func TestSessionOverviewDeltaClusterFanOut(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	a := newDeviceTestDB(t, []string{"a1"}, 0)
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/a", DataDir: a}))
	bridge := &deltaBridge{}
	b.SetClusterBridge(bridge)

	full, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{})
	require.NoError(t, err)
	positions, err := DecodeSyncCursor(full.SyncCursor)
	require.NoError(t, err)
	require.Equal(t, NodeSyncPos{Epoch: "eu", Rev: 4}, positions["up"])
	positions["down"] = NodeSyncPos{Epoch: "ed", Rev: 7}
	positions["gone"] = NodeSyncPos{Epoch: "eg", Rev: 1}
	since := EncodeSyncCursor(positions)

	bridge.results = map[string]NodeSessionDelta{
		"up": {
			Sessions: []proto.DeviceSessionOverview{{ID: "u1", NodeID: "up", UpdatedAt: 50}},
			Deleted:  []proto.SessionTombstone{{ID: "u0", NodeID: "up", Root: "/r"}},
			Position: &NodeSyncPos{Epoch: "eu", Rev: 6},
		},
		"down": {Err: errors.New("i/o timeout")},
		"restarted": {
			Sessions: []proto.DeviceSessionOverview{{ID: "r1", NodeID: "restarted", UpdatedAt: 10}},
			Position: &NodeSyncPos{Epoch: "new", Rev: 0},
			Reset:    true,
		},
	}
	delta, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Since: since})
	require.NoError(t, err)
	require.False(t, delta.Reset)
	require.Equal(t, []string{"u1", "r1"}, overviewIDs(delta.Sessions))
	require.Equal(t, []proto.SessionTombstone{{ID: "u0", NodeID: "up", Root: "/r"}}, delta.Deleted)
	require.Equal(t, []string{"down"}, delta.StaleNodes)
	require.Equal(t, []string{"gone", "restarted"}, delta.ResetNodes)
	require.Equal(t, NodeSyncPos{Epoch: "eu", Rev: 4}, *bridge.asked["up"])

	next, err := DecodeSyncCursor(delta.SyncCursor)
	require.NoError(t, err)
	require.Equal(t, NodeSyncPos{Epoch: "eu", Rev: 6}, next["up"])
	require.Equal(t, NodeSyncPos{Epoch: "ed", Rev: 7}, next["down"], "a stale node keeps its position")
	require.Equal(t, NodeSyncPos{Epoch: "new", Rev: 0}, next["restarted"])
	require.NotContains(t, next, "gone")
	require.Equal(t, positions["self"], next["self"])

	// This node's own part needs a reset: its full list, not a global
	// reset.
	positions["self"] = NodeSyncPos{Epoch: "old", Rev: 1}
	selfReset, err := b.ListSessionOverviews(ctx, SessionOverviewQuery{Since: EncodeSyncCursor(positions)})
	require.NoError(t, err)
	require.False(t, selfReset.Reset)
	require.Contains(t, selfReset.ResetNodes, "self")
	require.Contains(t, overviewIDs(selfReset.Sessions), "a1")
}

func gzipSize(t testing.TB, raw []byte) int {
	t.Helper()
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	require.NoError(t, err)
	_, err = zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Len()
}

// TestSessionOverviewDeltaWireSize reports what a client downloads for
// a cluster-sized list: full rows, slim rows, an empty delta, and a
// one-change delta.
func TestSessionOverviewDeltaWireSize(t *testing.T) {
	t.Parallel()
	const sessions = 1600
	rows := make([]proto.DeviceSessionOverview, sessions)
	for i := range rows {
		rows[i] = proto.DeviceSessionOverview{
			ID:        fmt.Sprintf("%08x-1111-4222-8333-%012x", i, i*7919),
			Title:     fmt.Sprintf("Fix the flaky reconnect test in package %d", i%97),
			UpdatedAt: 1_760_000_000_000 - int64(i)*60_000,
			Color:     "aliceblue", Animal: "tiger", Unread: i%5 == 0,
			NodeID: fmt.Sprintf("node%dabcdef0123456789", i%3), NodeName: fmt.Sprintf("node%d", i%3),
			Root:    fmt.Sprintf("/Users/tai/code/foss/project%d", i%40),
			DataDir: fmt.Sprintf("/Users/tai/code/foss/project%d/.crush", i%40),
		}
	}
	log := newSessionChangeLog()
	start, _, _ := log.observeSince(rows, nil, nil)
	cursor := EncodeSyncCursor(map[string]NodeSyncPos{"node0": start, "node1": start, "node2": start})

	slim := append([]proto.DeviceSessionOverview(nil), rows...)
	slimRows(slim)
	_, emptyDelta, _ := log.observeSince(rows, nil, &start)
	changed := append([]proto.DeviceSessionOverview(nil), rows...)
	changed[10].IsBusy = true
	_, oneDelta, _ := log.observeSince(changed, nil, &start)
	require.Len(t, oneDelta.sessions, 1)
	slimRows(oneDelta.sessions)

	cases := []struct {
		name string
		page proto.DeviceSessionOverviewPage
	}{
		{"full", proto.DeviceSessionOverviewPage{Sessions: rows, Total: sessions, SyncCursor: cursor, Unavailable: []proto.NodeUnavailable{}}},
		{"fields=row", proto.DeviceSessionOverviewPage{Sessions: slim, Total: sessions, SyncCursor: cursor, Unavailable: []proto.NodeUnavailable{}}},
		{"empty delta", proto.DeviceSessionOverviewPage{Sessions: append([]proto.DeviceSessionOverview{}, emptyDelta.sessions...), Deleted: []proto.SessionTombstone{}, SyncCursor: cursor, Unavailable: []proto.NodeUnavailable{}}},
		{"1-change delta", proto.DeviceSessionOverviewPage{Sessions: oneDelta.sessions, Total: 1, Deleted: []proto.SessionTombstone{}, SyncCursor: cursor, Unavailable: []proto.NodeUnavailable{}}},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(tc.page)
		require.NoError(t, err)
		t.Logf("%-15s raw=%8d bytes gzip=%7d bytes", tc.name, len(raw), gzipSize(t, raw))
	}
}
