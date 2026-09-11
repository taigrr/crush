package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/jsonrepair"
)

// errToolCallNotRepairable is returned by repairToolCall when the call
// cannot be fixed deterministically. fantasy treats a non-nil error as
// "no repair" and falls back to reporting the original validation error
// to the model.
var errToolCallNotRepairable = errors.New("tool call not repairable")

// repairToolCall is the fantasy.RepairToolCallFunction wired into the
// coder agent. It never invents semantic arguments; it only fixes the
// two failure classes a model produces mechanically:
//
//  1. Malformed argument JSON (truncated or with trailing garbage), run
//     through jsonrepair. An empty body is treated as "{}".
//  2. A missing required parameter whose value is purely descriptive
//     and derivable from the rest of the input (today: `description`,
//     synthesized from `command` or the tool name).
//
// Anything else — a missing path, an unknown tool — is left for the
// model to retry with the original validation error.
func repairToolCall(_ context.Context, opts fantasy.ToolCallRepairOptions) (*fantasy.ToolCallContent, error) {
	call := opts.OriginalToolCall
	var tool fantasy.AgentTool
	for _, t := range opts.AvailableTools {
		if t.Info().Name == call.ToolName {
			tool = t
			break
		}
	}
	if tool == nil {
		return nil, errToolCallNotRepairable
	}

	input, changed, err := repairToolInputJSON(call.Input)
	if err != nil {
		return nil, err
	}

	info := tool.Info()
	for _, key := range info.Required {
		if _, ok := input[key]; ok {
			continue
		}
		value, ok := defaultForMissingParam(info, key, input)
		if !ok {
			return nil, errToolCallNotRepairable
		}
		input[key] = value
		changed = true
	}
	if !changed {
		return nil, errToolCallNotRepairable
	}

	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal repaired tool input: %w", err)
	}
	slog.Debug("Repaired tool call arguments",
		"tool", call.ToolName,
		"tool_call_id", call.ToolCallID,
		"validation_error", opts.ValidationError,
	)
	repaired := call
	repaired.Input = string(raw)
	return &repaired, nil
}

// repairToolInputJSON parses raw as a JSON object, falling back to
// jsonrepair when it is malformed. It reports whether the text had to
// be changed to parse.
func repairToolInputJSON(raw string) (map[string]any, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}, true, nil
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(trimmed), &input); err == nil {
		if input == nil {
			input = map[string]any{}
		}
		return input, false, nil
	}
	fixed, err := jsonrepair.RepairJSON(trimmed)
	if err != nil || fixed == "" {
		return nil, false, errToolCallNotRepairable
	}
	if err := json.Unmarshal([]byte(fixed), &input); err != nil || input == nil {
		return nil, false, errToolCallNotRepairable
	}
	return input, true, nil
}

// defaultForMissingParam returns a safe value for a required parameter
// the model omitted, or false when no value can be derived without
// guessing at intent. Only descriptive, human-facing fields qualify.
func defaultForMissingParam(info fantasy.ToolInfo, key string, input map[string]any) (any, bool) {
	if key != "description" || !paramIsString(info, key) {
		return nil, false
	}
	if command, ok := input["command"].(string); ok && strings.TrimSpace(command) != "" {
		return tools.DefaultBashDescription(command), true
	}
	return info.Name + " call", true
}

// paramIsString reports whether the tool schema declares key as a
// string (or leaves its type unspecified). A typed non-string
// parameter is never defaulted.
func paramIsString(info fantasy.ToolInfo, key string) bool {
	prop, ok := info.Parameters[key]
	if !ok {
		return true
	}
	p, ok := prop.(map[string]any)
	if !ok {
		return false
	}
	t, ok := p["type"]
	if !ok {
		return true
	}
	switch tv := t.(type) {
	case string:
		return tv == "string"
	case []any:
		for _, alt := range tv {
			if s, ok := alt.(string); ok && s == "string" {
				return true
			}
		}
	}
	return false
}
