package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/proto"
)

func TestSessionOverviewsDeltaRoute(t *testing.T) {
	h, root := newSessionRoutesServer(t)

	var full proto.DeviceSessionOverviewPage
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/session-overviews?local=1&fields=row", nil, &full))
	require.Len(t, full.Sessions, 1)
	require.Empty(t, full.Sessions[0].DataDir)
	require.Equal(t, root, full.Sessions[0].Root)
	require.NotEmpty(t, full.SyncCursor)

	var raw map[string]any
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/session-overviews?local=1&since="+url.QueryEscape(full.SyncCursor), nil, &raw))
	require.Equal(t, []any{}, raw["sessions"])
	require.Equal(t, []any{}, raw["deleted"])
	require.NotContains(t, raw, "reset")
	require.NotEmpty(t, raw["sync_cursor"])

	var reset proto.DeviceSessionOverviewPage
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/session-overviews?local=1&since=bogus", nil, &reset))
	require.True(t, reset.Reset)
	require.Empty(t, reset.Sessions)

	var perr proto.Error
	require.Equal(t, http.StatusBadRequest, doJSON(t, h, "GET", "/v1/session-overviews?fields=everything", nil, &perr))
}

// fakeMember answers session overview requests from canned pages, keyed
// by whether since was sent.
type fakeMember struct {
	delta []proto.DeviceSessionOverviewPage
	full  []proto.DeviceSessionOverviewPage
	err   error
	paths []string
}

func (f *fakeMember) call(_ context.Context, path string, out any) error {
	f.paths = append(f.paths, path)
	if f.err != nil {
		return f.err
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return err
	}
	q := parsed.Query()
	pages := f.full
	if q.Has(proto.SessionOverviewsParamSince) {
		pages = f.delta
	}
	idx := 0
	if q.Has(proto.SessionOverviewsParamCursor) {
		idx = 1
	}
	*out.(*proto.DeviceSessionOverviewPage) = pages[idx]
	return nil
}

func TestFetchNodeSessionDelta(t *testing.T) {
	t.Parallel()
	pos := &backend.NodeSyncPos{Epoch: "e", Rev: 3}
	newCursor := backend.EncodeSyncCursor(map[string]backend.NodeSyncPos{"m": {Epoch: "e", Rev: 5}})
	next := "page2"
	row := func(id string) proto.DeviceSessionOverview { return proto.DeviceSessionOverview{ID: id, NodeID: "m"} }

	t.Run("delta pages", func(t *testing.T) {
		t.Parallel()
		f := &fakeMember{delta: []proto.DeviceSessionOverviewPage{
			{Sessions: []proto.DeviceSessionOverview{row("a")}, Deleted: []proto.SessionTombstone{{ID: "z"}}, SyncCursor: newCursor, NextCursor: &next},
			{Sessions: []proto.DeviceSessionOverview{row("b")}, SyncCursor: newCursor},
		}}
		res := fetchNodeSessionDelta(t.Context(), f.call, "m", pos, proto.SessionOverviewFieldsRow)
		require.NoError(t, res.Err)
		require.False(t, res.Reset)
		require.Equal(t, &backend.NodeSyncPos{Epoch: "e", Rev: 5}, res.Position)
		require.Len(t, res.Sessions, 2)
		require.Len(t, res.Deleted, 1)
		require.Contains(t, f.paths[0], "fields=row")
		require.Contains(t, f.paths[0], "local=1")
	})
	t.Run("member reset falls back to its full list", func(t *testing.T) {
		t.Parallel()
		f := &fakeMember{
			delta: []proto.DeviceSessionOverviewPage{{Reset: true}},
			full:  []proto.DeviceSessionOverviewPage{{Sessions: []proto.DeviceSessionOverview{row("a"), row("b")}, SyncCursor: newCursor}},
		}
		res := fetchNodeSessionDelta(t.Context(), f.call, "m", pos, "")
		require.NoError(t, res.Err)
		require.True(t, res.Reset)
		require.Len(t, res.Sessions, 2)
		require.NotNil(t, res.Position)
		require.False(t, strings.Contains(f.paths[1], proto.SessionOverviewsParamSince+"="))
	})
	t.Run("legacy member ignores since", func(t *testing.T) {
		t.Parallel()
		f := &fakeMember{delta: []proto.DeviceSessionOverviewPage{{Sessions: []proto.DeviceSessionOverview{row("a")}}}}
		res := fetchNodeSessionDelta(t.Context(), f.call, "m", pos, "")
		require.NoError(t, res.Err)
		require.True(t, res.Reset)
		require.Nil(t, res.Position)
	})
	t.Run("unreachable", func(t *testing.T) {
		t.Parallel()
		f := &fakeMember{err: errors.New("dial tcp: i/o timeout")}
		res := fetchNodeSessionDelta(t.Context(), f.call, "m", pos, "")
		require.Error(t, res.Err)
		require.Nil(t, fetchSyncPosition(t.Context(), f.call, "m"))
	})
}
