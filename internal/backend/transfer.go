package backend

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/registry"
	"github.com/taigrr/crush/internal/transfer"
	"github.com/taigrr/crush/internal/version"
)

// resumePrompt is queued on the destination when a session was paused
// mid-turn to move it.
const resumePrompt = "You were paused mid-task and moved to another machine in the Crush cluster (from %s to %s). The project files were carried over as they were. Continue where you left off."

// TransferBridge is the part of [ClusterBridge] that moves sessions. It
// is a separate interface so single-node servers need not implement it.
type TransferBridge interface {
	TransferPreflight(ctx context.Context, nodeID string, req proto.TransferPreflightRequest) (proto.TransferPreflightResponse, error)
	TransferSend(ctx context.Context, nodeID string, plan transfer.Plan, resume bool, body io.Reader) (proto.TransferImportResponse, error)
}

// ErrNoCluster is returned when a cluster operation runs on a server that
// is not part of a cluster.
var ErrNoCluster = errors.New("this server is not part of a cluster")

// MoveSession hands a session, its history, and its project's on-disk
// state to another cluster node, then archives the local copy as moved.
// A session with an in-flight turn is paused first and, when
// req.Resume is set, continued on the destination.
func (b *Backend) MoveSession(ctx context.Context, req proto.MoveSessionRequest) (proto.MoveSessionResponse, error) {
	cb := b.clusterBridge()
	tb, ok := cb.(TransferBridge)
	if cb == nil || !ok {
		return proto.MoveSessionResponse{}, ErrNoCluster
	}
	if req.TargetNode == "" || req.TargetNode == cb.NodeID() {
		return proto.MoveSessionResponse{}, errors.New("choose a different node to move to")
	}
	ws, err := b.GetWorkspace(req.WorkspaceID)
	if err != nil {
		return proto.MoveSessionResponse{}, err
	}
	sess, err := ws.Sessions.Get(ctx, req.SessionID)
	if err != nil {
		return proto.MoveSessionResponse{}, fmt.Errorf("session %s: %w", req.SessionID, err)
	}
	if sess.ParentSessionID != "" {
		return proto.MoveSessionResponse{}, errors.New("only top-level sessions can be moved")
	}
	if !b.ownedHere(sess.OwnerNode) {
		// Only the owner may hand a session off; a replica moving would
		// create a second owner.
		return proto.MoveSessionResponse{}, fmt.Errorf("%w: %s; ask that node to move it", ErrSessionNotOwned, b.nodeName(sess.OwnerNode))
	}

	b.transfers.begin(proto.TransferStatus{SessionID: sess.ID, Title: sess.Title, Root: ws.resolvedPath, Stage: "preparing"})
	defer b.transfers.end(sess.ID)
	stage := func(st string) {
		b.transfers.update(sess.ID, func(s *proto.TransferStatus) { s.Stage = st })
	}

	wasBusy := ws.AgentCoordinator != nil && ws.AgentCoordinator.IsSessionBusy(sess.ID)
	if wasBusy {
		stage("pausing the running turn")
		ws.AgentCoordinator.Cancel(sess.ID)
		if err := waitIdle(ctx, ws, sess.ID, 30*time.Second); err != nil {
			return proto.MoveSessionResponse{}, err
		}
	}

	root := ws.resolvedPath
	dataDir := ws.Cfg.Config().Options.DataDirectory
	dataRel, err := filepath.Rel(root, dataDir)
	if err != nil || filepath.IsAbs(dataRel) || strings.HasPrefix(dataRel, "..") {
		return proto.MoveSessionResponse{}, fmt.Errorf("data directory %s must live inside the project root to move sessions", dataDir)
	}
	git := transfer.ReadGitState(ctx, root)

	if req.GC && ws.Checkpoints != nil {
		stage("cleaning up snapshots")
		if freed, err := ws.Checkpoints.GC(ctx); err != nil {
			slog.Warn("Snapshot cleanup before move failed", "session", sess.ID, "error", err)
		} else {
			slog.Info("Snapshot cleanup before move", "session", sess.ID, "freed", freed)
		}
	}
	preReq := proto.TransferPreflightRequest{
		Root:          root,
		SessionID:     sess.ID,
		Git:           git,
		SchemaVersion: db.LatestSchemaVersion(),
		Version:       version.Version,
		OnConflict:    req.OnConflict,
	}
	// The preflight changes nothing, so retry it across brief network
	// blips (lossy links, VPN reconnects) before giving up.
	stage("checking the destination")
	var pre proto.TransferPreflightResponse
	for attempt := range 3 {
		pre, err = tb.TransferPreflight(ctx, req.TargetNode, preReq)
		if err == nil || !isTransient(err) || attempt == 2 {
			break
		}
		slog.Warn("Transfer preflight failed; retrying", "target", req.TargetNode, "error", err)
		select {
		case <-ctx.Done():
			return proto.MoveSessionResponse{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
	if err != nil {
		return proto.MoveSessionResponse{}, err
	}

	manifest := transfer.Manifest{
		Version:        version.Version,
		SourceNodeID:   cb.NodeID(),
		SourceNodeName: cb.NodeName(),
		SessionID:      sess.ID,
		Title:          sess.Title,
		Root:           root,
		DataDirRel:     filepath.ToSlash(dataRel),
		Git:            git,
		WasBusy:        wasBusy,
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(transfer.Export(ctx, pw, transfer.ExportParams{
			DB:       ws.DB(),
			Manifest: manifest,
			Plan:     pre.Plan,
			Have:     pre.Have,
			OnProgress: func(p transfer.Progress) {
				b.transfers.update(sess.ID, func(s *proto.TransferStatus) {
					s.Stage, s.Files, s.TotalFiles, s.Bytes, s.TotalBytes = p.Stage, p.Files, p.TotalFiles, p.Bytes, p.TotalBytes
					s.CurrentFile = p.CurrentFile
				})
			},
		}))
	}()
	b.transfers.update(sess.ID, func(s *proto.TransferStatus) { s.TargetName = pre.NodeName })
	res, err := tb.TransferSend(ctx, req.TargetNode, pre.Plan, req.Resume, pr)
	_ = pr.Close()
	if err != nil {
		return proto.MoveSessionResponse{}, fmt.Errorf("move to %s: %w", pre.NodeName, err)
	}

	// The destination owns the session now. The local copy stays as a
	// read-only replica that replication keeps current.
	q := db.New(ws.DB())
	if err := q.SetSessionOwner(ctx, db.SetSessionOwnerParams{
		ID:        sess.ID,
		OwnerNode: sql.NullString{String: req.TargetNode, Valid: true},
	}); err != nil {
		return proto.MoveSessionResponse{}, fmt.Errorf("moved, but failed to release local ownership: %w", err)
	}
	_ = q.SetSessionMovedTo(ctx, db.SetSessionMovedToParams{
		ID:          sess.ID,
		MovedToNode: sql.NullString{String: pre.NodeName, Valid: true},
	})
	slog.Info("Session moved", "session", sess.ID, "to", pre.NodeName, "plan", pre.Plan)
	return proto.MoveSessionResponse{
		TargetNode:     req.TargetNode,
		TargetNodeName: pre.NodeName,
		Root:           root,
		SessionID:      sess.ID,
		Plan:           pre.Plan,
		BackupPath:     res.BackupPath,
		Resumed:        res.Resumed,
	}, nil
}

func waitIdle(ctx context.Context, ws *Workspace, sessionID string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for ws.AgentCoordinator.IsSessionBusy(sessionID) {
		if time.Now().After(deadline) {
			return errors.New("timed out pausing the running turn")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

// attachedAt returns the attached workspace hosting root, if any.
func (b *Backend) attachedAt(root string) (*Workspace, bool) {
	b.mu.Lock()
	id, ok := b.pathIndex[root]
	b.mu.Unlock()
	if !ok {
		return nil, false
	}
	return b.workspaces.Get(id)
}

// TransferPreflight decides, without changing anything, how this node
// would receive a session rooted at req.Root.
func (b *Backend) TransferPreflight(ctx context.Context, req proto.TransferPreflightRequest) (proto.TransferPreflightResponse, error) {
	if req.SchemaVersion != db.LatestSchemaVersion() {
		return proto.TransferPreflightResponse{}, fmt.Errorf("%w (source %d, here %d)", transfer.ErrSchemaMismatch, req.SchemaVersion, db.LatestSchemaVersion())
	}
	if !filepath.IsAbs(req.Root) {
		return proto.TransferPreflightResponse{}, errors.New("root must be absolute")
	}
	root := filepath.Clean(req.Root)
	if err := b.checkNotOwnedHere(ctx, root, req.SessionID); err != nil {
		return proto.TransferPreflightResponse{}, err
	}
	dest, err := transfer.InspectDest(ctx, root, ".crush")
	if err != nil {
		return proto.TransferPreflightResponse{}, err
	}
	plan, err := transfer.Decide(dest, req.Git, root, req.OnConflict)
	if err != nil {
		return proto.TransferPreflightResponse{}, err
	}
	if plan == transfer.PlanBackup {
		if ws, attached := b.attachedAt(root); attached && !b.closeIfUnused(ws) {
			return proto.TransferPreflightResponse{}, transfer.ErrDestinationBusy
		}
	}
	resp := proto.TransferPreflightResponse{Plan: plan}
	if plan == transfer.PlanOverlay || plan == transfer.PlanBackup {
		opts := transfer.TreeOptions{DataDirRel: ".crush", Excludes: transfer.DefaultExcludes, Overlay: plan == transfer.PlanOverlay}
		opts.Ignored = transfer.GitIgnored(ctx, root)
		resp.Have = transfer.IndexTree(ctx, root, opts)
	}
	if cb := b.clusterBridge(); cb != nil {
		resp.NodeID, resp.NodeName = cb.NodeID(), cb.NodeName()
	}
	return resp, nil
}

// checkNotOwnedHere refuses a move onto a node that already has the
// session active (it would create two owners). An archived copy left by
// an earlier move away from this node is fine: it is replaced.
func (b *Backend) checkNotOwnedHere(ctx context.Context, root, sessionID string) error {
	dataDir := filepath.Join(root, ".crush")
	if ws, ok := b.attachedAt(root); ok {
		dataDir = ws.Cfg.Config().Options.DataDirectory
	}
	conn, err := db.OpenReadOnly(dataDir)
	if err != nil || conn == nil {
		return nil
	}
	defer conn.Close()
	var owner, movedTo sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT owner_node, moved_to_node FROM sessions WHERE id = ?`, sessionID).Scan(&owner, &movedTo)
	if err != nil {
		return nil // Not present (or unreadable): nothing to protect.
	}
	if b.ownedHere(owner.String) && !movedTo.Valid {
		return transfer.ErrSessionOwned
	}
	return nil
}

// ImportTransfer receives a bundle streamed by another node.
func (b *Backend) ImportTransfer(ctx context.Context, r io.Reader, plan transfer.Plan, resume bool) (proto.TransferImportResponse, error) {
	res, err := transfer.Import(ctx, r, transfer.ImportParams{
		Plan: plan,
		Check: func(m transfer.Manifest) error {
			if m.SchemaVersion != db.LatestSchemaVersion() {
				return transfer.ErrSchemaMismatch
			}
			return b.checkNotOwnedHere(ctx, filepath.Clean(m.Root), m.SessionID)
		},
		OpenDB: func(ctx context.Context, dataDir string) (*sql.DB, func(), error) {
			conn, err := db.Connect(ctx, dataDir, db.WithDataDirLock(true))
			if err != nil {
				return nil, nil, err
			}
			return conn, func() { _ = db.Release(dataDir) }, nil
		},
	})
	if err != nil {
		return proto.TransferImportResponse{}, err
	}
	m := res.Manifest
	root := filepath.Clean(m.Root)
	dataDir := filepath.Join(root, filepath.FromSlash(m.DataDirRel))

	// The imported copy is the live one: clear any moved/archived marks
	// carried over from an earlier hop.
	if conn, err := db.Connect(ctx, dataDir, db.WithDataDirLock(true)); err == nil {
		_, _ = conn.ExecContext(ctx, `UPDATE sessions SET owner_node = NULL, moved_to_node = NULL WHERE id = ? OR parent_session_id = ?`, m.SessionID, m.SessionID)
		_ = db.Release(dataDir)
	}
	if b.registry != nil {
		if err := b.registry.Add(registry.Entry{Root: root, DataDir: dataDir}); err != nil {
			slog.Warn("Failed to register imported workspace", "root", root, "error", err)
		}
	}
	out := proto.TransferImportResponse{Root: root, SessionID: m.SessionID, Plan: res.Plan, BackupPath: res.BackupPath}
	if resume && m.WasBusy {
		if err := b.resumeImported(root, m); err != nil {
			slog.Warn("Failed to resume moved session", "session", m.SessionID, "error", err)
		} else {
			out.Resumed = true
		}
	}
	slog.Info("Session received", "session", m.SessionID, "from", m.SourceNodeName, "plan", res.Plan)
	return out, nil
}

func (b *Backend) resumeImported(root string, m transfer.Manifest) error {
	ws, _, err := b.CreateWorkspace(proto.Workspace{Path: root, ClientID: uuid.NewString()})
	if err != nil {
		return err
	}
	here := "this machine"
	if cb := b.clusterBridge(); cb != nil {
		here = cb.NodeName()
	}
	return b.SendMessage(ws.ID, proto.AgentMessage{
		SessionID: m.SessionID,
		Prompt:    fmt.Sprintf(resumePrompt, m.SourceNodeName, here),
	})
}

// closeIfUnused tears ws down when nobody is looking at it — no live
// event streams (clients that merely disconnected recently do not count)
// and no running turn — so its directory can be moved aside. It reports
// whether the workspace is gone.
func (b *Backend) closeIfUnused(ws *Workspace) bool {
	if ws.agentBusy() {
		return false
	}
	ws.clientsMu.Lock()
	for _, cs := range ws.clients {
		if cs.streams > 0 {
			ws.clientsMu.Unlock()
			return false
		}
	}
	for id, cs := range ws.clients {
		if cs.holdTimer != nil {
			cs.holdTimer.Stop()
		}
		cs.closeBridge()
		delete(ws.clients, id)
	}
	ws.clientsMu.Unlock()
	b.teardown(ws)
	_, still := b.attachedAt(ws.resolvedPath)
	return !still
}

// isTransient reports network-level failures worth retrying.
func isTransient(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	for _, s := range []string{"TLS handshake timeout", "connection refused", "connection reset", "unreachable", "i/o timeout", "EOF"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// transferTracker records moves in flight for progress reporting.
type transferTracker struct {
	mu     sync.Mutex
	active map[string]*proto.TransferStatus
}

func (t *transferTracker) begin(s proto.TransferStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.active = map[string]*proto.TransferStatus{}
	}
	s.StartedAt = time.Now().UnixMilli()
	t.active[s.SessionID] = &s
}

func (t *transferTracker) update(sessionID string, fn func(*proto.TransferStatus)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.active[sessionID]; ok {
		fn(s)
	}
}

func (t *transferTracker) end(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, sessionID)
}

// Transfers lists moves currently running from this node.
func (b *Backend) Transfers() []proto.TransferStatus {
	b.transfers.mu.Lock()
	defer b.transfers.mu.Unlock()
	out := make([]proto.TransferStatus, 0, len(b.transfers.active))
	for _, s := range b.transfers.active {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, c proto.TransferStatus) int { return cmp.Compare(a.StartedAt, c.StartedAt) })
	return out
}
