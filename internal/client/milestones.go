package client

import (
	"context"
	"fmt"

	"github.com/taigrr/crush/internal/milestone"
)

// ListMilestones retrieves all milestones for a session.
func (c *Client) ListMilestones(ctx context.Context, workspaceID, sessionID string) ([]milestone.Milestone, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/milestones", workspaceID, sessionID), nil, nil)
	return decodeResponse[[]milestone.Milestone](rsp, err, "list milestones")
}
