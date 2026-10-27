package client

import (
	"context"
	"net/http"

	"github.com/taigrr/crush/internal/proto"
)

// MoveSession moves a session on the targeted node to another node.
func (c *Client) MoveSession(ctx context.Context, req proto.MoveSessionRequest) (proto.MoveSessionResponse, error) {
	return clusterCall[proto.MoveSessionResponse](ctx, c, http.MethodPost, "/sessions/move", req)
}

// PlanMigration asks where every local job would move.
func (c *Client) PlanMigration(ctx context.Context, req proto.MigrateRequest) (proto.MigrationPlan, error) {
	return clusterCall[proto.MigrationPlan](ctx, c, http.MethodPost, "/migrate/plan", req)
}

// ExecuteMigration runs a plan.
func (c *Client) ExecuteMigration(ctx context.Context, plan proto.MigrationPlan, resume bool) (proto.MigrationResult, error) {
	return clusterCall[proto.MigrationResult](ctx, c, http.MethodPost, "/migrate/execute", proto.MigrateExecuteRequest{Plan: plan, Resume: resume})
}

// Transfers lists session moves running from the targeted node.
func (c *Client) Transfers(ctx context.Context) ([]proto.TransferStatus, error) {
	return clusterCall[[]proto.TransferStatus](ctx, c, http.MethodGet, "/transfers", nil)
}

// IncomingTransfers lists preflights and imports the targeted node is
// serving, with the step each one is on.
func (c *Client) IncomingTransfers(ctx context.Context) ([]proto.TransferStatus, error) {
	return clusterCall[[]proto.TransferStatus](ctx, c, http.MethodGet, "/transfers/incoming", nil)
}
