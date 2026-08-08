package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/taigrr/fantasy"
)

// WorkspaceLookupToolName is the tool identifier exposed to the model.
const WorkspaceLookupToolName = "workspace_lookup"

//go:embed workspace_lookup.md
var workspaceLookupToolDescription string

// WorkspaceLookupParams is the JSON schema exposed to the model.
type WorkspaceLookupParams struct {
	// Path is the directory to resolve to a running workspace id.
	Path string `json:"path" description:"Directory path to resolve to a running workspace id. Any directory within the project (git worktree, subdirectory) resolves to the same workspace."`
}

// NewWorkspaceLookupTool builds the fantasy tool wrapper. It resolves a
// directory path to the ID of the running workspace rooted there,
// which callers can then pass to the swarm tool (e.g. address='new'
// with workspace_id).
func NewWorkspaceLookupTool(be SwarmBackend) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		WorkspaceLookupToolName,
		workspaceLookupToolDescription,
		func(ctx context.Context, params WorkspaceLookupParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return runWorkspaceLookup(ctx, be, params)
		},
	)
}

func runWorkspaceLookup(ctx context.Context, be SwarmBackend, params WorkspaceLookupParams) (fantasy.ToolResponse, error) {
	path := strings.TrimSpace(params.Path)
	if path == "" {
		return fantasy.NewTextErrorResponse("workspace_lookup: path is required"), nil
	}
	id, found, err := be.ResolveWorkspaceByPath(ctx, path)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("workspace_lookup: failed to resolve path: %s", err)), nil
	}
	if !found {
		return fantasy.NewTextResponse(fmt.Sprintf("No running workspace at that path: %s", path)), nil
	}
	return fantasy.NewTextResponse(fmt.Sprintf("workspace_id=%s (path=%s)", id, path)), nil
}
