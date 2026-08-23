package backend

import (
	"context"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/sessionimport"
)

// withWorkspaceSession resolves a workspace to a session-write path,
// whether it is ATTACHED (running in this server) or merely DETACHED
// (registry-known but not open), and runs fn against it.
//
// When workspaceID names an attached workspace, fn receives that
// workspace and its live session service (the normal path). Otherwise the
// workspace is looked up in the registry by root: its database is opened
// READ-WRITE without migrations or the data-dir lock (see
// [db.OpenWritable]), fn receives a fresh session.Service over that
// connection and a nil *Workspace, and the connection is closed before
// returning. This mirrors the read-only fan-out used by cross-workspace
// search and overviews, but for the archive/mark-read writes.
//
// Failure to open a detached workspace's database (missing file, held
// lock, corruption) is returned as an error for THIS workspace only, so a
// caller processing several sessions can record a per-session failure and
// continue with the rest — it never panics and never migrates.
func (b *Backend) withWorkspaceSession(workspaceID, root string, fn func(ws *Workspace, s session.Service) error) error {
	if ws, err := b.GetWorkspace(workspaceID); err == nil && ws.App != nil {
		return fn(ws, ws.App.Sessions)
	}

	// The caller may route by root with an empty (or placeholder) id even
	// though the workspace is actually ATTACHED in this process — the id is
	// only unknown to the caller because it read a stale detached overview,
	// or the workspace was attached between snapshot and action. Resolve
	// root against the attached set FIRST so we use the live session
	// service and never open a second writable handle to a database this
	// process already has open (which would bypass the data-dir lock and
	// risk WAL/header desync, and would leave the live in-memory cache
	// stale).
	//
	// We scan b.workspaces by resolvedPath rather than consulting
	// b.pathIndex because ISOLATED workspaces (e.g. `crush run`) are held in
	// b.workspaces but deliberately absent from pathIndex and the registry;
	// a pathIndex-only guard would miss them and route their live,
	// lock-held database through OpenWritable.
	//
	// NORMALIZATION INVARIANT: this match is exact. It relies on the client
	// sending the same canonical root the registry stores and overviews
	// report — CreateWorkspace writes registry.Add(Root: key) where key ==
	// resolvedPath, so the three agree today. If registry Root and
	// resolvedPath ever diverge in normalization (trailing slash, symlink
	// resolution), an attached workspace routed by its registry root would
	// miss here and be double-opened. Keep them normalized identically.
	//
	// TEARDOWN/TOCTOU: a workspace being torn down is removed from
	// b.workspaces BEFORE its database handle is closed, so a same-root
	// lookup here can miss it while its live connection is still open. The
	// authoritative guard against a second writable handle therefore lives
	// in [db.OpenWritable], which refuses (ErrDataDirBusy) while the data
	// dir is still in this process's shared connection pool. The in-memory
	// checks here are a fast path; the App==nil branch additionally fails
	// closed for a workspace that resolves but is unusable. Cross-PROCESS
	// attaches (another crush holding the DB) rely on busy_timeout; the
	// UI's busy-skip only reflects same-process live state.
	if root != "" {
		if ws, exists := b.attachedByRoot(root); exists {
			if ws.App != nil {
				return fn(ws, ws.App.Sessions)
			}
			// Present but unusable (tearing down): fail closed rather than
			// racing a second writable open against its still-open DB.
			return ErrWorkspaceNotFound
		}
	}

	// Detached: resolve the workspace's data directory from the registry
	// by root and open its database read-write for a one-shot write.
	dataDir, found, err := b.registryDataDirForRoot(root)
	if err != nil {
		return err
	}
	if !found {
		return ErrWorkspaceNotFound
	}

	conn, err := db.OpenWritable(dataDir)
	if err != nil {
		return err
	}
	if conn == nil {
		return ErrWorkspaceNotFound // no database file yet
	}
	defer conn.Close()

	svc := session.NewService(db.New(conn), conn)
	return fn(nil, svc)
}

// registryDataDirForRoot resolves a workspace root to its on-disk data
// directory via the registry, for one-shot detached opens (read-only
// preview, writable archive/mark-read). found is false with a nil error
// when the registry is unset or simply has no entry for root; a non-nil
// error means the registry listing itself failed and the caller should
// surface it rather than treat root as unknown.
func (b *Backend) registryDataDirForRoot(root string) (dataDir string, found bool, err error) {
	if root == "" || b.registry == nil {
		return "", false, nil
	}
	entries, err := b.registry.List()
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		if e.Root == root {
			return e.DataDir, true, nil
		}
	}
	return "", false, nil
}

// attachedByRoot returns the in-process workspace whose resolved path
// equals root and whether any such workspace exists. It scans the full
// attached set (including isolated workspaces, which are absent from
// pathIndex) so a session write is never routed to OpenWritable against a
// database already open in this process. The returned *Workspace may have a
// nil App if it is mid-teardown; callers must check before dereferencing.
func (b *Backend) attachedByRoot(root string) (*Workspace, bool) {
	for _, ws := range b.workspaces.Seq2() {
		if ws.resolvedPath == root {
			return ws, true
		}
	}
	return nil, false
}

// CreateSession creates a new session in the given workspace.
func (b *Backend) CreateSession(ctx context.Context, workspaceID, title string) (session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return session.Session{}, err
	}

	return ws.Sessions.Create(ctx, title)
}

// GetSession retrieves a session by workspace and session ID.
func (b *Backend) GetSession(ctx context.Context, workspaceID, sessionID string) (session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return session.Session{}, err
	}

	return ws.Sessions.Get(ctx, sessionID)
}

// ListSessions returns all sessions in the given workspace.
func (b *Backend) ListSessions(ctx context.Context, workspaceID string) ([]session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Sessions.List(ctx)
}

func (b *Backend) ListSessionImportSources() ([]sessionimport.SourceInfo, error) {
	return sessionimport.Sources()
}

func (b *Backend) DiscoverSessionImports(ctx context.Context, source sessionimport.Source) ([]sessionimport.Candidate, error) {
	return sessionimport.Discover(ctx, source)
}

func (b *Backend) ImportSessions(ctx context.Context, workspaceID string, paths []string, from sessionimport.Source) ([]sessionimport.Result, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.App == nil || ws.App.SessionImporter == nil {
		return nil, ErrWorkspaceNotFound
	}
	results := make([]sessionimport.Result, 0, len(paths))
	source := from
	if source == "" {
		source = sessionimport.SourceAuto
	}
	for _, path := range paths {
		imported, parseErr := sessionimport.Parse(path, source)
		if parseErr != nil {
			return nil, parseErr
		}
		result, importErr := ws.App.SessionImporter(ctx, imported)
		if importErr != nil {
			return nil, importErr
		}
		results = append(results, result)
	}
	return results, nil
}

// GetAgentSession returns session metadata with the agent's busy
// status.
func (b *Backend) GetAgentSession(ctx context.Context, workspaceID, sessionID string) (proto.AgentSession, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.AgentSession{}, err
	}

	se, err := ws.Sessions.Get(ctx, sessionID)
	if err != nil {
		return proto.AgentSession{}, err
	}

	var isSessionBusy bool
	if ws.AgentCoordinator != nil {
		isSessionBusy = ws.AgentCoordinator.IsSessionBusy(sessionID)
	}

	return proto.AgentSession{
		ID:     se.ID,
		Title:  se.Title,
		IsBusy: isSessionBusy,
	}, nil
}

// ListSessionMessages returns all messages for a session.
func (b *Backend) ListSessionMessages(ctx context.Context, workspaceID, sessionID string) ([]message.Message, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	// Drain debounced updates so HTTP clients (and the TUI on session
	// switch) observe the latest in-memory state rather than racing the
	// debounce timer in message.Service.
	if err := ws.Messages.FlushAll(ctx); err != nil {
		return nil, err
	}
	return ws.Messages.List(ctx, sessionID)
}

// ListSessionHistory returns the history items for a session.
func (b *Backend) ListSessionHistory(ctx context.Context, workspaceID, sessionID string) (any, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.History.ListBySession(ctx, sessionID)
}

// SaveSession updates a session in the given workspace.
func (b *Backend) SaveSession(ctx context.Context, workspaceID string, sess session.Session) (session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return session.Session{}, err
	}

	return ws.Sessions.Save(ctx, sess)
}

// DeleteSession deletes a session from the given workspace.
func (b *Backend) DeleteSession(ctx context.Context, workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	return ws.Sessions.Delete(ctx, sessionID)
}

// ArchiveSession archives a session and releases its snapshot refs. The
// session may live in an attached workspace or a detached one (resolved by
// root via the registry); see [Backend.withWorkspaceSession]. Snapshot ref
// cleanup only runs for attached workspaces (a detached workspace's
// checkpoint service is not loaded); the archive write itself lands in
// either case.
//
// KNOWN LIMITATION: archiving a session in a truly-detached workspace does
// NOT prune its snapshot refs, so git objects for that session stay
// reachable and are not GC'd until the workspace is next attached and the
// session re-processed. The archive flag itself is correct; only the
// snapshot cleanup is deferred. Fixing it would require standing up a
// checkpoint service against the detached project dir, which this one-shot
// path deliberately avoids.
func (b *Backend) ArchiveSession(ctx context.Context, workspaceID, root, sessionID string) error {
	return b.withWorkspaceSession(workspaceID, root, func(ws *Workspace, s session.Service) error {
		if ws != nil {
			return ws.ArchiveSession(ctx, sessionID)
		}
		return s.Archive(ctx, sessionID)
	})
}

// MarkSessionSeen marks an arbitrary session as read, clearing its derived
// unread state (LastFinishedAt > LastSeenAt). The session may live in an
// attached or a detached workspace (resolved by root via the registry).
func (b *Backend) MarkSessionSeen(ctx context.Context, workspaceID, root, sessionID string) error {
	return b.withWorkspaceSession(workspaceID, root, func(_ *Workspace, s session.Service) error {
		return s.MarkSeen(ctx, sessionID)
	})
}

// SetSessionFavorite pins or unpins a session so the sidebar inbox sticks
// it to the top. The session may live in an attached or a detached
// workspace (resolved by root via the registry).
func (b *Backend) SetSessionFavorite(ctx context.Context, workspaceID, root, sessionID string, favorite bool) error {
	return b.withWorkspaceSession(workspaceID, root, func(_ *Workspace, s session.Service) error {
		return s.SetFavorite(ctx, sessionID, favorite)
	})
}

// UnarchiveSession unarchives a session. The session may live in an
// attached or a detached workspace (resolved by root via the registry).
func (b *Backend) UnarchiveSession(ctx context.Context, workspaceID, root, sessionID string) error {
	return b.withWorkspaceSession(workspaceID, root, func(_ *Workspace, s session.Service) error {
		return s.Unarchive(ctx, sessionID)
	})
}

// ListArchivedSessions returns archived sessions for a workspace.
func (b *Backend) ListArchivedSessions(ctx context.Context, workspaceID string) ([]session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Sessions.ListArchived(ctx)
}

// ListUserMessages returns user-role messages for a session.
func (b *Backend) ListUserMessages(ctx context.Context, workspaceID, sessionID string) ([]message.Message, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Messages.ListUserMessages(ctx, sessionID)
}

// ListAllUserMessages returns all user-role messages across sessions.
func (b *Backend) ListAllUserMessages(ctx context.Context, workspaceID string) ([]message.Message, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Messages.ListAllUserMessages(ctx)
}

// BackfillEmbeddings embeds past messages lacking a vector under the
// active embedding model. Returns the count embedded.
func (b *Backend) BackfillEmbeddings(ctx context.Context, workspaceID string) (int, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return 0, err
	}
	return ws.BackfillEmbeddings(ctx)
}

// PendingEmbeddingCount reports how many past messages BackfillEmbeddings
// would embed.
func (b *Backend) PendingEmbeddingCount(ctx context.Context, workspaceID string) (int, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return 0, err
	}
	return ws.PendingEmbeddingCount(ctx)
}

// EmbeddingStatus reports the embedding index state for a workspace.
func (b *Backend) EmbeddingStatus(ctx context.Context, workspaceID string) (proto.EmbeddingStatus, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.EmbeddingStatus{}, err
	}
	return ws.EmbeddingStatus(ctx)
}

// SearchHistory runs hybrid search over a workspace's conversation
// history and returns per-session hits tagged with the workspace's id and
// root. By default only the requested workspace is searched (the fast
// path). When params.AllWorkspaces is set it fans out over every known
// workspace and merges the results (see searchAllWorkspaces).
func (b *Backend) SearchHistory(ctx context.Context, workspaceID string, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error) {
	if params.AllWorkspaces {
		return b.searchAllWorkspaces(ctx, workspaceID, params)
	}
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.SearchHistoryResult{}, err
	}
	res, err := ws.SearchHistory(ctx, params)
	if err != nil {
		return proto.SearchHistoryResult{}, err
	}
	for i := range res.Hits {
		res.Hits[i].WorkspaceID = ws.ID
		res.Hits[i].WorkspaceRoot = ws.Path
	}
	return res, nil
}
