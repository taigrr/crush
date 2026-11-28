package chat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// TestSetStylesRetargetsItemAndNestedTools locks the pointer swap used
// when minimal mode toggles or the theme changes while it is on.
func TestSetStylesRetargetsItemAndNestedTools(t *testing.T) {
	t.Parallel()

	base := themes.CharmtonePantera()
	next := base.WithMinimal(true)
	require.NotSame(t, &base, next)

	user := NewUserMessageItem(&base, &message.Message{ID: "u", Role: message.User}, nil).(*UserMessageItem)
	assistant := NewAssistantMessageItem(&base, &message.Message{ID: "a", Role: message.Assistant}).(*AssistantMessageItem)
	shell := NewShellMessageItem(&base, &message.Message{ID: "s", Role: message.Shell}).(*ShellMessageItem)
	info := NewAssistantInfoItem(&base, &message.Message{ID: "a", Role: message.Assistant}, nil, time.Unix(testStartedAt, 0)).(*AssistantInfoItem)
	parent := NewAgentToolMessageItem(&base, message.ToolCall{ID: "p", Name: agent.AgentToolName}, nil, false)
	nested := NewToolMessageItem(&base, "a", message.ToolCall{ID: "c", Name: "unknown-tool"}, nil, false)
	parent.AddNestedTool(nested)
	review := NewReviewToolMessageItem(&base, message.ToolCall{ID: "r", Name: agent.ReviewToolName}, nil, false)
	reviewChild := NewToolMessageItem(&base, "a", message.ToolCall{ID: "rc", Name: "unknown-tool"}, nil, false)
	review.AddNestedTool(reviewChild)

	for _, item := range []MessageItem{user, assistant, shell, info, parent, review} {
		item.SetStyles(next)
	}

	require.Same(t, next, user.sty)
	require.Same(t, next, assistant.sty)
	require.Same(t, next, shell.sty)
	require.Same(t, next, info.sty)
	require.Same(t, next, parent.sty)
	require.Same(t, next, nested.(*baseToolMessageItem).sty)
	require.Same(t, next, review.sty)
	require.Same(t, next, reviewChild.(*baseToolMessageItem).sty)
	require.NotNil(t, user.highlighter)
	require.NotNil(t, assistant.highlighter)
}
