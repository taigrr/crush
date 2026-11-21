package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

func TestMergeActiveSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ovs := []proto.WorkspaceOverview{
		{Root: "/other", Sessions: []proto.SessionOverview{{ID: "x"}}},
		{Root: root, SessionCount: 80, Sessions: []proto.SessionOverview{{ID: "a"}, {ID: "b"}}},
	}

	require.Equal(t, ovs, mergeActiveSession(ovs, root, nil))
	require.Equal(t, ovs, mergeActiveSession(ovs, root, &session.Session{ID: "b"}))
	require.Equal(t, ovs, mergeActiveSession(ovs, "/nowhere", &session.Session{ID: "old"}))
	require.Equal(t, ovs, mergeActiveSession(ovs, root, &session.Session{ID: "child", ParentSessionID: "a"}))

	got := mergeActiveSession(ovs, root, &session.Session{ID: "old", Title: "Old one"})
	require.Len(t, got[1].Sessions, 3)
	require.Equal(t, "old", got[1].Sessions[2].ID)
	require.Equal(t, "Old one", got[1].Sessions[2].Title)
	require.Equal(t, 80, got[1].SessionCount)
	require.Len(t, ovs[1].Sessions, 2, "input must not be modified")
	require.Equal(t, 80, ovs[1].SessionCount)
}
