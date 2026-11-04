package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
)

const (
	// sessionOverviewsPath is the session overview route on a member.
	sessionOverviewsPath = "/v1/session-overviews"
	// sessionDeltaTimeout bounds one member's part of a delta request.
	sessionDeltaTimeout = 5 * time.Second
	// maxSessionDeltaPages bounds the pages read from one member.
	maxSessionDeltaPages = 100
	// localFlag is the value of the local query parameter.
	localFlag = "1"
)

var (
	_ backend.SessionDeltaSource = (*meshBridge)(nil)

	errMemberRecentlyDown = errors.New("member recently unreachable")
	errTooManyDeltaPages  = errors.New("too many session overview pages")
)

// memberCall fetches path (relative to the API root, with query) from a
// member and decodes the JSON answer into out.
type memberCall func(ctx context.Context, path string, out any) error

// SessionOverviewDeltas asks every member, in parallel, for its session
// overview changes since its position (see [backend.SessionDeltaSource]).
func (m *meshBridge) SessionOverviewDeltas(ctx context.Context, since map[string]*backend.NodeSyncPos, fields proto.SessionOverviewFields) map[string]backend.NodeSessionDelta {
	members := m.node.Members()
	out := make(map[string]backend.NodeSessionDelta, len(members))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, mem := range members {
		wg.Go(func() {
			var res backend.NodeSessionDelta
			if m.view.recentlyDown(mem.NodeID) {
				res.Err = errMemberRecentlyDown
			} else {
				callCtx, cancel := context.WithTimeout(ctx, sessionDeltaTimeout)
				res = fetchNodeSessionDelta(callCtx, m.memberCaller(mem), mem.NodeID, since[mem.NodeID], fields)
				cancel()
			}
			mu.Lock()
			out[mem.NodeID] = res
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

func (m *meshBridge) memberCaller(mem cluster.Member) memberCall {
	return func(ctx context.Context, path string, out any) error {
		return m.node.CallAPI(ctx, mem, http.MethodGet, path, out)
	}
}

// fetchNodeSessionDelta reads one member's changes since its position,
// every page. A member that cannot serve the position (restarted, window
// passed) or predates delta sync (no sync_cursor) yields its full list,
// flagged Reset.
func fetchNodeSessionDelta(ctx context.Context, call memberCall, nodeID string, since *backend.NodeSyncPos, fields proto.SessionOverviewFields) backend.NodeSessionDelta {
	query := url.Values{}
	query.Set(proto.SessionOverviewsParamLocal, localFlag)
	query.Set(proto.SessionOverviewsParamLimit, strconv.Itoa(backend.MaxSessionOverviewLimit))
	if fields != "" {
		query.Set(proto.SessionOverviewsParamFields, string(fields))
	}
	if since != nil {
		query.Set(proto.SessionOverviewsParamSince, backend.EncodeSyncCursor(map[string]backend.NodeSyncPos{nodeID: *since}))
	}
	var res backend.NodeSessionDelta
	first := true
	for range maxSessionDeltaPages {
		var page proto.DeviceSessionOverviewPage
		if err := call(ctx, sessionOverviewsPath+"?"+query.Encode(), &page); err != nil {
			return backend.NodeSessionDelta{Err: err}
		}
		if page.Reset {
			if !query.Has(proto.SessionOverviewsParamSince) {
				return backend.NodeSessionDelta{Err: fmt.Errorf("node %s reset a full list", nodeID)}
			}
			query.Del(proto.SessionOverviewsParamSince)
			query.Del(proto.SessionOverviewsParamCursor)
			res, first = backend.NodeSessionDelta{}, true
			continue
		}
		if first {
			first = false
			res.Position = memberPosition(page.SyncCursor, nodeID)
			res.Reset = !query.Has(proto.SessionOverviewsParamSince) || page.SyncCursor == ""
		}
		res.Sessions = append(res.Sessions, page.Sessions...)
		res.Deleted = append(res.Deleted, page.Deleted...)
		res.Unavailable = append(res.Unavailable, page.Unavailable...)
		if page.NextCursor == nil {
			return res
		}
		query.Set(proto.SessionOverviewsParamCursor, *page.NextCursor)
	}
	return backend.NodeSessionDelta{Err: errTooManyDeltaPages}
}

// memberPosition is nodeID's position in a member's sync cursor.
func memberPosition(cursor, nodeID string) *backend.NodeSyncPos {
	if cursor == "" {
		return nil
	}
	nodes, err := backend.DecodeSyncCursor(cursor)
	if err != nil {
		return nil
	}
	if pos, ok := nodes[nodeID]; ok {
		return &pos
	}
	return nil
}

// fetchSyncPosition reads a member's current change log position with a
// one-row page; nil for members that predate delta sync.
func fetchSyncPosition(ctx context.Context, call memberCall, nodeID string) *backend.NodeSyncPos {
	query := url.Values{}
	query.Set(proto.SessionOverviewsParamLocal, localFlag)
	query.Set(proto.SessionOverviewsParamLimit, "1")
	query.Set(proto.SessionOverviewsParamFields, string(proto.SessionOverviewFieldsRow))
	var page proto.DeviceSessionOverviewPage
	if err := call(ctx, sessionOverviewsPath+"?"+query.Encode(), &page); err != nil {
		return nil
	}
	return memberPosition(page.SyncCursor, nodeID)
}
