package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/jsonrepair"
)

// derivableParams lists, per tool, the required parameters whose value is
// purely descriptive and can be derived deterministically from another
// parameter without inventing semantics. Each entry maps the missing
// parameter name to a function that derives it from the parsed input, or
// returns ok=false when it cannot.
var derivableParams = map[string]map[string]func(input map[string]any) (string, bool){
	tools.BashToolName: {
		"description": func(input map[string]any) (string, bool) {
			command, _ := input["command"].(string)
			desc := tools.DeriveBashDescription(command)
			return desc, desc != ""
		},
	},
}

// repairToolCall is the deterministic fantasy.RepairToolCallFunction used by
// the agent. It never invents semantic arguments. It only:
//
//  1. Repairs malformed or truncated argument JSON via jsonrepair (an empty
//     body becomes "{}").
//  2. Fills a missing required parameter whose value is purely descriptive
//     and derivable from the other arguments (currently only the bash tool's
//     "description", derived from the first line of "command"), and only if
//     the tool schema declares that parameter as a string.
//
// Anything else returns an error so fantasy falls back to the original
// validation error.
func repairToolCall(_ context.Context, options fantasy.ToolCallRepairOptions) (*fantasy.ToolCallContent, error) {
	call := options.OriginalToolCall

	var tool fantasy.AgentTool
	for _, candidate := range options.AvailableTools {
		if candidate.Info().Name == call.ToolName {
			tool = candidate
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("tool not found: %s", call.ToolName)
	}

	input, repairedJSON, err := repairToolInput(call.Input)
	if err != nil {
		return nil, err
	}

	info := tool.Info()
	changed := repairedJSON != call.Input
	for _, required := range info.Required {
		if _, exists := input[required]; exists {
			continue
		}
		derive, ok := derivableParams[call.ToolName][required]
		if !ok {
			return nil, fmt.Errorf("missing required parameter %q cannot be derived", required)
		}
		if !schemaDeclaresString(info.Parameters, required) {
			return nil, fmt.Errorf("parameter %q is not declared as a string", required)
		}
		value, ok := derive(input)
		if !ok {
			return nil, fmt.Errorf("missing required parameter %q could not be derived", required)
		}
		input[required] = value
		changed = true
	}

	if !changed {
		return nil, errors.New("tool call input is well-formed; nothing to repair")
	}

	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to encode repaired input: %w", err)
	}

	repaired := call
	repaired.Input = string(encoded)
	return &repaired, nil
}

// repairToolInput parses raw tool call arguments, repairing malformed JSON
// when needed. It returns the parsed object and the (possibly repaired)
// JSON text. Inputs that do not represent a JSON object are rejected.
func repairToolInput(raw string) (map[string]any, string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, "{}", nil
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err == nil {
		if input == nil {
			input = map[string]any{}
		}
		return input, raw, nil
	}

	repaired, err := jsonrepair.RepairJSON(raw)
	if err != nil {
		return nil, "", fmt.Errorf("failed to repair tool input JSON: %w", err)
	}
	if strings.TrimSpace(repaired) == "" {
		return map[string]any{}, "{}", nil
	}
	input = nil
	if err := json.Unmarshal([]byte(repaired), &input); err != nil {
		return nil, "", fmt.Errorf("repaired tool input is not a JSON object: %w", err)
	}
	if input == nil {
		input = map[string]any{}
	}
	return input, repaired, nil
}

// schemaDeclaresString reports whether the tool parameter schema declares
// the named parameter with type "string".
func schemaDeclaresString(parameters map[string]any, name string) bool {
	prop, ok := parameters[name].(map[string]any)
	if !ok {
		return false
	}
	typ, _ := prop["type"].(string)
	return typ == "string"
}
