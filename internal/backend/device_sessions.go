package backend

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/historysearch"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

// Errors of the session-addressed device routes.
var (
	// ErrSessionNotOnNode is returned when a session is not owned by (or
	// not known to) this node; the client should re-route to its owner.
	ErrSessionNotOnNode = errors.New("session is not on this node")
	// ErrUnknownRoot is returned for a workspace root that is not in this
	// node's registry of known workspaces.
	ErrUnknownRoot = errors.New("workspace root is not known on this node")
	// ErrInvalidCursor is returned for a malformed or unknown paging
	// cursor.
	ErrInvalidCursor = errors.New("invalid cursor")
)

// Paging bounds of the session-addressed device routes.
const (
	DefaultSessionOverviewLimit = 50
	MaxSessionOverviewLimit     = 200
	DefaultSessionMessageLimit  = 200
	MaxSessionMessageLimit      = 500
)

// knownWorkspace is one workspace this node knows: attached (ws set) or
// registry-known only.
type knownWorkspace struct {
	Root     string
	DataDir  string
	LastUsed int64
	ws       *Workspace
}

func (k knownWorkspace) workspaceID() string {
	if k.ws != nil {
		return k.ws.ID
	}
	return ""
}

// knownWorkspaces lists attached workspaces (by root) and then registry
// entries that are not attached, skipping a second root over a database
// already listed. With hideTemp, temporary workspaces are left out like
// the overview does.
func (b *Backend) knownWorkspaces(hideTemp bool) ([]knownWorkspace, error) {
	lastUsed := map[string]int64{}
	var entries []knownWorkspace
	if b.registry != nil {
		list, err := b.registry.List()
		if err != nil {
			return nil, fmt.Errorf("failed to list workspace registry: %w", err)
		}
		for _, e := range list {
			lastUsed[e.Root] = e.LastUsed
			entries = append(entries, knownWorkspace{Root: e.Root, DataDir: e.DataDir, LastUsed: e.LastUsed})
		}
	}
	var out []knownWorkspace
	seen := map[string]bool{}
	seenData := map[string]bool{}
	for _, ws := range b.workspaces.Seq2() {
		if ws.App == nil || seen[ws.resolvedPath] {
			continue
		}
		k := knownWorkspace{Root: ws.resolvedPath, LastUsed: lastUsed[ws.resolvedPath], ws: ws}
		if ws.Cfg != nil {
			k.DataDir = ws.Cfg.Config().Options.DataDirectory
			seenData[filepath.Clean(k.DataDir)] = true
		}
		seen[k.Root] = true
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b knownWorkspace) int { return strings.Compare(a.Root, b.Root) })
	for _, e := range entries {
		dd := filepath.Clean(e.DataDir)
		if seen[e.Root] || seenData[dd] {
			continue
		}
		seen[e.Root], seenData[dd] = true, true
		if hideTemp && isTempWorkspace(e.Root, e.DataDir) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// knownRoot returns the known workspace rooted at root, or
// ErrUnknownRoot. Only roots already in the registry (or attached) are
// accepted, never arbitrary paths.
func (b *Backend) knownRoot(root string) (knownWorkspace, error) {
	if root == "" {
		return knownWorkspace{}, ErrUnknownRoot
	}
	known, err := b.knownWorkspaces(false)
	if err != nil {
		return knownWorkspace{}, err
	}
	for _, k := range known {
		if k.Root == root {
			return k, nil
		}
	}
	// A registry root that is an alias (unresolved symlink, shared data
	// dir) of an attached workspace resolves to that workspace.
	if b.registry != nil {
		entries, err := b.registry.List()
		if err != nil {
			return knownWorkspace{}, fmt.Errorf("failed to list workspace registry: %w", err)
		}
		for _, e := range entries {
			if e.Root != root {
				continue
			}
			for _, k := range known {
				if k.ws != nil && filepath.Clean(k.DataDir) == filepath.Clean(e.DataDir) {
					return k, nil
				}
			}
		}
	}
	return knownWorkspace{}, ErrUnknownRoot
}

// sessionSource is a located session with services to read it: live ones
// for an attached workspace, read-only ones over the detached database.
type sessionSource struct {
	knownWorkspace
	sess     session.Session
	messages message.Service
}

// withSession locates sessionID among this node's known workspaces (only
// the one rooted at root when root is set) and runs fn on it. Detached
// workspaces are read READ-ONLY, without attaching. A session that is not
// here, or is a replica owned by another node, is ErrSessionNotOnNode.
func (b *Backend) withSession(ctx context.Context, sessionID, root string, fn func(src sessionSource) error) error {
	if sessionID == "" {
		return ErrSessionNotOnNode
	}
	var known []knownWorkspace
	if root != "" {
		k, err := b.knownRoot(root)
		if err != nil {
			return err
		}
		known = []knownWorkspace{k}
	} else {
		var err error
		if known, err = b.knownWorkspaces(false); err != nil {
			return err
		}
		// Detached databases whose peeked listing has the session are
		// tried first, so a lookup does not open every database.
		var likely, rest []knownWorkspace
		for _, k := range known {
			if b.likelyHas(ctx, k, sessionID) {
				likely = append(likely, k)
			} else {
				rest = append(rest, k)
			}
		}
		known = append(likely, rest...)
	}
	for _, k := range known {
		found, err := b.trySession(ctx, k, sessionID, fn)
		if err != nil || found {
			return err
		}
	}
	return ErrSessionNotOnNode
}

// likelyHas reports whether k is attached or its peeked sessions list
// sessionID.
func (b *Backend) likelyHas(ctx context.Context, k knownWorkspace, sessionID string) bool {
	if k.ws != nil {
		return true
	}
	peeked, err := b.peekCache.get(ctx, k.DataDir)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(peeked, func(p db.PeekedSession) bool { return p.ID == sessionID })
}

func (b *Backend) trySession(ctx context.Context, k knownWorkspace, sessionID string, fn func(src sessionSource) error) (bool, error) {
	src := sessionSource{knownWorkspace: k}
	var sessions session.Service
	if k.ws != nil {
		if err := k.ws.Messages.FlushAll(ctx); err != nil {
			slog.Debug("Failed to flush messages before device read", "root", k.Root, "error", err)
		}
		sessions, src.messages = k.ws.Sessions, k.ws.Messages
	} else {
		conn, err := db.OpenReadOnly(k.DataDir)
		if err != nil {
			slog.Debug("Failed to open detached workspace", "root", k.Root, "error", err)
			return false, nil
		}
		if conn == nil {
			return false, nil
		}
		defer conn.Close()
		queries := db.New(conn)
		sessions, src.messages = session.NewService(queries, conn), message.NewService(queries)
	}
	sess, err := sessions.Get(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !b.ownedHere(sess.OwnerNode) {
		return false, ErrSessionNotOnNode
	}
	src.sess = sess
	return true, fn(src)
}

func (b *Backend) sessionBusy(k knownWorkspace, sessionID string) bool {
	return k.ws != nil && k.ws.AgentCoordinator != nil && k.ws.AgentCoordinator.IsSessionBusy(sessionID)
}

// DeviceSession returns a session's metadata by session ID (and optional
// root), from an attached or detached workspace.
func (b *Backend) DeviceSession(ctx context.Context, sessionID, root string) (proto.DeviceSession, error) {
	var out proto.DeviceSession
	err := b.withSession(ctx, sessionID, root, func(src sessionSource) error {
		msgs, err := src.messages.List(ctx, sessionID)
		if err != nil {
			return err
		}
		out = proto.DeviceSession{
			ID:           src.sess.ID,
			Title:        src.sess.Title,
			UpdatedAt:    src.sess.UpdatedAt,
			IsBusy:       b.sessionBusy(src.knownWorkspace, sessionID),
			NodeID:       b.selfNodeID(),
			Root:         src.Root,
			DataDir:      src.DataDir,
			WorkspaceID:  src.workspaceID(),
			Attached:     src.ws != nil,
			MessageCount: len(msgs),
		}
		return nil
	})
	return out, err
}

// DeviceSessionMessages returns up to limit messages older than the
// message before (newest when empty), in chronological order, and the
// before value for the next older page ("" at the first message).
func (b *Backend) DeviceSessionMessages(ctx context.Context, sessionID, root string, limit int, before string) (msgs []message.Message, nextBefore, workspaceID string, err error) {
	limit = clampLimit(limit, DefaultSessionMessageLimit, MaxSessionMessageLimit)
	err = b.withSession(ctx, sessionID, root, func(src sessionSource) error {
		all, err := src.messages.List(ctx, sessionID)
		if err != nil {
			return err
		}
		end := len(all)
		if before != "" {
			end = slices.IndexFunc(all, func(m message.Message) bool { return m.ID == before })
			if end < 0 {
				return ErrInvalidCursor
			}
		}
		start := max(0, end-limit)
		msgs = all[start:end]
		if start > 0 {
			nextBefore = all[start].ID
		}
		workspaceID = src.workspaceID()
		return nil
	})
	return msgs, nextBefore, workspaceID, err
}

func clampLimit(limit, def, maxLimit int) int {
	switch {
	case limit <= 0:
		return def
	case limit > maxLimit:
		return maxLimit
	default:
		return limit
	}
}

// openRoot attaches the registry-known workspace rooted at root on this
// node, or returns the already attached one, the same way a client
// re-creates a workspace whose ID went stale.
func (b *Backend) openRoot(root string) (*Workspace, error) {
	k, err := b.knownRoot(root)
	if err != nil {
		return nil, err
	}
	if k.ws != nil {
		return k.ws, nil
	}
	ws, _, err := b.CreateWorkspace(proto.Workspace{Path: k.Root, DataDir: k.DataDir, ClientID: uuid.NewString()})
	if err != nil {
		return nil, err
	}
	return ws, nil
}

// OpenDeviceSession attaches the workspace rooted at root if needed and
// returns its runtime ID for the session, so a device can stream events
// and prompt. It is idempotent.
func (b *Backend) OpenDeviceSession(ctx context.Context, sessionID, root string) (proto.DeviceSessionOpen, error) {
	if sessionID == "" {
		return proto.DeviceSessionOpen{}, ErrSessionNotOnNode
	}
	ws, err := b.openRoot(root)
	if err != nil {
		return proto.DeviceSessionOpen{}, err
	}
	sess, err := ws.Sessions.Get(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.DeviceSessionOpen{}, ErrSessionNotOnNode
	}
	if err != nil {
		return proto.DeviceSessionOpen{}, err
	}
	if !b.ownedHere(sess.OwnerNode) {
		return proto.DeviceSessionOpen{}, ErrSessionNotOnNode
	}
	return proto.DeviceSessionOpen{WorkspaceID: ws.ID, SessionID: sessionID, Root: ws.resolvedPath}, nil
}

// CreateDeviceSession attaches the workspace rooted at root if needed,
// creates a session, and sends its first prompt.
func (b *Backend) CreateDeviceSession(ctx context.Context, params proto.DeviceCreateSessionParams) (proto.DeviceCreatedSession, error) {
	if strings.TrimSpace(params.Prompt) == "" {
		return proto.DeviceCreatedSession{}, agent.ErrEmptyPrompt
	}
	ws, err := b.openRoot(params.Root)
	if err != nil {
		return proto.DeviceCreatedSession{}, err
	}
	if ws.AgentCoordinator == nil {
		return proto.DeviceCreatedSession{}, ErrAgentNotInitialized
	}
	sess, err := b.CreateSessionWithModel(ctx, ws.ID, "", params.Model)
	if err != nil {
		return proto.DeviceCreatedSession{}, err
	}
	if err := b.SendMessage(ws.ID, proto.AgentMessage{SessionID: sess.ID, ClientID: params.ClientID, Prompt: params.Prompt}); err != nil {
		return proto.DeviceCreatedSession{}, err
	}
	return proto.DeviceCreatedSession{SessionID: sess.ID, WorkspaceID: ws.ID, Root: ws.resolvedPath, NodeID: b.selfNodeID()}, nil
}

// WorkspaceRoots lists this node's known workspace roots, most recently
// used first, for a new-session picker.
func (b *Backend) WorkspaceRoots(ctx context.Context) (proto.WorkspaceRoots, error) {
	known, err := b.knownWorkspaces(b.hideTempWorkspaces())
	if err != nil {
		return proto.WorkspaceRoots{}, err
	}
	roots := make([]proto.WorkspaceRoot, 0, len(known))
	for _, k := range known {
		r := proto.WorkspaceRoot{Root: k.Root, DataDir: k.DataDir, Attached: k.ws != nil, WorkspaceID: k.workspaceID(), LastUsedAt: k.LastUsed}
		if k.ws != nil {
			r.SessionCount = len(b.attachedSessionOverviews(ctx, k.ws))
		} else if peeked, err := b.peekCache.get(ctx, k.DataDir); err == nil {
			r.SessionCount = len(peeked)
		}
		roots = append(roots, r)
	}
	slices.SortStableFunc(roots, func(x, y proto.WorkspaceRoot) int {
		return cmp.Or(cmp.Compare(y.LastUsedAt, x.LastUsedAt), strings.Compare(x.Root, y.Root))
	})
	return proto.WorkspaceRoots{Roots: roots}, nil
}

// SearchNodeHistory searches every attached and registry-known workspace
// on this node, keeping the best hit per session.
func (b *Backend) SearchNodeHistory(ctx context.Context, params proto.NodeSearchHistoryParams) (proto.NodeSearchHistoryResult, error) {
	sessionLimit, candidateLimit := historysearch.ResolveLimits(params.Limit)
	var embParams embedding.Params
	if b.cfg != nil {
		embParams = b.cfg.EmbeddingParams()
	}
	sources, ids, closeAll := b.historySources(embParams)
	defer closeAll()
	res, err := historysearch.SearchAcross(ctx, sources, params.Query,
		historysearch.Scope(params.Scope), nil, candidateLimit, sessionLimit)
	if err != nil {
		return proto.NodeSearchHistoryResult{}, err
	}
	pr := app.HistoryResultToProto(res, func(root string) string { return ids[root] })
	out := proto.NodeSearchHistoryResult{
		Hits:           []proto.NodeSessionHit{},
		Total:          pr.Total,
		SemanticUsed:   pr.SemanticUsed,
		Mode:           pr.Mode,
		FallbackReason: pr.FallbackReason,
	}
	best := map[string]int{}
	node := b.selfNodeID()
	for _, h := range pr.Hits {
		hit := proto.NodeSessionHit{SessionHit: h, NodeID: node, Root: h.WorkspaceRoot}
		if i, ok := best[h.SessionID]; ok {
			if h.Score > out.Hits[i].Score {
				out.Hits[i] = hit
			}
			continue
		}
		best[h.SessionID] = len(out.Hits)
		out.Hits = append(out.Hits, hit)
	}
	slices.SortStableFunc(out.Hits, func(x, y proto.NodeSessionHit) int { return cmp.Compare(y.Score, x.Score) })
	return out, nil
}

// ListSessionOverviewPage flattens the session overviews (cluster-wide,
// or this node's with local) into one list sorted by updated_at desc then
// id, and returns the page after cursor.
func (b *Backend) ListSessionOverviewPage(ctx context.Context, local bool, limit int, cursor string) (proto.DeviceSessionOverviewPage, error) {
	return b.ListSessionOverviews(ctx, SessionOverviewQuery{Local: local, Limit: limit, Cursor: cursor})
}

// SessionOverviewQuery are the GET /v1/session-overviews parameters.
type SessionOverviewQuery struct {
	// Local restricts the list to this node's own sessions.
	Local bool
	Limit int
	// Cursor is the keyset paging cursor (next_cursor).
	Cursor string
	// Since, when set, asks for the changes after this sync cursor.
	Since  string
	Fields proto.SessionOverviewFields
}

// SessionDeltaSource is implemented by cluster bridges that can ask
// other nodes for their session overview changes.
type SessionDeltaSource interface {
	// SessionOverviewDeltas asks every other member for its changes
	// since its position in since (nil or absent: its full list) and
	// returns one result per current member.
	SessionOverviewDeltas(ctx context.Context, since map[string]*NodeSyncPos, fields proto.SessionOverviewFields) map[string]NodeSessionDelta
}

// NodeSessionDelta is one member's answer to a delta request.
type NodeSessionDelta struct {
	Sessions    []proto.DeviceSessionOverview
	Deleted     []proto.SessionTombstone
	Unavailable []proto.NodeUnavailable
	// Position is the member's new position; nil for members that
	// predate delta sync.
	Position *NodeSyncPos
	// Reset says Sessions is the member's full list.
	Reset bool
	// Err is set when the member could not be asked.
	Err error
}

// ListSessionOverviews is [Backend.ListSessionOverviewPage] with delta
// sync (since) and slim rows (fields=row).
//
// A pass (the pages of one full list or one delta) is computed once and
// later pages are served from that snapshot, so every page agrees on the
// sync cursor and on which nodes are reset or stale. Once a snapshot
// expires, a full list falls back to recomputing from the keyset position
// and a delta answers reset.
func (b *Backend) ListSessionOverviews(ctx context.Context, q SessionOverviewQuery) (proto.DeviceSessionOverviewPage, error) {
	limit := clampLimit(q.Limit, DefaultSessionOverviewLimit, MaxSessionOverviewLimit)
	var after *sessionCursor
	if q.Cursor != "" {
		c, err := decodeSessionCursor(q.Cursor)
		if err != nil {
			return proto.DeviceSessionOverviewPage{}, err
		}
		after = &c
	}
	key := q.passKey()
	if after != nil && after.pass != "" {
		if pass := b.sessionPasses.get(after.pass, key); pass != nil {
			return pass.page(limit, after, q.Fields), nil
		}
		if q.Since != "" {
			return resetSessionPage(), nil
		}
	}
	var pass *sessionPass
	if q.Since != "" {
		var reset bool
		var err error
		pass, reset, err = b.sessionOverviewDelta(ctx, q)
		if err != nil || reset {
			return resetSessionPage(), err
		}
	} else {
		var err error
		pass, err = b.sessionOverviewFull(ctx, q)
		if err != nil {
			return proto.DeviceSessionOverviewPage{}, err
		}
	}
	if q.Fields == proto.SessionOverviewFieldsCursor {
		// Position only: no rows, and no pass kept for later pages.
		return proto.DeviceSessionOverviewPage{SyncCursor: pass.meta.SyncCursor}, nil
	}
	page := pass.page(limit, after, q.Fields)
	if page.NextCursor != nil {
		pass.id = b.sessionPasses.put(pass, key)
		page = pass.page(limit, after, q.Fields)
	}
	return page, nil
}

// passKey identifies the queries a pass can serve later pages of.
func (q SessionOverviewQuery) passKey() string {
	return fmt.Sprintf("%t|%s|%s", q.Local, q.Since, q.Fields)
}

// sessionOverviewFull computes a full list pass.
func (b *Backend) sessionOverviewFull(ctx context.Context, q SessionOverviewQuery) (*sessionPass, error) {
	b.sessionListMu.Lock()
	all, err := b.listAllOverviews(ctx)
	if err != nil {
		b.sessionListMu.Unlock()
		return nil, err
	}
	owned, replicas := b.splitByOwner(all)
	localRows, unavailable := b.flattenOverviews(owned)
	selfID := b.selfNodeID()
	selfPos, _, _ := b.sessionChanges().observeSince(localRows, unavailableRoots(unavailable), nil)
	b.sessionListMu.Unlock()
	positions := map[string]NodeSyncPos{selfID: selfPos}
	rows := localRows
	if cb := b.clusterBridge(); cb != nil && !q.Local {
		remote := cb.RemoteOverviews(ctx)
		for id, pos := range remote.SyncPositions {
			positions[id] = pos
		}
		rows, unavailable = b.flattenOverviews(mergeReplicas(owned, replicas, remote, b.nodeName))
	}
	return &sessionPass{rows: rows, meta: proto.DeviceSessionOverviewPage{
		Unavailable: unavailable,
		SyncCursor:  EncodeSyncCursor(positions),
	}}, nil
}

// sessionOverviewDelta computes a delta pass: this node's changes plus,
// cluster-wide, every reachable member's. reset reports a since cursor
// that is unusable as a whole.
func (b *Backend) sessionOverviewDelta(ctx context.Context, q SessionOverviewQuery) (pass *sessionPass, reset bool, err error) {
	since, err := DecodeSyncCursor(q.Since)
	if err != nil {
		return nil, true, nil
	}
	selfID := b.selfNodeID()
	var source SessionDeltaSource
	if cb := b.clusterBridge(); cb != nil && !q.Local {
		source, _ = cb.(SessionDeltaSource)
	}
	var selfSince *NodeSyncPos
	if pos, ok := since[selfID]; ok {
		selfSince = &pos
	}
	b.sessionListMu.Lock()
	all, err := b.listAllOverviews(ctx)
	if err != nil {
		b.sessionListMu.Unlock()
		return nil, false, err
	}
	owned, _ := b.splitByOwner(all)
	localRows, unavailable := b.flattenOverviews(owned)
	selfPos, local, ok := b.sessionChanges().observeSince(localRows, unavailableRoots(unavailable), selfSince)
	b.sessionListMu.Unlock()
	if (!ok || selfSince == nil) && source == nil {
		return nil, true, nil
	}
	positions := map[string]NodeSyncPos{selfID: selfPos}
	var rows []proto.DeviceSessionOverview
	deleted := []proto.SessionTombstone{}
	resetNodes, staleNodes := []string{}, []string{}
	if ok && selfSince != nil {
		rows, deleted = append(rows, local.sessions...), append(deleted, local.deleted...)
	} else {
		rows = append(rows, localRows...)
		resetNodes = append(resetNodes, selfID)
	}
	if source != nil {
		want := map[string]*NodeSyncPos{}
		for id, pos := range since {
			if id != selfID {
				want[id] = &pos
			}
		}
		results := source.SessionOverviewDeltas(ctx, want, q.Fields)
		for _, id := range slices.Sorted(maps.Keys(since)) {
			if _, member := results[id]; !member && id != selfID {
				// The node left the cluster: clients drop its rows.
				resetNodes = append(resetNodes, id)
			}
		}
		for _, id := range slices.Sorted(maps.Keys(results)) {
			res := results[id]
			if res.Err != nil {
				staleNodes = append(staleNodes, id)
				if pos, ok := since[id]; ok {
					positions[id] = pos
				}
				continue
			}
			rows = append(rows, res.Sessions...)
			deleted = append(deleted, res.Deleted...)
			unavailable = append(unavailable, res.Unavailable...)
			if res.Reset {
				resetNodes = append(resetNodes, id)
			}
			if res.Position != nil {
				positions[id] = *res.Position
			}
		}
	}
	sortSessionRows(rows)
	slices.SortFunc(deleted, func(x, y proto.SessionTombstone) int {
		return cmp.Or(strings.Compare(x.NodeID, y.NodeID), strings.Compare(x.ID, y.ID))
	})
	return &sessionPass{rows: rows, meta: proto.DeviceSessionOverviewPage{
		Unavailable: unavailable,
		Deleted:     deleted,
		ResetNodes:  resetNodes,
		StaleNodes:  staleNodes,
		SyncCursor:  EncodeSyncCursor(positions),
	}}, false, nil
}

// resetSessionPage tells the client its since cursor is unusable.
func resetSessionPage() proto.DeviceSessionOverviewPage {
	return proto.DeviceSessionOverviewPage{
		Sessions:    []proto.DeviceSessionOverview{},
		Deleted:     []proto.SessionTombstone{},
		Unavailable: []proto.NodeUnavailable{},
		Reset:       true,
	}
}

// page returns the page of the pass's rows after the keyset position.
func (p *sessionPass) page(limit int, after *sessionCursor, fields proto.SessionOverviewFields) proto.DeviceSessionOverviewPage {
	page := p.meta
	page.Sessions, page.Total = []proto.DeviceSessionOverview{}, len(p.rows)
	start := 0
	if after != nil {
		start = len(p.rows)
		for i, s := range p.rows {
			if s.UpdatedAt < after.updatedAt || (s.UpdatedAt == after.updatedAt && s.ID > after.id) {
				start = i
				break
			}
		}
	}
	end := min(len(p.rows), start+limit)
	page.Sessions = append(page.Sessions, p.rows[start:end]...)
	if fields == proto.SessionOverviewFieldsRow {
		slimRows(page.Sessions)
	}
	if end < len(p.rows) {
		last := p.rows[end-1]
		next := sessionCursor{updatedAt: last.UpdatedAt, id: last.ID, pass: p.id}.encode()
		page.NextCursor, page.HasMore = &next, true
	}
	return page
}

// sortSessionRows orders rows by updated_at desc, then id.
func sortSessionRows(rows []proto.DeviceSessionOverview) {
	slices.SortFunc(rows, func(x, y proto.DeviceSessionOverview) int {
		return cmp.Or(cmp.Compare(y.UpdatedAt, x.UpdatedAt), strings.Compare(x.ID, y.ID))
	})
}

func (b *Backend) flattenOverviews(overviews []proto.WorkspaceOverview) ([]proto.DeviceSessionOverview, []proto.NodeUnavailable) {
	selfID, selfName := b.selfNodeID(), ""
	if cb := b.clusterBridge(); cb != nil {
		selfName = cb.NodeName()
	}
	unavailable := []proto.NodeUnavailable{}
	byID := map[string]int{}
	var out []proto.DeviceSessionOverview
	for _, ov := range overviews {
		nodeID, nodeName := cmp.Or(ov.NodeID, selfID), cmp.Or(ov.NodeName, selfName)
		if ov.Unavailable {
			unavailable = append(unavailable, proto.NodeUnavailable{NodeID: nodeID, NodeName: nodeName, Root: ov.Root, Error: ov.Error})
		}
		for _, s := range ov.Sessions {
			d := proto.DeviceSessionOverview{
				ID: s.ID, Title: s.Title, UpdatedAt: s.UpdatedAt, IsBusy: s.IsBusy, Unread: s.Unread,
				Color: s.Color, Animal: s.Animal, Favorite: s.Favorite,
				NodeID: nodeID, NodeName: nodeName, Root: ov.Root, DataDir: ov.DataDir,
				WorkspaceID: ov.WorkspaceID, Attached: ov.Attached,
			}
			if s.ReadOnly && s.OwnerNode != "" {
				d.NodeID, d.NodeName = s.OwnerNode, cmp.Or(s.OwnerName, s.OwnerNode)
				d.WorkspaceID, d.Attached = "", false
			}
			if !d.Attached {
				d.WorkspaceID = ""
			}
			if i, ok := byID[s.ID]; ok {
				if !s.ReadOnly {
					out[i] = d
				}
				continue
			}
			byID[s.ID] = len(out)
			out = append(out, d)
		}
	}
	sortSessionRows(out)
	return out, unavailable
}

// sessionCursor is a keyset position in the session overview order,
// optionally within a pass snapshot.
type sessionCursor struct {
	updatedAt int64
	id        string
	pass      string
}

// passCursorPrefix starts the encoding of a cursor within a pass; keyset
// only cursors start with a digit or a minus sign.
const passCursorPrefix = "p"

func (c sessionCursor) encode() string {
	raw := strconv.FormatInt(c.updatedAt, 10) + ":" + c.id
	if c.pass != "" {
		raw = passCursorPrefix + c.pass + "." + raw
	}
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeSessionCursor(s string) (sessionCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return sessionCursor{}, ErrInvalidCursor
	}
	var pass string
	if rest, ok := strings.CutPrefix(string(raw), passCursorPrefix); ok {
		var found bool
		if pass, rest, found = strings.Cut(rest, "."); !found || pass == "" {
			return sessionCursor{}, ErrInvalidCursor
		}
		raw = []byte(rest)
	}
	ts, id, ok := strings.Cut(string(raw), ":")
	if !ok || id == "" {
		return sessionCursor{}, ErrInvalidCursor
	}
	updatedAt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return sessionCursor{}, ErrInvalidCursor
	}
	return sessionCursor{updatedAt: updatedAt, id: id, pass: pass}, nil
}
