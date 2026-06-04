package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/taigrr/crush/internal/milestone"
)

// ListMilestones retrieves all milestones for a session.
func (c *Client) ListMilestones(ctx context.Context, workspaceID, sessionID string) ([]milestone.Milestone, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/milestones", workspaceID, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list milestones: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list milestones: status code %d", rsp.StatusCode)
	}
	var milestones []milestone.Milestone
	if err := json.NewDecoder(rsp.Body).Decode(&milestones); err != nil {
		return nil, fmt.Errorf("failed to decode milestones: %w", err)
	}
	return milestones, nil
}
