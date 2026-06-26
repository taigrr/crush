package chat

import (
	"testing"

	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/agent/tools"
)

func TestPrettifyToolName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		agent.AgentToolName:        "Agent",
		tools.BashToolName:         "Bash",
		tools.JobOutputToolName:    "Job: Output",
		tools.JobKillToolName:      "Job: Kill",
		tools.DownloadToolName:     "Download",
		tools.EditToolName:         "Edit",
		tools.MultiEditToolName:    "Multi-Edit",
		tools.FetchToolName:        "Fetch",
		tools.AgenticFetchToolName: "Agentic Fetch",
		tools.WebFetchToolName:     "Fetch",
		tools.WebSearchToolName:    "Search",
		tools.GlobToolName:         "Glob",
		tools.GrepToolName:         "Grep",
		tools.LSToolName:           "List",
		tools.SourcegraphToolName:  "Sourcegraph",
		tools.TodosToolName:        "To-Do",
		tools.ViewToolName:         "View",
		tools.WriteToolName:        "Write",
	}
	for name, want := range cases {
		if got := prettifyToolName(name); got != want {
			t.Errorf("prettifyToolName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPrettifyToolName_UnknownFallsBackToHumanized(t *testing.T) {
	t.Parallel()
	// Unknown (e.g. MCP) names fall through to humanizedToolName: underscores
	// and dashes become spaces and each word is capitalized.
	if got := prettifyToolName("my_custom-tool"); got != "My Custom Tool" {
		t.Errorf("prettifyToolName(unknown) = %q, want %q", got, "My Custom Tool")
	}
}
