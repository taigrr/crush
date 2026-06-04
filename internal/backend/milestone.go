package backend

import (
	"context"

	"github.com/taigrr/crush/internal/milestone"
)

// ListMilestones returns all milestones for a session.
func (b *Backend) ListMilestones(ctx context.Context, workspaceID, sessionID string) ([]milestone.Milestone, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	return ws.Milestones.List(ctx, sessionID)
}
