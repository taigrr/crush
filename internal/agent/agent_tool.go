package agent

import (
	"context"
	_ "embed"
	"errors"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/agent/prompt"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
)

//go:embed templates/agent_tool.md
var agentToolDescription string

type AgentParams struct {
	Prompt string `json:"prompt" description:"The task for the agent to perform"`
	// Model optionally picks the model this sub-agent runs on for this
	// call only: a configured role name (large, small, worker, or
	// a user-defined role), 'provider/model', or a bare model id. Empty
	// means the configured worker role, else the large model.
	Model string `json:"model,omitempty" description:"Optional model for this sub-agent: a role name (large, small, worker, or a configured role), 'provider/model', or a bare model id. Defaults to the configured worker role, else the large model. Use a cheaper model for mechanical search and a stronger one for hard reasoning."`
}

const (
	AgentToolName = "agent"
)

func (c *coordinator) agentTool(ctx context.Context) (fantasy.AgentTool, error) {
	agentCfg, ok := c.cfg.Config().Agents[config.AgentTask]
	if !ok {
		return nil, errors.New("task agent not configured")
	}
	prompt, err := taskPrompt(prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return nil, err
	}

	agent, err := c.buildAgent(ctx, prompt, agentCfg, true)
	if err != nil {
		return nil, err
	}
	return fantasy.NewParallelAgentTool(
		AgentToolName,
		agentToolDescription,
		func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			// Resolution failures are returned to the model as a tool
			// error (not a hard error) so it can correct the reference
			// and retry rather than aborting the whole turn.
			model, err := c.optionalModelRef(params.Model)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			return c.runSubAgent(ctx, subAgentParams{
				Agent:            agent,
				SessionID:        sessionID,
				AgentMessageID:   agentMessageID,
				ToolCallID:       call.ID,
				Prompt:           params.Prompt,
				SessionTitle:     "New Agent Session",
				Model:            model,
				UseWorkerDefault: true,
			})
		},
	), nil
}
