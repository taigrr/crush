package headless

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

type fakeLocator struct {
	sessions []proto.Session
	target   proto.SwarmTarget
	swarmErr error
}

func (f fakeLocator) GetSession(_ context.Context, _, id string) (*proto.Session, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return &s, nil
		}
	}
	return nil, errors.New("not found")
}

func (f fakeLocator) ListSessions(context.Context, string) ([]proto.Session, error) {
	return f.sessions, nil
}

func (f fakeLocator) ResolveSwarmAddress(context.Context, string) (proto.SwarmTarget, error) {
	return f.target, f.swarmErr
}

func TestLocateSession(t *testing.T) {
	t.Parallel()

	local := proto.Session{ID: "11111111-1111-4111-8111-11111111aaaa", Color: "red", Animal: "fox"}

	sess, elsewhere, err := LocateSession(t.Context(), fakeLocator{sessions: []proto.Session{local}}, "ws", "red-fox")
	require.NoError(t, err)
	require.Nil(t, elsewhere)
	require.Equal(t, local.ID, sess.ID)

	remote := proto.SwarmTarget{WorkspaceID: "other", SessionID: "s2", Address: "teal-owl-0002", Path: "/elsewhere"}
	sess, elsewhere, err = LocateSession(t.Context(), fakeLocator{target: remote}, "ws", "teal-owl")
	require.NoError(t, err)
	require.Nil(t, sess)
	require.Equal(t, &remote, elsewhere)

	// The server-wide lookup can find a session the local listing missed
	// (for example an archived one); it is fetched from this workspace.
	sess, elsewhere, err = LocateSession(t.Context(), fakeLocator{
		sessions: []proto.Session{local},
		target:   proto.SwarmTarget{WorkspaceID: "ws", SessionID: local.ID},
	}, "ws", "something")
	require.NoError(t, err)
	require.Nil(t, elsewhere)
	require.Equal(t, local.ID, sess.ID)

	_, _, err = LocateSession(t.Context(), fakeLocator{swarmErr: errors.New("404")}, "ws", "ghost-cat")
	require.ErrorContains(t, err, `session "ghost-cat" not found`)
	require.ErrorContains(t, err, "across all known workspaces")

	_, _, err = LocateSession(t.Context(), fakeLocator{target: proto.SwarmTarget{WorkspaceID: "x", SessionID: "y"}}, "ws", "z")
	require.ErrorContains(t, err, "no known path")
}
