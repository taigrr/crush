package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/fantasy"
)

// repairTestTool is a minimal fantasy.AgentTool whose schema the tests
// control directly, so we can exercise MCP-style hand-written schemas as
// well as the reflected ones fantasy.NewAgentTool produces.
type repairTestTool struct {
	name       string
	parameters map[string]any
	required   []string
	run        func(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error)
	opts       fantasy.ProviderOptions
}

func (t *repairTestTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: t.name, Parameters: t.parameters, Required: t.required}
}

func (t *repairTestTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if t.run != nil {
		return t.run(ctx, call)
	}
	return fantasy.NewTextResponse("ok"), nil
}

func (t *repairTestTool) SetProviderOptions(o fantasy.ProviderOptions) { t.opts = o }
func (t *repairTestTool) ProviderOptions() fantasy.ProviderOptions     { return t.opts }

type descriptionAndCommand struct {
	Description string `json:"description"`
	Command     string `json:"command"`
}

func requireDescriptionAndCommand(t *testing.T, input string) descriptionAndCommand {
	t.Helper()
	var got descriptionAndCommand
	require.NoError(t, json.Unmarshal([]byte(input), &got))
	return got
}

func repairWith(t *testing.T, tool fantasy.AgentTool, input string, validationErr error) (*fantasy.ToolCallContent, error) {
	t.Helper()
	return repairToolCall(context.Background(), fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{ToolCallID: "call-1", ToolName: tool.Info().Name, Input: input},
		ValidationError:  validationErr,
		AvailableTools:   []fantasy.AgentTool{tool},
	})
}

func TestRepairToolCall_FillsMissingDescriptionFromCommand(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{
		name: "bash",
		parameters: map[string]any{
			"description": map[string]any{"type": "string"},
			"command":     map[string]any{"type": "string"},
		},
		required: []string{"description", "command"},
	}

	repaired, err := repairWith(t, tool, `{"command": "go test ./...\necho done"}`, errMissing("description"))
	require.NoError(t, err)
	require.NotNil(t, repaired)
	require.Equal(t, "call-1", repaired.ToolCallID)
	require.Equal(t, "bash", repaired.ToolName)

	got := requireDescriptionAndCommand(t, repaired.Input)
	require.Equal(t, "go test ./...", got.Description)
	require.Equal(t, "go test ./...\necho done", got.Command, "existing arguments must be preserved verbatim")
}

func TestRepairToolCall_FillsMissingDescriptionWithoutCommand(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{
		name:       "mcp_thing_do",
		parameters: map[string]any{"description": map[string]any{"type": "string"}},
		required:   []string{"description"},
	}

	repaired, err := repairWith(t, tool, `{}`, errMissing("description"))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(repaired.Input), &got))
	require.Equal(t, "mcp_thing_do call", got["description"])
}

func TestRepairToolCall_RefusesToInventSemanticParams(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{
		name: "view",
		parameters: map[string]any{
			"file_path": map[string]any{"type": "string"},
		},
		required: []string{"file_path"},
	}

	repaired, err := repairWith(t, tool, `{"offset": 10}`, errMissing("file_path"))
	require.ErrorIs(t, err, errToolCallNotRepairable)
	require.Nil(t, repaired)
}

func TestRepairToolCall_RefusesNonStringDescription(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{
		name:       "weird",
		parameters: map[string]any{"description": map[string]any{"type": "object"}},
		required:   []string{"description"},
	}

	repaired, err := repairWith(t, tool, `{}`, errMissing("description"))
	require.ErrorIs(t, err, errToolCallNotRepairable)
	require.Nil(t, repaired)
}

func TestRepairToolCall_RepairsTruncatedJSON(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{
		name: "bash",
		parameters: map[string]any{
			"command": map[string]any{"type": "string"},
		},
		required: []string{"command"},
	}

	repaired, err := repairWith(t, tool, `{"command": "echo hi"`, errInvalidJSON())
	require.NoError(t, err)
	got := requireDescriptionAndCommand(t, repaired.Input)
	require.Equal(t, "echo hi", got.Command)
}

func TestRepairToolCall_EmptyInputBecomesEmptyObject(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{name: "crush_info", parameters: map[string]any{}}

	repaired, err := repairWith(t, tool, ``, errInvalidJSON())
	require.NoError(t, err)
	require.JSONEq(t, `{}`, repaired.Input)
}

func TestRepairToolCall_UnknownToolIsNotRepaired(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{name: "bash", required: []string{"command"}}

	repaired, err := repairToolCall(context.Background(), fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{ToolName: "nope", Input: `{}`},
		AvailableTools:   []fantasy.AgentTool{tool},
	})
	require.ErrorIs(t, err, errToolCallNotRepairable)
	require.Nil(t, repaired)
}

func TestRepairToolCall_ValidInputIsLeftAlone(t *testing.T) {
	t.Parallel()
	tool := &repairTestTool{name: "bash", required: []string{"command"}}

	repaired, err := repairWith(t, tool, `{"command": "ls"}`, nil)
	require.ErrorIs(t, err, errToolCallNotRepairable)
	require.Nil(t, repaired)
}

// TestRepairToolCall_EndToEndThroughFantasy proves fantasy actually
// consults the hook: a model that emits bash without a description
// still gets the tool executed instead of an "invalid tool call" result.
func TestRepairToolCall_EndToEndThroughFantasy(t *testing.T) {
	t.Parallel()
	var executedWith string
	tool := &repairTestTool{
		name: "bash",
		parameters: map[string]any{
			"description": map[string]any{"type": "string"},
			"command":     map[string]any{"type": "string"},
		},
		required: []string{"description", "command"},
		run: func(_ context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			executedWith = call.Input
			return fantasy.NewTextResponse("ran"), nil
		},
	}
	model := &repairMockModel{
		response: &fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "c1", ToolName: "bash", Input: `{"command": "echo hi"}`},
			},
			Usage:        fantasy.Usage{TotalTokens: 1},
			FinishReason: fantasy.FinishReasonStop,
		},
	}

	agent := fantasy.NewAgent(model,
		fantasy.WithTools(tool),
		fantasy.WithRepairToolCall(repairToolCall),
		fantasy.WithStopConditions(fantasy.StepCountIs(1)),
	)
	result, err := agent.Generate(context.Background(), fantasy.AgentCall{Prompt: "go"})
	require.NoError(t, err)
	require.Len(t, result.Steps, 1)

	calls := result.Steps[0].Content.ToolCalls()
	require.Len(t, calls, 1)
	require.False(t, calls[0].Invalid, "repaired call must pass validation: %v", calls[0].ValidationError)
	got := requireDescriptionAndCommand(t, executedWith)
	require.Equal(t, "echo hi", got.Command)
	require.Equal(t, tools.DefaultBashDescription("echo hi"), got.Description)
}

type repairMockModel struct {
	response *fantasy.Response
}

func (m *repairMockModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return m.response, nil
}

func (m *repairMockModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	return nil, errToolCallNotRepairable
}

func (m *repairMockModel) Provider() string { return "mock" }
func (m *repairMockModel) Model() string    { return "mock" }

func (m *repairMockModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errToolCallNotRepairable
}

func (m *repairMockModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errToolCallNotRepairable
}

func errMissing(name string) error { return &repairTestErr{"missing required parameter: " + name} }
func errInvalidJSON() error        { return &repairTestErr{"invalid JSON input"} }

type repairTestErr struct{ s string }

func (e *repairTestErr) Error() string { return e.s }
