package backend

import (
	"context"
	"log/slog"
	"sort"

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

	sessionLimit, candidateLimit := app.ResolveSearchLimits(params.Limit)
	scope := historysearch.Scope(params.Scope)
	// Global embedding params (see doc comment): resolved once, reused for
	// every detached workspace's read-only embedder.
	embParams := reqWS.Store().EmbeddingParams()

	var merged []proto.SessionHit
	semanticUsed := false

	// Attached workspaces first, deduped by resolved root so a registry
	// Attached workspaces, iterated in a deterministic order (by resolved
	// root) so the merged result is stable run-to-run — Seq2 ranges a Go
	// map in random order, which would otherwise make tied-score ordering
	// (and thus the cap survivors) nondeterministic. Mirrors overview.go.
	attached := make(map[string]*Workspace)
	for _, ws := range b.workspaces.Seq2() {
		if ws.App == nil {
			continue
		}
		attached[ws.resolvedPath] = ws
	}
	attachedRoots := make([]string, 0, len(attached))
	for root := range attached {
		attachedRoots = append(attachedRoots, root)
	}
	sort.Strings(attachedRoots)

	// entry for the same root isn't searched twice.
	seen := make(map[string]struct{})
	for _, root := range attachedRoots {
		ws := attached[root]
		seen[ws.resolvedPath] = struct{}{}
		res, err := ws.SearchHistoryHits(ctx, params.Query, scope, params.Semantic, candidateLimit)
		if err != nil {
			slog.Debug("Cross-workspace search: attached workspace failed, skipping",
				"workspace", ws.ID, "error", err)
			continue
		}
		if res.SemanticUsed {
			semanticUsed = true
		}
		// Tag with resolvedPath (the dedup key and overview.go's
		// convention) so a hit's root matches what the client compares
		// against BaseDir(); ws.Path could in principle diverge.
		merged = append(merged, tagHits(res.Hits, ws.ID, ws.resolvedPath)...)
	}

	// Registry-known but detached workspaces, read-only.
	if b.registry != nil {
		entries, err := b.registry.List()
		if err != nil {
			slog.Warn("Cross-workspace search: failed to list registry", "error", err)
		}
		for _, e := range entries {
			if _, ok := seen[e.Root]; ok {
				continue
			}
			seen[e.Root] = struct{}{}
			hits, used, err := searchDetachedWorkspace(ctx, e.DataDir, e.Root, params.Query, scope, params.Semantic, candidateLimit, embParams)
			if err != nil {
				slog.Debug("Cross-workspace search: detached workspace failed, skipping",
					"root", e.Root, "error", err)
				continue
			}
			if used {
				semanticUsed = true
			}
			merged = append(merged, hits...)
		}
	}

	return mergeCrossWorkspaceHits(merged, semanticUsed, sessionLimit), nil
}

// searchDetachedWorkspace opens a detached workspace's database read-only,
// runs the hybrid search with an embedder built from the shared global
// params, and returns its message-level hits tagged with the workspace
// root. Returns (nil, false, nil) when the workspace has no database yet.
// The read-only handle is always closed before returning.
func searchDetachedWorkspace(
	ctx context.Context,
	dataDir, root, query string,
	scope historysearch.Scope,
	semantic *bool,
	candidateLimit int,
	embParams embedding.Params,
) ([]proto.SessionHit, bool, error) {
	conn, err := db.OpenReadOnly(dataDir)
	if err != nil {
		return nil, false, err
	}
	if conn == nil {
		return nil, false, nil // no database file yet
	}
	defer conn.Close()

	queries := db.New(conn)
	messages := message.NewService(queries)
	sessions := session.NewService(queries, conn)
	emb := embedding.Build(queries, embParams)

	res, err := historysearch.Search(ctx, messages, sessions, emb, query, historysearch.Options{
		Scope:    scope,
		Semantic: semantic,
		Limit:    candidateLimit,
		Offset:   0,
	})
	if err != nil {
		return nil, false, err
	}
	// Detached workspaces have no server-side workspace id; tag with the
	// root only. Commit routes by root (SwitchWorkspace), so id is not
	// required for navigation.
	return tagHits(res.Hits, "", root), res.SemanticUsed, nil
}

// tagHits converts message-level embedding hits into workspace-tagged
// proto.SessionHit rows (still message-granular; collapsed later).
func tagHits(hits []embedding.Hit, workspaceID, workspaceRoot string) []proto.SessionHit {
	out := make([]proto.SessionHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, proto.SessionHit{
			SessionID:     h.SessionID,
			SessionTitle:  h.SessionTitle,
			WorkspaceID:   workspaceID,
			WorkspaceRoot: workspaceRoot,
			Score:         h.Score,
			Match:         string(h.Match),
			Snippet:       h.Snippet,
			MessageID:     h.SourceID,
			Role:          h.Role,
			CreatedAt:     h.CreatedAt,
		})
	}
	return out
}

// mergeCrossWorkspaceHits re-ranks the merged message-level hits by fused
// score (descending), collapses to one representative hit per (workspace
// root, session) keeping the best-scoring one, then caps to sessionLimit.
// Dedup is keyed on workspace root + session id so identical session ids
// in different workspaces are never conflated. Total reflects the number
// of distinct sessions found across the merged candidate windows (an
// approximation when a corpus exceeds its per-workspace candidate limit).
func mergeCrossWorkspaceHits(hits []proto.SessionHit, semanticUsed bool, sessionLimit int) proto.SearchHistoryResult {
	// Sort by score desc with a deterministic tie-break. Fused RRF scores
	// tie constantly across workspaces (every workspace's rank-1 hit
	// scores ~1/(rrfK+1), rank-2 the same, etc.), so without a tie-break
	// both the display order AND which sessions survive the cap would be
	// nondeterministic. Break ties by workspace root, then session id,
	// then message id so identical searches always yield identical output.
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.WorkspaceRoot != b.WorkspaceRoot {
			return a.WorkspaceRoot < b.WorkspaceRoot
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		return a.MessageID < b.MessageID
	})

	type sessionKey struct{ root, id string }
	seen := make(map[sessionKey]struct{}, len(hits))
	collapsed := make([]proto.SessionHit, 0, len(hits))
	for _, h := range hits {
		key := sessionKey{root: h.WorkspaceRoot, id: h.SessionID}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		collapsed = append(collapsed, h)
	}

	total := len(collapsed)
	if sessionLimit > 0 && len(collapsed) > sessionLimit {
		collapsed = collapsed[:sessionLimit]
	}
	return proto.SearchHistoryResult{
		Hits:         collapsed,
		Total:        total,
		SemanticUsed: semanticUsed,
	}
}
