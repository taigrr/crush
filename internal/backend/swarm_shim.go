package backend

import (
	"context"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

// swarmShim adapts [Backend] to the tools.SwarmBackend interface the
// swarm tool consumes. It exists solely to break the circular
// dependency: the tool lives in internal/agent/tools, the backend
// lives in internal/backend, and neither can import the other
// directly.
type swarmShim struct {
	b *Backend
}

func (s *swarmShim) LookupAddress(ctx context.Context, addr string) (tools.SwarmLookupResult, error) {
	r, err := s.b.LookupSwarmAddress(ctx, addr)
	if err != nil {
		return tools.SwarmLookupResult{}, err
	}
	return tools.SwarmLookupResult{
		WorkspaceID:   r.WorkspaceID,
		SessionID:     r.SessionID,
		Color:         r.Color,
		Animal:        r.Animal,
		WorkspaceRoot: r.WorkspaceRoot,
		Sub:           r.Sub,
	}, nil
}

func (s *swarmShim) Send(ctx context.Context, senderSessionID string, target tools.SwarmLookupResult, part message.SwarmMessage) (string, error) {
	res, err := s.b.SwarmSend(ctx, senderSessionID, SwarmLookupResult{
		WorkspaceID:   target.WorkspaceID,
		SessionID:     target.SessionID,
		Color:         target.Color,
		Animal:        target.Animal,
		WorkspaceRoot: target.WorkspaceRoot,
		Sub:           target.Sub,
	}, proto.SwarmMessage{
		Text:              part.Text,
		Body:              part.Body,
		SenderSessionID:   part.SenderSessionID,
		SenderColor:       part.SenderColor,
		SenderAnimal:      part.SenderAnimal,
		SenderWorkspaceID: part.SenderWorkspaceID,
		BTW:               part.BTW,
	})
	if err != nil {
		return "", err
	}
	return res.Delivery, nil
}

func (s *swarmShim) CreateSessionInWorkspace(ctx context.Context, workspaceID, title string, model *config.SelectedModel) (session.Session, error) {
	return s.b.CreateSwarmSession(ctx, workspaceID, title, model)
}

func (s *swarmShim) ArchiveSessionInWorkspace(ctx context.Context, workspaceID, sessionID string) error {
	return s.b.ArchiveWorkspaceSession(ctx, workspaceID, sessionID)
}

func (s *swarmShim) ResolveWorkspaceByPath(ctx context.Context, path string) (string, bool, error) {
	return s.b.ResolveWorkspaceByPath(ctx, path)
}

func (s *swarmShim) RenameSession(ctx context.Context, target tools.SwarmLookupResult, title string) error {
	return s.b.RenameWorkspaceSession(ctx, SwarmLookupResult{
		WorkspaceID:   target.WorkspaceID,
		SessionID:     target.SessionID,
		Color:         target.Color,
		Animal:        target.Animal,
		WorkspaceRoot: target.WorkspaceRoot,
		Sub:           target.Sub,
	}, title)
}

func (s *swarmShim) CreateSessionInWorkspaceAtPath(ctx context.Context, path, title string, model *config.SelectedModel) (string, session.Session, error) {
	return s.b.CreateSwarmSessionAtPath(ctx, path, title, model)
}

// SearchAllWorkspaces fans a history query out over every known
// workspace (attached and registry-detached) and returns the merged,
// per-session result mapped into the tool-facing type so the tools
// package needn't import backend/proto.
func (s *swarmShim) SearchAllWorkspaces(ctx context.Context, requestingWorkspaceID, query, scope string, semantic *bool, limit int) (tools.CrossWorkspaceResult, error) {
	res, err := s.b.searchAllWorkspaces(ctx, requestingWorkspaceID, proto.SearchHistoryParams{
		Query:         query,
		Scope:         scope,
		Semantic:      semantic,
		Limit:         limit,
		AllWorkspaces: true,
	})
	if err != nil {
		return tools.CrossWorkspaceResult{}, err
	}
	hits := make([]tools.CrossWorkspaceHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hits = append(hits, tools.CrossWorkspaceHit{
			SessionID:     h.SessionID,
			SessionTitle:  h.SessionTitle,
			WorkspaceRoot: h.WorkspaceRoot,
			Match:         h.Match,
			Snippet:       h.Snippet,
			MessageID:     h.MessageID,
			Role:          h.Role,
			CreatedAt:     h.CreatedAt,
		})
	}
	return tools.CrossWorkspaceResult{
		Hits:         hits,
		Total:        res.Total,
		SemanticUsed: res.SemanticUsed,
	}, nil
}
