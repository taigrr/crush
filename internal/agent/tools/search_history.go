package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/historysearch"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

// CrossWorkspaceHit is one per-session hit returned by a cross-workspace
// fan-out. It mirrors the backend's per-session result but is defined
// locally so the tools package doesn't import the backend/proto layers
// (which would create an import cycle).
type CrossWorkspaceHit struct {
	SessionID     string
	SessionTitle  string
	WorkspaceRoot string
	Match         string
	Snippet       string
	MessageID     string
	Role          string
	CreatedAt     time.Time
}

// CrossWorkspaceResult is the merged, ranked page of cross-workspace hits.
type CrossWorkspaceResult struct {
	Hits         []CrossWorkspaceHit
	Total        int
	SemanticUsed bool
}

// HistorySearcher is the minimal contract the search_history tool needs
// to fan a query out over every known workspace (attached and
// registry-detached) and merge the results. It is satisfied by the
// backend shim; when nil the tool searches only the local workspace.
type HistorySearcher interface {
	SearchAllWorkspaces(ctx context.Context, requestingWorkspaceID, query, scope string, semantic *bool, limit int) (CrossWorkspaceResult, error)
}

const (
	SearchHistoryToolName = "search_history"
	maxHistoryMatches     = 50
	// currentSessionSentinel is the session_id value that resolves to
	// the running session from context.
	currentSessionSentinel = "current"
)

//go:embed search_history.md
var searchHistoryDescription string

// SearchHistoryParams scopes the search. SessionID and Scope are
// independent: SessionID optionally limits the search to one session,
// and Scope optionally widens the role filter from user-only to all
// messages.
type SearchHistoryParams struct {
	Query     string              `json:"query" description:"Search query (case-insensitive). Matched as a substring and, when embeddings are enabled, semantically."`
	SessionID string              `json:"session_id,omitempty" description:"Optional: limit the search to a single session. Pass 'current' for the active session."`
	Scope     historysearch.Scope `json:"scope,omitempty" description:"Which messages to search: 'user' (default, user/shell messages only) or 'all' (include assistant replies and reasoning)"`
	Semantic  *bool               `json:"semantic,omitempty" description:"Force semantic (vector) matching on/off for this query. Defaults to the global hybrid_search setting."`
	Limit     int                 `json:"limit,omitempty" description:"Max matches to return per page (default 20, max 50)"`
	Offset    int                 `json:"offset,omitempty" description:"Number of matches to skip for pagination (default 0)"`
	// AllWorkspaces opts into cross-workspace search: when true the tool
	// searches every known workspace (attached and registry-detached)
	// and merges the results. Ignored (and unavailable) when SessionID
	// is set, since a session id is workspace-local.
	AllWorkspaces bool `json:"all_workspaces,omitempty" description:"Search across every known workspace (attached and detached), not just the current one. Cannot be combined with session_id."`
}

// NewSearchHistoryTool returns the search_history tool. emb may be an
// inert service (no embedder configured); in that case search degrades
// to substring matching. searcher enables cross-workspace fan-out when
// non-nil (the local workspace's own id is passed as senderWorkspaceID);
// pass a nil searcher to disable the all_workspaces option.
func NewSearchHistoryTool(messages message.Service, sessions session.Service, emb embedding.Service, searcher HistorySearcher, senderWorkspaceID string) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		SearchHistoryToolName,
		searchHistoryDescription,
		func(ctx context.Context, params SearchHistoryParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			query := strings.TrimSpace(params.Query)
			if query == "" {
				return fantasy.NewTextErrorResponse("query is required"), nil
			}

			limit := params.Limit
			if limit <= 0 {
				limit = 20
			}
			if limit > maxHistoryMatches {
				limit = maxHistoryMatches
			}

			// Cross-workspace fan-out: a session id is workspace-local, so
			// the two are mutually exclusive. Requires a wired searcher.
			if params.AllWorkspaces {
				if params.SessionID != "" {
					return fantasy.NewTextErrorResponse("all_workspaces cannot be combined with session_id"), nil
				}
				if searcher == nil {
					return fantasy.NewTextErrorResponse("cross-workspace search is unavailable in this context"), nil
				}
				res, err := searcher.SearchAllWorkspaces(ctx, senderWorkspaceID, query, string(params.Scope), params.Semantic, limit)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("search failed: %s", err)), nil
				}
				if res.Total == 0 {
					return fantasy.NewTextResponse(fmt.Sprintf("No matches for %q across workspaces", query)), nil
				}
				return fantasy.NewTextResponse(formatCrossWorkspaceHits(query, res)), nil
			}

			// Resolve the magic "current" value to the running session so
			// the agent can scope to this conversation without a prior
			// lookup. Empty stays empty (= all sessions).
			sessionID := params.SessionID
			if sessionID == currentSessionSentinel {
				sessionID = GetSessionFromContext(ctx)
				if sessionID == "" {
					return fantasy.NewTextErrorResponse("no current session in context; pass an explicit session_id"), nil
				}
			}

			res, err := historysearch.Search(ctx, messages, sessions, emb, query, historysearch.Options{
				SessionID: sessionID,
				Scope:     params.Scope,
				Semantic:  params.Semantic,
				Limit:     limit,
				Offset:    max(params.Offset, 0),
			})
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("search failed: %s", err)), nil
			}

			if res.Total == 0 {
				return fantasy.NewTextResponse(fmt.Sprintf("No matches for %q", query)), nil
			}
			if len(res.Hits) == 0 {
				return fantasy.NewTextResponse(fmt.Sprintf("No matches for %q at offset %d (only %d total)", query, res.Offset, res.Total)), nil
			}
			return fantasy.NewTextResponse(formatHistoryHits(query, res)), nil
		},
	)
}

// formatCrossWorkspaceHits renders a page of merged cross-workspace hits.
// Each hit carries its originating workspace root so the agent can tell
// which project a match came from.
func formatCrossWorkspaceHits(query string, res CrossWorkspaceResult) string {
	var b strings.Builder
	mode := "substring"
	if res.SemanticUsed {
		mode = "hybrid"
	}
	fmt.Fprintf(&b, "Matches 1-%d of %d for %q across workspaces (%s):\n\n", len(res.Hits), res.Total, query, mode)
	for i, h := range res.Hits {
		title := h.SessionTitle
		if title == "" {
			title = "(untitled)"
		}
		root := h.WorkspaceRoot
		if root == "" {
			root = "(unknown workspace)"
		}
		fmt.Fprintf(&b, "#%d [%s] %s — %s {%s}\n  workspace %s\n  session %s · message %s\n  %s\n\n",
			i+1, h.CreatedAt.Format("2006-01-02 15:04"), h.Role, title, h.Match,
			root, h.SessionID, h.MessageID, h.Snippet)
	}
	return b.String()
}

// formatHistoryHits renders a page of fused hits. Each hit shows the
// full session id and title (lining up with list_sessions), the match
// type, and a snippet, with a pagination footer.
func formatHistoryHits(query string, res embedding.SearchResult) string {
	var b strings.Builder
	first := res.Offset + 1
	last := res.Offset + len(res.Hits)
	mode := "substring"
	if res.SemanticUsed {
		mode = "hybrid"
	}
	fmt.Fprintf(&b, "Matches %d-%d of %d for %q (%s):\n\n", first, last, res.Total, query, mode)
	for _, h := range res.Hits {
		title := h.SessionTitle
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "#%d [%s] %s — %s {%s}\n  session %s · message %s\n  %s\n\n",
			h.Rank, h.CreatedAt.Format("2006-01-02 15:04"), h.Role, title, h.Match,
			h.SessionID, h.SourceID, h.Snippet)
	}
	if last < res.Total {
		fmt.Fprintf(&b, "%d more match(es). Pass offset=%d to see the next page.\n", res.Total-last, last)
	}
	return b.String()
}
