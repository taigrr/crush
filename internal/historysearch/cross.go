package historysearch

import (
	"context"
	"sort"
	"time"

	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

// Source is one workspace to include in a cross-workspace search: its
// resolved project root (used to tag and dedup hits) plus the message,
// session, and embedding services backing its database.
type Source struct {
	Root     string
	Messages message.Service
	Sessions session.Service
	Emb      embedding.Service
}

// CrossHit is one message-level hit tagged with its originating
// workspace root.
type CrossHit struct {
	Root         string              `json:"workspace_root"`
	SessionID    string              `json:"session_id"`
	SessionTitle string              `json:"session_title"`
	Match        embedding.MatchType `json:"match"`
	Snippet      string              `json:"snippet"`
	MessageID    string              `json:"message_id"`
	Role         string              `json:"role"`
	CreatedAt    time.Time           `json:"created_at"`
	score        float64
}

// CrossResult is the merged, ranked, per-session page produced by
// [SearchAcross].
type CrossResult struct {
	Hits         []CrossHit `json:"hits"`
	Total        int        `json:"total"`
	SemanticUsed bool       `json:"semantic_used"`
}

// SearchAcross runs the hybrid search over every source, tags each hit
// with its workspace root, merges all hits by fused score, collapses to
// one representative hit per (root, session), and caps to sessionLimit.
// A source whose search errors is skipped (its error is returned only if
// every source errored and nothing matched). candidateLimit bounds the
// per-source candidate window; sessionLimit caps the collapsed result.
//
// This mirrors the backend's own attached+detached fan-out
// (internal/backend/search.go) but works against a caller-provided set
// of already-opened services, so the `crush search` CLI can fan out over
// the registry without a running backend.
func SearchAcross(
	ctx context.Context,
	sources []Source,
	query string,
	scope Scope,
	semantic *bool,
	candidateLimit, sessionLimit int,
) (CrossResult, error) {
	if candidateLimit <= 0 {
		candidateLimit = 20
	}
	var merged []CrossHit
	semanticUsed := false
	var firstErr error

	for _, src := range sources {
		res, err := Search(ctx, src.Messages, src.Sessions, src.Emb, query, Options{
			Scope:    scope,
			Semantic: semantic,
			Limit:    candidateLimit,
			Offset:   0,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if res.SemanticUsed {
			semanticUsed = true
		}
		for _, h := range res.Hits {
			merged = append(merged, CrossHit{
				Root:         src.Root,
				SessionID:    h.SessionID,
				SessionTitle: h.SessionTitle,
				Match:        h.Match,
				Snippet:      h.Snippet,
				MessageID:    h.SourceID,
				Role:         h.Role,
				CreatedAt:    h.CreatedAt,
				score:        h.Score,
			})
		}
	}

	if len(merged) == 0 && firstErr != nil {
		return CrossResult{}, firstErr
	}

	// Rank by fused score desc with a deterministic tie-break (root,
	// session, message) so identical queries yield identical output.
	sort.Slice(merged, func(i, j int) bool {
		a, b := merged[i], merged[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.Root != b.Root {
			return a.Root < b.Root
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		return a.MessageID < b.MessageID
	})

	type sessionKey struct{ root, id string }
	seen := make(map[sessionKey]struct{}, len(merged))
	collapsed := make([]CrossHit, 0, len(merged))
	for _, h := range merged {
		key := sessionKey{root: h.Root, id: h.SessionID}
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
	return CrossResult{Hits: collapsed, Total: total, SemanticUsed: semanticUsed}, nil
}
