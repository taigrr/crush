package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/taigrr/crush/internal/checkpoint"
	"github.com/taigrr/crush/internal/fork"
	"github.com/taigrr/crush/internal/worktree"
)

// SnapshotsEnabled returns whether snapshots are enabled for a workspace.
func (c *Client) SnapshotsEnabled(ctx context.Context, workspaceID string) (bool, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/snapshots/enabled", workspaceID), nil, nil)
	if err != nil {
		return false, fmt.Errorf("failed to check snapshots enabled: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to check snapshots enabled: status code %d", rsp.StatusCode)
	}
	var result struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(rsp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode response: %w", err)
	}
	return result.Enabled, nil
}

// ListSnapshots returns all snapshots for a session.
func (c *Client) ListSnapshots(ctx context.Context, workspaceID, sessionID string) ([]*checkpoint.Snapshot, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/snapshots", workspaceID, sessionID), nil, nil)
	return decodeResponse[[]*checkpoint.Snapshot](rsp, err, "list snapshots")
}

// GetSnapshot retrieves a snapshot by ID.
func (c *Client) GetSnapshot(ctx context.Context, workspaceID, snapshotID string) (*checkpoint.Snapshot, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/snapshots/%s", workspaceID, snapshotID), nil, nil)
	return decodeResponse[*checkpoint.Snapshot](rsp, err, "get snapshot")
}

// GetSnapshotByMessage retrieves a snapshot by message ID.
func (c *Client) GetSnapshotByMessage(ctx context.Context, workspaceID, messageID string) (*checkpoint.Snapshot, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/messages/%s/snapshot", workspaceID, messageID), nil, nil)
	return decodeResponse[*checkpoint.Snapshot](rsp, err, "get snapshot by message")
}

// RestoreSnapshot restores a workspace to a snapshot.
func (c *Client) RestoreSnapshot(ctx context.Context, workspaceID, snapshotID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/snapshots/%s/restore", workspaceID, snapshotID), nil, nil, nil)
	return checkResponse(rsp, err, "restore snapshot")
}

// DiffFromCurrentSnapshot returns the diff from current filesystem to a snapshot.
func (c *Client) DiffFromCurrentSnapshot(ctx context.Context, workspaceID, snapshotID string) (string, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/snapshots/%s/diff", workspaceID, snapshotID), nil, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get snapshot diff: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get snapshot diff: status code %d", rsp.StatusCode)
	}
	var result struct {
		Diff string `json:"diff"`
	}
	if err := json.NewDecoder(rsp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode diff: %w", err)
	}
	return result.Diff, nil
}

// SnapshotGC runs garbage collection on snapshots.
func (c *Client) SnapshotGC(ctx context.Context, workspaceID string) (int64, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/snapshots/gc", workspaceID), nil, nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to run snapshot GC: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to run snapshot GC: status code %d", rsp.StatusCode)
	}
	var result struct {
		BytesFreed int64 `json:"bytes_freed"`
	}
	if err := json.NewDecoder(rsp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode GC result: %w", err)
	}
	return result.BytesFreed, nil
}

// SnapshotStats returns snapshot storage statistics.
func (c *Client) SnapshotStats(ctx context.Context, workspaceID string) (*checkpoint.Stats, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/snapshots/stats", workspaceID), nil, nil)
	return decodeResponse[*checkpoint.Stats](rsp, err, "get snapshot stats")
}

// WorktreesEnabled returns whether worktrees are enabled for a workspace.
func (c *Client) WorktreesEnabled(ctx context.Context, workspaceID string) (bool, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/worktrees/enabled", workspaceID), nil, nil)
	if err != nil {
		return false, fmt.Errorf("failed to check worktrees enabled: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to check worktrees enabled: status code %d", rsp.StatusCode)
	}
	var result struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(rsp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode response: %w", err)
	}
	return result.Enabled, nil
}

// ListWorktrees returns all worktrees for a session.
func (c *Client) ListWorktrees(ctx context.Context, workspaceID, sessionID string) ([]*worktree.Worktree, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/worktrees", workspaceID, sessionID), nil, nil)
	return decodeResponse[[]*worktree.Worktree](rsp, err, "list worktrees")
}

// ListAllWorktrees returns all worktrees for a workspace.
func (c *Client) ListAllWorktrees(ctx context.Context, workspaceID string) ([]*worktree.Worktree, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/worktrees", workspaceID), nil, nil)
	return decodeResponse[[]*worktree.Worktree](rsp, err, "list all worktrees")
}

// GetWorktree retrieves a worktree by ID.
func (c *Client) GetWorktree(ctx context.Context, workspaceID, worktreeID string) (*worktree.Worktree, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/worktrees/%s", workspaceID, worktreeID), nil, nil)
	return decodeResponse[*worktree.Worktree](rsp, err, "get worktree")
}

// GetActiveWorktree retrieves the active worktree for a session.
func (c *Client) GetActiveWorktree(ctx context.Context, workspaceID, sessionID string) (*worktree.Worktree, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/worktrees/active", workspaceID, sessionID), nil, nil)
	return decodeResponse[*worktree.Worktree](rsp, err, "get active worktree")
}

// CreateWorktree creates a new worktree.
func (c *Client) CreateWorktree(ctx context.Context, workspaceID, sessionID, name, fromSnapshotID string) (*worktree.Worktree, error) {
	body := struct {
		Name           string `json:"name"`
		FromSnapshotID string `json:"from_snapshot_id"`
	}{
		Name:           name,
		FromSnapshotID: fromSnapshotID,
	}
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/worktrees", workspaceID, sessionID), nil, jsonBody(body), jsonHeader())
	return decodeResponse[*worktree.Worktree](rsp, err, "create worktree")
}

// SwitchWorktree switches to a different worktree.
func (c *Client) SwitchWorktree(ctx context.Context, workspaceID, sessionID, worktreeID string) error {
	q := c.clientQuery()
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/worktrees/%s/switch", workspaceID, sessionID, worktreeID), q, nil, nil)
	return checkResponse(rsp, err, "switch worktree")
}

// DeleteWorktree deletes a worktree.
func (c *Client) DeleteWorktree(ctx context.Context, workspaceID, worktreeID string) error {
	rsp, err := c.delete(ctx, fmt.Sprintf("/workspaces/%s/worktrees/%s", workspaceID, worktreeID), nil, nil)
	return checkResponse(rsp, err, "delete worktree")
}

// MergeWorktree merges or rebases a worktree onto a target branch.
func (c *Client) MergeWorktree(ctx context.Context, workspaceID, worktreeID, targetBranch string, rebase bool) error {
	body := struct {
		TargetBranch string `json:"target_branch"`
		Rebase       bool   `json:"rebase"`
	}{
		TargetBranch: targetBranch,
		Rebase:       rebase,
	}
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/worktrees/%s/merge", workspaceID, worktreeID), nil, jsonBody(body), jsonHeader())
	return checkResponse(rsp, err, "merge worktree")
}

// ListGitBranches returns all git branches in the workspace.
func (c *Client) ListGitBranches(ctx context.Context, workspaceID string) ([]string, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/git/branches", workspaceID), nil, nil)
	return decodeResponse[[]string](rsp, err, "list git branches")
}

// ForkConversation creates a fork of a conversation.
func (c *Client) ForkConversation(ctx context.Context, workspaceID string, params fork.ForkParams) (*fork.ForkResult, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/fork", workspaceID), nil, jsonBody(params), jsonHeader())
	return decodeResponse[*fork.ForkResult](rsp, err, "fork conversation")
}
