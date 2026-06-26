package chat

import (
	"testing"

	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/message"
)

func formatParams(name, input string) string {
	t := &baseToolMessageItem{
		toolCall: message.ToolCall{Name: name, Input: input},
	}
	return t.formatParametersForCopy()
}

func TestFormatParametersForCopy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{"bash collapses whitespace", tools.BashToolName, `{"command":"echo\nhi\tthere"}`, "**Command:** echo hi    there"},
		{"view file only", tools.ViewToolName, `{"file_path":"/tmp/a.go"}`, "**File:** /tmp/a.go"},
		{"view with limit and offset", tools.ViewToolName, `{"file_path":"/tmp/a.go","limit":10,"offset":5}`, "**File:** /tmp/a.go\n**Limit:** 10\n**Offset:** 5"},
		{"edit", tools.EditToolName, `{"file_path":"/tmp/a.go"}`, "**File:** /tmp/a.go"},
		{"multiedit", tools.MultiEditToolName, `{"file_path":"/tmp/a.go","edits":[{},{}]}`, "**File:** /tmp/a.go\n**Edits:** 2"},
		{"write", tools.WriteToolName, `{"file_path":"/tmp/a.go"}`, "**File:** /tmp/a.go"},
		{"fetch full", tools.FetchToolName, `{"url":"http://x","format":"text","timeout":5}`, "**URL:** http://x\n**Format:** text\n**Timeout:** 5s"},
		{"agentic fetch", tools.AgenticFetchToolName, `{"url":"http://x","prompt":"hi"}`, "**URL:** http://x\n**Prompt:** hi"},
		{"web fetch", tools.WebFetchToolName, `{"url":"http://x"}`, "**URL:** http://x"},
		{"grep full", tools.GrepToolName, `{"pattern":"foo","path":"src","include":"*.go","literal_text":true}`, "**Pattern:** foo\n**Path:** src\n**Include:** *.go\n**Literal:** true"},
		{"glob", tools.GlobToolName, `{"pattern":"*.go","path":"src"}`, "**Pattern:** *.go\n**Path:** src"},
		{"ls default path", tools.LSToolName, `{}`, "**Path:** ."},
		{"sourcegraph", tools.SourcegraphToolName, `{"query":"q","count":3,"context_window":2}`, "**Query:** q\n**Count:** 3\n**Context:** 2"},
		{"diagnostics", tools.DiagnosticsToolName, `{}`, "**Project:** diagnostics"},
		{"agent", agent.AgentToolName, `{"prompt":"do it"}`, "**Task:**\ndo it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatParams(tc.tool, tc.input); got != tc.want {
				t.Errorf("formatParametersForCopy(%s) = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

func TestFormatParametersForCopy_GenericFallback(t *testing.T) {
	t.Parallel()
	got := formatParams("my_custom_tool", `{"some_key":"val"}`)
	if got != "**Some key:** val" {
		t.Errorf("generic fallback = %q", got)
	}
}

func TestFormatParametersForCopy_InvalidJSON(t *testing.T) {
	t.Parallel()
	if got := formatParams(tools.BashToolName, `not json`); got != "" {
		t.Errorf("invalid json = %q, want empty", got)
	}
}
