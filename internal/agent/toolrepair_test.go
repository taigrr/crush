package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/fantasy"
)

// strictBashParams mirrors BashParams but keeps description required, so
// the repair path for a schema that still demands it is exercised.
type strictBashParams struct {
	Description string `json:"description"`
	Command     string `json:"command"`
}

type otherParams struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

type numericDescParams struct {
	Description int    `json:"description"`
	Command     string `json:"command"`
}

func noopTool[T any](name string) fantasy.AgentTool {
	return fantasy.NewAgentTool(name, "test tool", func(context.Context, T, fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	})
}

func TestRepairToolCall(t *testing.T) {
	t.Parallel()

	available := []fantasy.AgentTool{
		noopTool[strictBashParams](tools.BashToolName),
		noopTool[otherParams]("grep"),
	}

	longCommand := strings.Repeat("x", tools.MaxDerivedBashDescriptionLen+20)

	tests := []struct {
		name      string
		toolName  string
		input     string
		tools     []fantasy.AgentTool
		wantErr   bool
		wantInput map[string]any
	}{
		{
			name:     "truncated JSON is repaired",
			toolName: tools.BashToolName,
			input:    `{"description": "List files", "command": "ls -la`,
			wantInput: map[string]any{
				"description": "List files",
				"command":     "ls -la",
			},
		},
		{
			name:     "trailing garbage is stripped",
			toolName: tools.BashToolName,
			input:    `{"description": "List files", "command": "ls"}}}garbage`,
			wantInput: map[string]any{
				"description": "List files",
				"command":     "ls",
			},
		},
		{
			name:     "empty body becomes empty object then derivation fails without command",
			toolName: tools.BashToolName,
			input:    "",
			wantErr:  true,
		},
		{
			name:      "empty body for tool without required params becomes {}",
			toolName:  "noreq",
			input:     "   ",
			tools:     []fantasy.AgentTool{noopTool[struct{}]("noreq")},
			wantInput: map[string]any{},
		},
		{
			name:     "missing description derived from command",
			toolName: tools.BashToolName,
			input:    `{"command": "  go test ./...  \necho done"}`,
			wantInput: map[string]any{
				"description": "go test ./...",
				"command":     "  go test ./...  \necho done",
			},
		},
		{
			name:     "missing description derived from long command is capped",
			toolName: tools.BashToolName,
			input:    `{"command": "` + longCommand + `"}`,
			wantInput: map[string]any{
				"description": strings.Repeat("x", tools.MaxDerivedBashDescriptionLen-1) + "…",
				"command":     longCommand,
			},
		},
		{
			name:     "missing description without command is not repaired",
			toolName: tools.BashToolName,
			input:    `{}`,
			wantErr:  true,
		},
		{
			name:     "missing description with blank command is not repaired",
			toolName: tools.BashToolName,
			input:    `{"command": "  \n  "}`,
			wantErr:  true,
		},
		{
			name:     "truncated JSON and missing description repaired together",
			toolName: tools.BashToolName,
			input:    `{"command": "git status`,
			wantInput: map[string]any{
				"description": "git status",
				"command":     "git status",
			},
		},
		{
			name:     "missing non-descriptive required param is not repaired",
			toolName: "grep",
			input:    `{"path": "."}`,
			wantErr:  true,
		},
		{
			name:     "missing command on bash is not repaired",
			toolName: tools.BashToolName,
			input:    `{"description": "List files"}`,
			wantErr:  true,
		},
		{
			name:     "unknown tool is not repaired",
			toolName: "nonexistent",
			input:    `{"command": "ls`,
			wantErr:  true,
		},
		{
			name:     "well-formed input is not repaired",
			toolName: tools.BashToolName,
			input:    `{"description": "List files", "command": "ls"}`,
			wantErr:  true,
		},
		{
			name:     "well-formed other tool input is not repaired",
			toolName: "grep",
			input:    `{"path": ".", "pattern": "foo"}`,
			wantErr:  true,
		},
		{
			name:     "non-object JSON is not repaired",
			toolName: tools.BashToolName,
			input:    `["ls"]`,
			wantErr:  true,
		},
		{
			name:     "description declared as non-string is not derived",
			toolName: tools.BashToolName,
			input:    `{"command": "ls"}`,
			tools:    []fantasy.AgentTool{noopTool[numericDescParams](tools.BashToolName)},
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			toolSet := tc.tools
			if toolSet == nil {
				toolSet = available
			}
			original := fantasy.ToolCallContent{
				ToolCallID: "call-1",
				ToolName:   tc.toolName,
				Input:      tc.input,
			}
			repaired, err := repairToolCall(context.Background(), fantasy.ToolCallRepairOptions{
				OriginalToolCall: original,
				ValidationError:  errors.New("validation failed"),
				AvailableTools:   toolSet,
			})
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, repaired)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, repaired)
			require.Equal(t, original.ToolCallID, repaired.ToolCallID)
			require.Equal(t, original.ToolName, repaired.ToolName)

			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(repaired.Input), &got))
			require.Equal(t, tc.wantInput, got)
		})
	}
}

func TestRepairToolInputEmptyBody(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "   ", "\n"} {
		input, repaired, err := repairToolInput(raw)
		require.NoError(t, err)
		require.Equal(t, "{}", repaired)
		require.Empty(t, input)
	}
}

func TestRepairToolInputWellFormedUnchanged(t *testing.T) {
	t.Parallel()

	raw := `{"a": 1, "b": "two"}`
	input, repaired, err := repairToolInput(raw)
	require.NoError(t, err)
	require.Equal(t, raw, repaired)
	require.Equal(t, map[string]any{"a": float64(1), "b": "two"}, input)
}
