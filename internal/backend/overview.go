package backend

import (
	"context"
	"log/slog"
	"sort"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
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
	// Snapshot attached workspaces keyed by resolved root so registry
	// entries for the same root are merged into the live view.
	attached := make(map[string]*Workspace)
	for _, ws := range b.workspaces.Seq2() {
		attached[ws.resolvedPath] = ws
	}

	var out []proto.WorkspaceOverview
	seen := make(map[string]struct{})

	// Attached workspaces first, in a stable order (by root).
	attachedRoots := make([]string, 0, len(attached))
	for root := range attached {
		attachedRoots = append(attachedRoots, root)
	}
	sort.Strings(attachedRoots)
	for _, root := range attachedRoots {
		ws := attached[root]
		ov := proto.WorkspaceOverview{
			Root:        root,
			WorkspaceID: ws.ID,
			Attached:    true,
		}
		if ws.Cfg != nil {
			ov.DataDir = ws.Cfg.Config().Options.DataDirectory
		}
		ov.Sessions = b.attachedSessionOverviews(ctx, ws)
		sortSessionOverviews(ov.Sessions)
		out = append(out, ov)
		seen[root] = struct{}{}
	}

	// Registry workspaces not currently attached, read-only.
	if b.registry != nil {
		entries, err := b.registry.List()
		if err != nil {
			slog.Warn("Failed to list workspace registry", "error", err)
		}
		for _, e := range entries {
			if _, ok := seen[e.Root]; ok {
				continue
			}
			seen[e.Root] = struct{}{}
			peeked, err := db.PeekSessions(ctx, e.DataDir)
			if err != nil {
				slog.Debug("Failed to peek workspace sessions", "root", e.Root, "error", err)
				continue
			}
			ov := proto.WorkspaceOverview{
				Root:     e.Root,
				DataDir:  e.DataDir,
				Attached: false,
			}
			for _, p := range peeked {
				ov.Sessions = append(ov.Sessions, proto.SessionOverview{
					ID:         p.ID,
					Title:      p.Title,
					WorkingDir: p.WorkingDir,
					UpdatedAt:  p.UpdatedAt,
					IsBusy:     false, // not attached: no live state
					Unread:     p.Unread(),
					Color:      p.Color,
					Animal:     p.Animal,
					Favorite:   p.Favorite,

					SpawnedBySessionID: p.SpawnedBySessionID,
				})
			}
			sortSessionOverviews(ov.Sessions)
			out = append(out, ov)
		}
	}

	return out, nil
}

// attachedSessionOverviews lists a live workspace's top-level sessions with
// live busy state and computed unread state. Busy includes accepted-but-
// not-yet-active runs: clients refresh overviews on the AttentionBusy event
// runAgent publishes at dispatch, which precedes the run registering as
// active, so a strict active-only check would report a just-started turn as
// idle and leave the navigator stale for the whole turn.
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
			busy = ws.AgentCoordinator.IsSessionBusyOrAccepted(s.ID)
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

			SpawnedBySessionID: s.SpawnedBySessionID,
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
