package historysearch

import (
	"cmp"
	"context"
	"slices"
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
	// Score is the fused rank score (higher is better), comparable only
	// within one result.
	Score float64 `json:"score"`
}

// CrossResult is the merged, ranked, per-session page produced by
// [SearchAcross].
type CrossResult struct {
	Hits         []CrossHit `json:"hits"`
	Total        int        `json:"total"`
	SemanticUsed bool       `json:"semantic_used"`
	// Mode is hybrid when any source ran hybrid, otherwise text.
	Mode embedding.SearchMode `json:"mode"`
	// FallbackReason is the first source's reason when Mode is text.
	FallbackReason embedding.FallbackReason `json:"fallback_reason,omitempty"`
}

// SearchAcross runs the hybrid search over every source, tags each hit
// with its workspace root, merges all hits by fused score, collapses to
// one representative hit per (root, session), and caps to sessionLimit.
// A source whose search errors is skipped; the first error is returned
// only if every source errored. candidateLimit bounds the
// per-source candidate window; sessionLimit caps the collapsed result.
//
// It is the one search behind the /v1 history search (single and
// cross-workspace), the TUI, and `crush search --all-workspaces`.
func SearchAcross(
	ctx context.Context,
	sources []Source,
	query string,
	scope Scope,
	semantic *bool,
	candidateLimit, sessionLimit int,
) (CrossResult, error) {
	if candidateLimit <= 0 {
		_, candidateLimit = ResolveLimits(sessionLimit)
	}
	var merged []CrossHit
	semanticUsed := false
	mode := embedding.SearchModeText
	var reason embedding.FallbackReason
	var firstErr error
	failed := 0

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
			failed++
			continue
		}
		if res.SemanticUsed {
			semanticUsed = true
		}
		if res.Mode == embedding.SearchModeHybrid {
			mode = embedding.SearchModeHybrid
		} else if reason == "" {
			reason = res.FallbackReason
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
				Score:        h.Score,
			})
		}
	}

	if len(sources) > 0 && failed == len(sources) {
		return CrossResult{}, firstErr
	}

	collapsed, total := MergeHits(merged, sessionLimit)
	if mode == embedding.SearchModeHybrid {
		reason = ""
	}
	return CrossResult{Hits: collapsed, Total: total, SemanticUsed: semanticUsed, Mode: mode, FallbackReason: reason}, nil
}

// MergeHits ranks message-level hits by fused score with a deterministic
// tie-break (root, session, message), collapses them to one hit per
// (root, session), and caps the result to sessionLimit (0 = no cap). It
// returns the hits and the number of distinct sessions before the cap.
// Fused scores tie constantly across workspaces, so without the
// tie-break the order and the cap survivors would be nondeterministic.
func MergeHits(hits []CrossHit, sessionLimit int) ([]CrossHit, int) {
	hits = slices.Clone(hits)
	slices.SortFunc(hits, func(a, b CrossHit) int {
		return cmp.Or(
			cmp.Compare(b.Score, a.Score),
			cmp.Compare(a.Root, b.Root),
			cmp.Compare(a.SessionID, b.SessionID),
			cmp.Compare(a.MessageID, b.MessageID),
		)
	})
	type sessionKey struct{ root, id string }
	seen := make(map[sessionKey]struct{}, len(hits))
	collapsed := make([]CrossHit, 0, len(hits))
	for _, h := range hits {
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
	return collapsed, total
}

// Session and candidate limits shared by every caller of [SearchAcross]
// (the /v1 history search, the TUI, and `crush search`).
const (
	// candidateFactor over-fetches message-level hits relative to the
	// sessions requested, so the per-session collapse picks each
	// session's globally best hit. minCandidates is the floor.
	candidateFactor = 10
	minCandidates   = 200
	// DefaultSessionLimit applies when no limit is given.
	DefaultSessionLimit = 50
	// MaxSessionLimit caps a caller-supplied limit so a hostile one
	// cannot force an unbounded candidate fetch.
	MaxSessionLimit = 200
)

// ResolveLimits turns a requested session limit into the effective
// (sessionLimit, candidateLimit) pair: the default when non-positive,
// clamped to MaxSessionLimit, with the over-fetch candidate window.
func ResolveLimits(limit int) (sessionLimit, candidateLimit int) {
	sessionLimit = limit
	if sessionLimit <= 0 {
		sessionLimit = DefaultSessionLimit
	}
	sessionLimit = min(sessionLimit, MaxSessionLimit)
	return sessionLimit, max(sessionLimit*candidateFactor, minCandidates)
}
