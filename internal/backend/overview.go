package backend

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"sync"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/registry"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/startuptrace"
)

// ListWorkspaceOverviews returns every known workspace and its top-level
// sessions for the cross-workspace picker.
//
// Attached workspaces (currently hosted by this server) report live busy
// state and read sessions from their in-memory service. Workspaces known
// only from the global registry are read from their database READ-ONLY via
// [db.PeekSessions] — no attach, no migrations, no lock — so listing never
// disturbs a workspace that is not open. Opening one later goes through a
// fresh read-write attach.
//
// Within each workspace, sessions are ordered busy-first, then unread,
// then most-recently-updated, so live and unseen-complete work floats to
// the top. Workspaces are ordered attached-first, then by registry
// recency.
func (b *Backend) ListWorkspaceOverviews(ctx context.Context) ([]proto.WorkspaceOverview, error) {
	return b.ListWorkspaceOverviewsLimit(ctx, 0)
}

// ListWorkspaceOverviewsLimit is [Backend.ListWorkspaceOverviews] keeping
// at most limit sessions per workspace (in display order) and reporting
// the full count in SessionCount. A limit <= 0 lists everything.
func (b *Backend) ListWorkspaceOverviewsLimit(ctx context.Context, limit int) ([]proto.WorkspaceOverview, error) {
	all, err := b.listAllOverviews(ctx)
	if err != nil {
		return nil, err
	}
	owned, _ := b.splitByOwner(all)
	return limitOverviews(owned, limit), nil
}

// limitOverviews truncates each workspace's sessions to limit, recording
// the original count. Sessions must already be in display order. Busy
// sessions (running agent or pending permission) sort first and are never
// cut, so live work always reaches the sidebar.
func limitOverviews(ovs []proto.WorkspaceOverview, limit int) []proto.WorkspaceOverview {
	if limit <= 0 {
		return ovs
	}
	for i := range ovs {
		keep := limit
		for keep < len(ovs[i].Sessions) && ovs[i].Sessions[keep].IsBusy {
			keep++
		}
		if n := len(ovs[i].Sessions); n > keep {
			ovs[i].SessionCount = max(ovs[i].SessionCount, n)
			ovs[i].Sessions = slices.Clip(ovs[i].Sessions[:keep])
		}
	}
	return ovs
}

// listAllOverviews lists every session, including read-only replicas of
// sessions owned by other cluster nodes.
func (b *Backend) listAllOverviews(ctx context.Context) ([]proto.WorkspaceOverview, error) {
	defer startuptrace.Span("srv.list_overviews")()
	// Snapshot attached workspaces keyed by resolved root so registry
	// entries for the same root are merged into the live view.
	attached := make(map[string]*Workspace)
	for _, ws := range b.workspaces.Seq2() {
		attached[ws.resolvedPath] = ws
	}

	var out []proto.WorkspaceOverview
	seen := make(map[string]struct{})
	// Two roots can share one database (a symlinked or unresolved path
	// registered alongside the resolved one, or worktrees of one project).
	// Listing each would show every session twice.
	seenDataDirs := make(map[string]struct{})

	// Attached workspaces first, in a stable order (by root).
	attachedRoots := make([]string, 0, len(attached))
	for root := range attached {
		attachedRoots = append(attachedRoots, root)
	}
	slices.Sort(attachedRoots)
	for _, root := range attachedRoots {
		ws := attached[root]
		ov := proto.WorkspaceOverview{
			Root:        root,
			WorkspaceID: ws.ID,
			Attached:    true,
		}
		if ws.Cfg != nil {
			ov.DataDir = ws.Cfg.Config().Options.DataDirectory
			seenDataDirs[filepath.Clean(ov.DataDir)] = struct{}{}
		}
		ov.Sessions = b.attachedSessionOverviews(ctx, ws)
		sortSessionOverviews(ov.Sessions)
		out = append(out, ov)
		seen[root] = struct{}{}
	}

	// Registry workspaces not currently attached, read-only. Each is
	// peeked concurrently under its own deadline, so one unreadable
	// database marks that workspace unavailable instead of stalling the
	// whole list.
	if b.registry != nil {
		endList := startuptrace.Span("srv.registry_list")
		entries, err := b.registry.List()
		endList()
		startuptrace.Count("srv.registry_entries", len(entries))
		if err != nil {
			slog.Warn("Failed to list workspace registry", "error", err)
		}
		hideTemp := b.hideTempWorkspaces()
		var pending []registry.Entry
		for _, e := range entries {
			if _, ok := seen[e.Root]; ok {
				continue
			}
			seen[e.Root] = struct{}{}
			if hideTemp && isTempWorkspace(e.Root, e.DataDir) {
				continue
			}
			dd := filepath.Clean(e.DataDir)
			if _, ok := seenDataDirs[dd]; ok {
				continue
			}
			seenDataDirs[dd] = struct{}{}
			pending = append(pending, e)
		}
		startuptrace.Count("srv.peek_dbs", len(pending))
		endPeek := startuptrace.Span("srv.peek_overviews")
		out = append(out, b.peekOverviews(ctx, pending)...)
		endPeek()
	}

	return out, nil
}

// peekOverviews reads detached workspaces' sessions with bounded
// concurrency, preserving entries' order. Hundreds of simultaneous reads
// contend for disk and CPU until most miss their deadline; a small pool
// finishes them all sooner. Workspaces whose database could not be read
// are kept and flagged so callers can tell "empty" from "unreadable", or
// served from their last listing marked stale when one is cached.
//
// Sessions slices are shared with the cache: callers must not modify
// their elements in place, and are clipped so appends copy.
func (b *Backend) peekOverviews(ctx context.Context, entries []registry.Entry) []proto.WorkspaceOverview {
	out := make([]proto.WorkspaceOverview, len(entries))
	sem := make(chan struct{}, peekConcurrency())
	var wg sync.WaitGroup
	for i, e := range entries {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			ov := proto.WorkspaceOverview{Root: e.Root, DataDir: e.DataDir}
			sessions, staleSince, err := b.peekCache.overviews(ctx, e.DataDir)
			switch {
			case err == nil:
			case !staleSince.IsZero():
				ov.Stale, ov.StaleSince, ov.LastError = true, staleSince.UnixMilli(), err.Error()
			default:
				slog.Debug("Failed to peek workspace sessions", "root", e.Root, "error", err)
				ov.Unavailable, ov.Error = true, err.Error()
			}
			ov.Sessions = slices.Clip(sessions)
			out[i] = ov
		})
	}
	wg.Wait()
	return out
}

// peekConcurrency bounds simultaneous detached-workspace reads.
func peekConcurrency() int {
	return max(4, runtime.GOMAXPROCS(0))
}

// attachedSessionOverviews lists a live workspace's top-level sessions with
// live busy state and computed unread state.
func (b *Backend) attachedSessionOverviews(ctx context.Context, ws *Workspace) []proto.SessionOverview {
	if ws.App == nil || ws.Sessions == nil {
		return nil
	}
	sessions, err := ws.Sessions.List(ctx)
	if err != nil {
		slog.Debug("Failed to list attached workspace sessions", "workspace", ws.ID, "error", err)
		return nil
	}
	out := make([]proto.SessionOverview, 0, len(sessions))
	for _, s := range sessions {
		busy := false
		if ws.AgentCoordinator != nil {
			busy = ws.AgentCoordinator.IsSessionBusy(s.ID)
		}
		out = append(out, proto.SessionOverview{
			ID:         s.ID,
			Title:      s.Title,
			WorkingDir: s.WorkingDir,
			UpdatedAt:  s.UpdatedAt,
			IsBusy:     busy,
			Unread:     unreadSession(s),
			Color:      s.Color,
			Animal:     s.Animal,
			Favorite:   s.Favorite,
			OwnerNode:  s.OwnerNode,
		})
	}
	return out
}

// unreadSession mirrors session.Session.Unread for the overview.
func unreadSession(s session.Session) bool {
	return s.Unread()
}

// sortSessionOverviews orders sessions busy-first, then unread, then by
// most-recently-updated, so live and unseen-complete work is surfaced.
func sortSessionOverviews(s []proto.SessionOverview) {
	sort.SliceStable(s, func(i, j int) bool {
		a, b := s[i], s[j]
		if a.IsBusy != b.IsBusy {
			return a.IsBusy
		}
		if a.Unread != b.Unread {
			return a.Unread
		}
		return a.UpdatedAt > b.UpdatedAt
	})
}
