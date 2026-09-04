package journal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/swarm"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dataDir := t.TempDir()
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir) })
	return New(conn, db.New(conn), dataDir)
}

func TestQueueRoundTrip(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()

	entries := []QueuedPrompt{
		{SessionID: "s1", RunID: "r1", Prompt: "first", Attachments: []message.Attachment{{FileName: "a.txt", MimeType: "text/plain", Content: []byte("hi")}}},
		{SessionID: "s1", Prompt: "second", SwarmParts: []message.SwarmMessage{{Text: "msg", SenderSessionID: "p", RequireReply: true}}},
	}
	require.NoError(t, s.SaveQueue(ctx, "s1", entries))
	require.NoError(t, s.SaveQueue(ctx, "s2", []QueuedPrompt{{SessionID: "s2", Prompt: "other"}}))

	loaded, err := s.LoadQueue(ctx)
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	for i := range loaded["s1"] {
		require.WithinDuration(t, time.Now(), loaded["s1"][i].CreatedAt, time.Minute)
		require.True(t, loaded["s1"][i].Fresh(time.Now()))
		loaded["s1"][i].CreatedAt = time.Time{}
	}
	require.Equal(t, entries, loaded["s1"])
	require.Equal(t, "other", loaded["s2"][0].Prompt)

	// Replacing with a shorter queue drops the tail; empty deletes.
	require.NoError(t, s.SaveQueue(ctx, "s1", entries[1:]))
	loaded, err = s.LoadQueue(ctx)
	require.NoError(t, err)
	require.Len(t, loaded["s1"], 1)
	require.Equal(t, "second", loaded["s1"][0].Prompt)

	require.NoError(t, s.SaveQueue(ctx, "s1", nil))
	loaded, err = s.LoadQueue(ctx)
	require.NoError(t, err)
	require.NotContains(t, loaded, "s1")
	require.Contains(t, loaded, "s2")

	require.NoError(t, s.ClearQueues(ctx))
	loaded, err = s.LoadQueue(ctx)
	require.NoError(t, err)
	require.Empty(t, loaded)
}

func TestRepliesRoundTrip(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	obs := []swarm.ReplyObligation{
		{SenderSessionID: "parent", SenderWorkspaceID: "ws", SenderAddress: "red-fox-abcd", Body: "do it", Nudges: 1},
		{SenderSessionID: "other", SenderAddress: "blue-owl-1234"},
	}
	require.NoError(t, s.SaveReplies("worker", obs))

	loaded, err := s.LoadReplies()
	require.NoError(t, err)
	require.Equal(t, obs, loaded["worker"])

	require.NoError(t, s.SaveReplies("worker", nil))
	loaded, err = s.LoadReplies()
	require.NoError(t, err)
	require.Empty(t, loaded)
}

// TestReplyTrackerWritesThrough exercises the tracker against the real
// store: mutations land in the DB and a fresh tracker rehydrates them.
func TestReplyTrackerWritesThrough(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	tr := swarm.NewPersistentReplyTracker(s)
	tr.Require("worker", swarm.ReplyObligation{SenderSessionID: "parent", SenderAddress: "red-fox-abcd", Body: "hi"})
	tr.Require("worker", swarm.ReplyObligation{SenderSessionID: "aunt", SenderAddress: "blue-owl-1234"})
	tr.Nudge("worker", 5)

	fresh := swarm.NewPersistentReplyTracker(s)
	pending := fresh.Pending("worker")
	require.Len(t, pending, 2)
	require.Equal(t, 1, pending[0].Nudges)
	require.Equal(t, "parent", pending[0].SenderSessionID)

	// Fulfilling on the fresh tracker is visible to a third one.
	require.True(t, fresh.Fulfill("worker", "parent"))
	third := swarm.NewPersistentReplyTracker(s)
	require.Len(t, third.Pending("worker"), 1)
	require.Equal(t, "aunt", third.Pending("worker")[0].SenderSessionID)

	// After DetachJournal a Clear no longer touches the DB.
	third.DetachJournal()
	third.Clear("worker")
	fourth := swarm.NewPersistentReplyTracker(s)
	require.Len(t, fourth.Pending("worker"), 1)
}

func TestHandoffMarker(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	require.False(t, s.ConsumeHandoff(), "no marker until one is written")
	require.NoError(t, s.MarkHandoff())
	require.True(t, s.ConsumeHandoff())
	require.False(t, s.ConsumeHandoff(), "the marker applies to exactly one replay")
}

func TestQueuedPrompt_Fresh(t *testing.T) {
	t.Parallel()
	now := time.Now()
	require.True(t, QueuedPrompt{CreatedAt: now.Add(-time.Hour)}.Fresh(now))
	require.False(t, QueuedPrompt{CreatedAt: now.Add(-ReplayTTL - time.Minute)}.Fresh(now))
	require.False(t, QueuedPrompt{}.Fresh(now), "unpersisted entries are never fresh")
}

func TestLoadReplies_StaleRowsDroppedWithoutHandoff(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()

	// One fresh and one stale row, written directly so the timestamp can
	// be aged.
	require.NoError(t, s.SaveReplies("worker", []swarm.ReplyObligation{{SenderSessionID: "fresh", SenderAddress: "a"}}))
	require.NoError(t, s.q.InsertSwarmReplyObligation(ctx, db.InsertSwarmReplyObligationParams{
		ObligatedSessionID: "worker", OwedToSessionID: "stale", OwedToAddress: "b",
		CreatedAt: time.Now().Add(-ReplayTTL - time.Hour).UnixMilli(),
	}))
	require.NoError(t, s.q.InsertSwarmReplyObligation(ctx, db.InsertSwarmReplyObligationParams{
		ObligatedSessionID: "other", OwedToSessionID: "stale2", OwedToAddress: "c",
		CreatedAt: time.Now().Add(-ReplayTTL - time.Hour).UnixMilli(),
	}))

	// With a hand-off marker everything is trusted.
	require.NoError(t, s.MarkHandoff())
	loaded, err := s.LoadReplies()
	require.NoError(t, err)
	require.Len(t, loaded["worker"], 2)
	require.Len(t, loaded["other"], 1)

	// Without it, stale rows are dropped and deleted.
	require.True(t, s.ConsumeHandoff())
	loaded, err = s.LoadReplies()
	require.NoError(t, err)
	require.Len(t, loaded["worker"], 1)
	require.Equal(t, "fresh", loaded["worker"][0].SenderSessionID)
	require.NotContains(t, loaded, "other")
	rows, err := s.q.ListSwarmReplyObligations(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1, "stale rows are deleted, not just skipped")

	require.NoError(t, s.ClearReplies(ctx))
	rows, err = s.q.ListSwarmReplyObligations(ctx)
	require.NoError(t, err)
	require.Empty(t, rows)
}
