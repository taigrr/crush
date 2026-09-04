package chat

import (
	"encoding/json"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// TestUserMessageItem_JobFinishedNoticeRendersAsJob verifies the folded
// background-job completion aside is drawn as a Job entry (id, label,
// command, output) with the model-facing guidance stripped, rather than
// as a user prompt bubble.
func TestUserMessageItem_JobFinishedNoticeRendersAsJob(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	text := tools.JobFinishedNoticePrefix + " Run the tests (job 01F)\n" +
		"Command: go test ./...\n\nok  pkg 0.1s\n\n" +
		"This is an automatic notice that a command you moved to the background has completed. Use the result if it matters."
	msg := &message.Message{
		ID:    "m1",
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}

	plain := ansi.Strip(NewUserMessageItem(&sty, msg, nil).(*UserMessageItem).RawRender(100))

	require.Contains(t, plain, "Job")
	require.Contains(t, plain, "(Finished)")
	require.Contains(t, plain, "PID 01F")
	require.Contains(t, plain, "Run the tests")
	require.Contains(t, plain, "go test ./...")
	require.Contains(t, plain, "ok  pkg 0.1s")
	require.NotContains(t, plain, "automatic notice")
	require.NotContains(t, plain, tools.JobFinishedNoticePrefix)
}

// TestBashTool_BackgroundReasonLabelsJobHeader verifies the job header
// action reflects why the command was backgrounded.
func TestBashTool_BackgroundReasonLabelsJobHeader(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	tc := message.ToolCall{ID: "tc", Name: tools.BashToolName, Input: `{"command":"sleep 90","description":"nap"}`, Finished: true}

	render := func(reason string) string {
		meta, err := json.Marshal(tools.BashResponseMetadata{Background: true, ShellID: "0A1", BackgroundReason: reason, Description: "nap"})
		require.NoError(t, err)
		res := &message.ToolResult{ToolCallID: "tc", Content: "moved", Metadata: string(meta)}
		return ansi.Strip((&BashToolRenderContext{}).RenderTool(&sty, 100, &ToolRenderOpts{ToolCall: tc, Result: res, Status: ToolStatusSuccess}))
	}

	require.Contains(t, render("user"), "(Backgrounded)")
	require.Contains(t, render("steer"), "(Backgrounded: steer)")
	require.Contains(t, render("timeout"), "(Backgrounded: timeout)")
	require.Contains(t, render(""), "(Start)")
}
