package backend

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/historysearch"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

// searchAllWorkspaces fans out a history search over every known
// workspace and merges the results into one ranked, per-session list.
//
// It searches attached workspaces via their live services and
// registry-known-but-detached ones via a READ-ONLY database open (no
// migrations, no lock — the same safe pattern as ListWorkspaceOverviews /
// db.PeekSessions). Each workspace's message-level hits are tagged with
// its id/root, all hits are merged and re-ranked by fused score, then
// collapsed to one representative hit per (workspace, session) and capped.
//
// Fan-out is SEQUENTIAL (v1): workspaces are searched one at a time. This
// is simpler and avoids unbounded parallel DB opens / embedder calls; the
// candidate pool is bounded per workspace so total work stays reasonable.
//
// Embedder resolution: embeddings are a GLOBAL-only config setting (a
// workspace config can never fragment the embedding space — see config
// load), so every workspace on this machine shares one embedding
// signature. We therefore resolve embedding.Params ONCE from the
// requesting workspace and reuse it to build each detached workspace's
// read-only embedder. Because all stored vectors share that signature,
// their scores are directly comparable across workspaces. A workspace
// whose stored vectors predate a global embedding-model change (different
// signature) is filtered out by the active-signature match and silently
// degrades to substring for that workspace — acceptable for v1.
//
// A workspace whose DB is missing or unreadable is skipped (logged at
// debug), never failing the whole search.
func (b *Backend) searchAllWorkspaces(ctx context.Context, requestingWorkspaceID string, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error) {
	reqWS, err := b.GetWorkspace(requestingWorkspaceID)
	if err != nil {
		return proto.SearchHistoryResult{}, err
	}

	sessionLimit, candidateLimit := historysearch.ResolveLimits(params.Limit)
	// Global embedding params (see doc comment): resolved once, reused for
	// every detached workspace's read-only embedder.
	embParams := reqWS.Store().EmbeddingParams()

	// Attached workspaces, iterated in a deterministic order (by resolved
	// root) so the merged result is stable run-to-run. Mirrors
	// overview.go.
	attached := make(map[string]*Workspace)
	for _, ws := range b.workspaces.Seq2() {
		if ws.App == nil {
			continue
		}
		attached[ws.resolvedPath] = ws
	}
	roots := slices.Sorted(maps.Keys(attached))

	// Tag with resolvedPath (the dedup key and overview.go's convention)
	// so a hit's root matches what the client compares against
	// BaseDir().
	sources := make([]historysearch.Source, 0, len(roots))
	ids := make(map[string]string, len(roots))
	for _, root := range roots {
		ws := attached[root]
		sources = append(sources, ws.HistorySource(root))
		ids[root] = ws.ID
	}

	// Registry-known but detached workspaces, read-only. Detached
	// workspaces have no server-side workspace ID; their hits carry only
	// the root (commit routes by root).
	if b.registry != nil {
		entries, err := b.registry.List()
		if err != nil {
			slog.Warn("Cross-workspace search: failed to list registry", "error", err)
		}
		for _, e := range entries {
			if _, ok := ids[e.Root]; ok {
				continue
			}
			ids[e.Root] = ""
			src, closeDB, err := detachedHistorySource(e.DataDir, e.Root, embParams)
			if err != nil {
				slog.Debug("Cross-workspace search: detached workspace failed, skipping",
					"root", e.Root, "error", err)
				continue
			}
			if closeDB == nil {
				continue // no database yet
			}
			defer closeDB()
			sources = append(sources, src)
		}
	}

	res, err := historysearch.SearchAcross(ctx, sources, params.Query,
		historysearch.Scope(params.Scope), params.Semantic, candidateLimit, sessionLimit)
	if err != nil {
		return proto.SearchHistoryResult{}, err
	}
	return app.HistoryResultToProto(res, func(root string) string { return ids[root] }), nil
}

// detachedHistorySource opens a detached workspace's database read-only
// as a history search source with an embedder built from the shared
// global params. closeDB is nil when the workspace has no database yet.
func detachedHistorySource(dataDir, root string, embParams embedding.Params) (historysearch.Source, func(), error) {
	conn, err := db.OpenReadOnly(dataDir)
	if err != nil || conn == nil {
		return historysearch.Source{}, nil, err
	}
	queries := db.New(conn)
	return historysearch.Source{
		Root:     root,
		Messages: message.NewService(queries),
		Sessions: session.NewService(queries, conn),
		Emb:      embedding.Build(queries, embParams),
	}, func() { _ = conn.Close() }, nil
}
