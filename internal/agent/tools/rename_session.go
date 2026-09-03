package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/taigrr/fantasy"
)

// RenameSessionToolName is the tool identifier exposed to the model.
const RenameSessionToolName = "rename_session"

//go:embed rename_session.md
var renameSessionToolDescription string

// RenameSessionParams is the JSON schema exposed to the model.
type RenameSessionParams struct {
	// Address is the target session: "color-animal",
	// "color-animal-shorthash", or a raw session id. It is resolved
	// across every running workspace, so any session (not just the
	// caller's) can be renamed.
	Address string `json:"address" description:"Target session address: color-animal, color-animal-shorthash, or a raw session id. Resolved across all workspaces."`
	// Title is the new session title.
	Title string `json:"title" description:"The new title for the session."`
}

// NewRenameSessionTool builds the fantasy tool wrapper. It renames any
// session across any running workspace, resolving the address through
// the same cross-workspace lookup the swarm tool uses.
func NewRenameSessionTool(be SwarmBackend) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		RenameSessionToolName,
		renameSessionToolDescription,
		func(ctx context.Context, params RenameSessionParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return runRenameSession(ctx, be, params)
		},
	)
}

func runRenameSession(ctx context.Context, be SwarmBackend, params RenameSessionParams) (fantasy.ToolResponse, error) {
	address := strings.TrimSpace(params.Address)
	if address == "" {
		return fantasy.NewTextErrorResponse("rename_session: address is required"), nil
	}
	title := strings.TrimSpace(params.Title)
	if title == "" {
		return fantasy.NewTextErrorResponse("rename_session: title is required"), nil
	}

	target, err := be.LookupAddress(ctx, address)
	if err != nil {
		if isContextErr(err) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("rename_session: %s", err)), nil
	}
	if target.Sub {
		return fantasy.NewTextErrorResponse("rename_session: target is a sub-agent session (not renamable)"), nil
	}

	if err := be.RenameSession(ctx, target, title); err != nil {
		if isContextErr(err) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("rename_session: failed to rename: %s", err)), nil
	}

	addr := address
	if target.Color != "" && target.Animal != "" {
		addr = target.Color + "-" + target.Animal
	}
	return fantasy.NewTextResponse(fmt.Sprintf("Renamed session %s to %q", addr, title)), nil
}
