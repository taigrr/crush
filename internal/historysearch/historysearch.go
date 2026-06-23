// Package historysearch ties together message loading and the embedding
// service to provide hybrid (substring + semantic) search over a
// project's conversation history. It is the single seam shared by the
// search_history agent tool, the `crush search` CLI, and the TUI search
// dialog so ranking is identical across all surfaces.
package historysearch

import (
	"context"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

// Scope selects which message roles are searched.
type Scope string

const (
	// ScopeUser searches only user/shell messages (what the user typed).
	ScopeUser Scope = "user"
	// ScopeAll searches all messages including assistant replies.
	ScopeAll Scope = "all"
)

// Options configures a search.
type Options struct {
	// SessionID, when non-empty, limits the search to one session. The
	// caller is responsible for resolving any "current" sentinel first.
	SessionID string
	// Scope selects the role filter. Empty defaults to ScopeUser.
	Scope Scope
	// Semantic overrides the embedder's hybrid default for this call.
	Semantic *bool
	Limit    int
	Offset   int
}

// Search loads the candidate messages per the options, builds documents,
// and delegates ranking to the embedding service (which fuses substring
// and semantic signals, degrading to substring-only when embeddings are
// unavailable).
func Search(
	ctx context.Context,
	messages message.Service,
	sessions session.Service,
	emb embedding.Service,
	query string,
	opts Options,
) (embedding.SearchResult, error) {
	titles, err := sessionTitles(ctx, sessions)
	if err != nil {
		return embedding.SearchResult{}, err
	}

	all := opts.Scope == ScopeAll
	var msgs []message.Message
	switch {
	case opts.SessionID != "" && all:
		msgs, err = messages.List(ctx, opts.SessionID)
	case opts.SessionID != "":
		msgs, err = messages.ListUserMessages(ctx, opts.SessionID)
	case all:
		msgs, err = messages.ListAllMessages(ctx)
	default:
		msgs, err = messages.ListAllUserMessages(ctx)
	}
	if err != nil {
		return embedding.SearchResult{}, err
	}

	docs := make([]embedding.Document, 0, len(msgs))
	for _, m := range msgs {
		body := MessageBody(m)
		if body == "" {
			continue
		}
		docs = append(docs, embedding.Document{
			SourceType:   embedding.SourceMessage,
			SourceID:     m.ID,
			SessionID:    m.SessionID,
			SessionTitle: titles[m.SessionID],
			Role:         string(m.Role),
			CreatedAt:    time.Unix(m.CreatedAt, 0),
			Body:         body,
		})
	}

	return emb.Search(ctx, query, docs, embedding.SearchOptions{
		Limit:    opts.Limit,
		Offset:   opts.Offset,
		Semantic: opts.Semantic,
	})
}

func sessionTitles(ctx context.Context, sessions session.Service) (map[string]string, error) {
	all, err := sessions.List(ctx)
	if err != nil {
		return nil, err
	}
	titles := make(map[string]string, len(all))
	for _, s := range all {
		titles[s.ID] = s.Title
	}
	return titles, nil
}

// MessageBody concatenates the human-readable parts of a message into a
// single search/embed target, skipping binary/image parts.
func MessageBody(m message.Message) string {
	var parts []string
	if t := m.Content().Text; t != "" {
		parts = append(parts, t)
	}
	if r := m.ReasoningContent().Thinking; r != "" {
		parts = append(parts, r)
	}
	return strings.Join(parts, "\n")
}
