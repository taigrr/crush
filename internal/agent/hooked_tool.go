package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/hooks"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/sound"
	"github.com/taigrr/fantasy"
	"github.com/tidwall/sjson"
)

// hookedTool wraps a fantasy.AgentTool to run PreToolUse hooks before
// delegating to the inner tool.
type hookedTool struct {
	inner  fantasy.AgentTool
	runner *hooks.Runner
	cfg    *config.ConfigStore
}

func newHookedTool(inner fantasy.AgentTool, runner *hooks.Runner, cfg *config.ConfigStore) *hookedTool {
	return &hookedTool{inner: inner, runner: runner, cfg: cfg}
}

// wrapToolsWithHooks returns a tool slice with each entry wrapped in a
// hookedTool. Returns the original slice unchanged when runner is nil or
// when isSubAgent is true — sub-agents never fire hooks, the top-level
// invocation of the sub-agent tool itself is wrapped on the caller's side.
func wrapToolsWithHooks(tools []fantasy.AgentTool, runner *hooks.Runner, cfg *config.ConfigStore, isSubAgent bool) []fantasy.AgentTool {
	if runner == nil || isSubAgent {
		return tools
	}
	out := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		out[i] = newHookedTool(tool, runner, cfg)
	}
	return out
}

func (h *hookedTool) Info() fantasy.ToolInfo {
	return h.inner.Info()
}

func (h *hookedTool) ProviderOptions() fantasy.ProviderOptions {
	return h.inner.ProviderOptions()
}

func (h *hookedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	h.inner.SetProviderOptions(opts)
}

func (h *hookedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	sessionID := tools.GetSessionFromContext(ctx)
	result, err := h.runner.Run(ctx, hooks.EventPreToolUse, sessionID, call.Name, call.Input)
	if err != nil {
		slog.Warn("Hook execution error, proceeding with tool call",
			"tool", call.Name, "error", err)
	}

	if result.Decision == hooks.DecisionDeny || result.Halt {
		reason := fmt.Sprintf("Tool call blocked by hook. Reason: %s", result.Reason)
		if result.Halt {
			reason = fmt.Sprintf("Turn halted by hook. Reason: %s", result.Reason)
		}
		resp := fantasy.NewTextErrorResponse(reason)
		// Halt ends the whole turn; a plain deny only blocks this tool
		// call so the model can see the error and try something else.
		resp.StopTurn = result.Halt
		resp.Metadata = hookMetadataJSON(result)
		return resp, nil
	}

	if result.UpdatedInput != "" {
		call.Input = result.UpdatedInput
	}

	// An explicit allow from a hook pre-approves the permission prompt for
	// this tool call. Deny is already handled above; silence falls through
	// to the normal permission flow.
	if result.Decision == hooks.DecisionAllow {
		ctx = permission.WithHookApproval(ctx, call.ID)
	}

	resp, err := h.inner.Run(ctx, call)
	if err != nil || resp.IsError {
		// A genuine tool failure (transport/system error, or a tool
		// that returned an error response). Distinct from a hook block
		// above, which is not a tool failure. Best-effort chime.
		h.playToolError()
	}
	if err != nil {
		return resp, err
	}

	if result.Context != "" {
		if resp.Content != "" {
			resp.Content += "\n"
		}
		resp.Content += result.Context
	}

	resp.Metadata = mergeHookMetadata(resp.Metadata, result)
	return resp, nil
}

// playToolError plays the tool-error notification sound. Best-effort and
// asynchronous: a no-op when config is unavailable, sound is disabled
// (globally or for the tool-error event), or the user has configured a
// ToolError hook — the hook then owns the notification and the built-in
// sound defers to it.
func (h *hookedTool) playToolError() {
	if h.cfg == nil {
		return
	}
	cfg := h.cfg.Config()
	if cfg == nil || !cfg.Options.SoundEnabled(sound.ToolError) {
		return
	}
	if len(cfg.Hooks[hooks.EventToolError]) > 0 {
		return
	}
	sound.PlayAsync(sound.ToolError, cfg.Options.SoundPath(sound.ToolError))
}

// buildHookMetadata creates a HookMetadata from an AggregateResult.
func buildHookMetadata(result hooks.AggregateResult) hooks.HookMetadata {
	return hooks.HookMetadata{
		HookCount:    result.HookCount,
		Decision:     result.Decision.String(),
		Halt:         result.Halt,
		Reason:       result.Reason,
		InputRewrite: result.UpdatedInput != "",
		Hooks:        result.Hooks,
	}
}

// hookMetadataJSON builds a JSON string containing only the hook metadata.
func hookMetadataJSON(result hooks.AggregateResult) string {
	meta := buildHookMetadata(result)
	data, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return `{"hook":` + string(data) + `}`
}

// mergeHookMetadata injects hook metadata into existing tool metadata.
func mergeHookMetadata(existing string, result hooks.AggregateResult) string {
	if result.HookCount == 0 {
		return existing
	}
	meta := buildHookMetadata(result)
	data, err := json.Marshal(meta)
	if err != nil {
		return existing
	}
	if existing == "" {
		existing = "{}"
	}
	merged, err := sjson.SetRaw(existing, "hook", string(data))
	if err != nil {
		return existing
	}
	return merged
}
