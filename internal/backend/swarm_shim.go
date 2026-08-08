package backend

import (
	"context"

	"github.com/taigrr/crush/internal/agent/tools"
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
		WorkspaceID: r.WorkspaceID,
		SessionID:   r.SessionID,
		Color:       r.Color,
		Animal:      r.Animal,
		Sub:         r.Sub,
	}, nil
}

func (s *swarmShim) Send(ctx context.Context, senderSessionID string, target tools.SwarmLookupResult, part message.SwarmMessage) (string, error) {
	res, err := s.b.SwarmSend(ctx, senderSessionID, SwarmLookupResult{
		WorkspaceID: target.WorkspaceID,
		SessionID:   target.SessionID,
		Color:       target.Color,
		Animal:      target.Animal,
		Sub:         target.Sub,
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

func (s *swarmShim) CreateSessionInWorkspace(ctx context.Context, workspaceID, title string) (session.Session, error) {
	return s.b.CreateSwarmSession(ctx, workspaceID, title)
}

func (s *swarmShim) ArchiveSessionInWorkspace(ctx context.Context, workspaceID, sessionID string) error {
	return s.b.ArchiveWorkspaceSession(ctx, workspaceID, sessionID)
}

func (s *swarmShim) ResolveWorkspaceByPath(ctx context.Context, path string) (string, bool, error) {
	return s.b.ResolveWorkspaceByPath(ctx, path)
}

func (s *swarmShim) CreateSessionInWorkspaceAtPath(ctx context.Context, path, title string) (string, session.Session, error) {
	return s.b.CreateSwarmSessionAtPath(ctx, path, title)
}
