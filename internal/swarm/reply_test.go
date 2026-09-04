package swarm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplyTracker_RequireDedupesBySender(t *testing.T) {
	t.Parallel()
	tr := NewReplyTracker()
	tr.Require("child", ReplyObligation{SenderSessionID: "parent", Body: "first", Nudges: 1})
	tr.Require("child", ReplyObligation{SenderSessionID: "parent", Body: "second"})
	tr.Require("child", ReplyObligation{SenderSessionID: "other", Body: "x"})

	got := tr.Pending("child")
	require.Len(t, got, 2)
	require.Equal(t, "second", got[0].Body)
	require.Equal(t, 0, got[0].Nudges, "re-requiring resets the nudge count")
	require.Equal(t, "other", got[1].SenderSessionID)
}

func TestReplyTracker_IgnoresSelfAndEmpty(t *testing.T) {
	t.Parallel()
	tr := NewReplyTracker()
	tr.Require("child", ReplyObligation{SenderSessionID: "child"})
	tr.Require("child", ReplyObligation{})
	tr.Require("", ReplyObligation{SenderSessionID: "parent"})
	require.Empty(t, tr.Pending("child"))
	require.Empty(t, tr.Pending(""))
}

func TestReplyTracker_FulfillOnlyMatchingSender(t *testing.T) {
	t.Parallel()
	tr := NewReplyTracker()
	tr.Require("child", ReplyObligation{SenderSessionID: "parent"})
	tr.Require("child", ReplyObligation{SenderSessionID: "other"})

	require.False(t, tr.Fulfill("child", "stranger"))
	require.Len(t, tr.Pending("child"), 2)

	require.True(t, tr.Fulfill("child", "parent"))
	got := tr.Pending("child")
	require.Len(t, got, 1)
	require.Equal(t, "other", got[0].SenderSessionID)

	require.True(t, tr.Fulfill("child", "other"))
	require.Empty(t, tr.Pending("child"))
	require.False(t, tr.Fulfill("child", "other"))
}

func TestReplyTracker_NudgeThenExhaust(t *testing.T) {
	t.Parallel()
	tr := NewReplyTracker()
	tr.Require("child", ReplyObligation{SenderSessionID: "parent"})

	due, exhausted := tr.Nudge("child", 2)
	require.Len(t, due, 1)
	require.Empty(t, exhausted)
	require.Equal(t, 1, due[0].Nudges)

	due, exhausted = tr.Nudge("child", 2)
	require.Len(t, due, 1)
	require.Empty(t, exhausted)
	require.Equal(t, 2, due[0].Nudges)

	due, exhausted = tr.Nudge("child", 2)
	require.Empty(t, due)
	require.Len(t, exhausted, 1)
	require.Equal(t, "parent", exhausted[0].SenderSessionID)
	require.Empty(t, tr.Pending("child"), "exhausted obligations are dropped")

	due, exhausted = tr.Nudge("child", 2)
	require.Empty(t, due)
	require.Empty(t, exhausted)
}

func TestReplyTracker_Clear(t *testing.T) {
	t.Parallel()
	tr := NewReplyTracker()
	tr.Require("child", ReplyObligation{SenderSessionID: "parent"})
	cleared := tr.Clear("child")
	require.Len(t, cleared, 1)
	require.Empty(t, tr.Pending("child"))
	require.Empty(t, tr.Clear("child"))
}

func TestReplyTracker_NilSafe(t *testing.T) {
	t.Parallel()
	var tr *ReplyTracker
	tr.Require("a", ReplyObligation{SenderSessionID: "b"})
	require.False(t, tr.Fulfill("a", "b"))
	require.Nil(t, tr.Pending("a"))
	due, exhausted := tr.Nudge("a", 1)
	require.Nil(t, due)
	require.Nil(t, exhausted)
	require.Nil(t, tr.Clear("a"))
}
