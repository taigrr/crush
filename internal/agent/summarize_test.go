package agent

import (
	"strings"
	"testing"

	"github.com/taigrr/crush/internal/milestone"
	"github.com/taigrr/crush/internal/session"
)

func TestBuildSummaryPrompt(t *testing.T) {
	t.Parallel()

	t.Run("base prompt with no extras", func(t *testing.T) {
		t.Parallel()
		got := buildSummaryPrompt(nil, nil)
		if got != "Provide a detailed summary of our conversation above." {
			t.Fatalf("unexpected base prompt: %q", got)
		}
		if strings.Contains(got, "##") {
			t.Fatal("base prompt should have no sections")
		}
	})

	t.Run("includes milestones", func(t *testing.T) {
		t.Parallel()
		got := buildSummaryPrompt(nil, []milestone.Milestone{
			{TurnNumber: 3, ShortSummary: "added auth", FullSummary: "Implemented login flow."},
		})
		if !strings.Contains(got, "## Session Milestones") {
			t.Fatal("missing milestones section")
		}
		if !strings.Contains(got, "**added auth** (turn 3): Implemented login flow.") {
			t.Fatalf("milestone not formatted correctly: %q", got)
		}
		if strings.Contains(got, "## Current Todo List") {
			t.Fatal("should not include todo section when none given")
		}
	})

	t.Run("includes todos", func(t *testing.T) {
		t.Parallel()
		got := buildSummaryPrompt([]session.Todo{
			{Status: session.TodoStatusInProgress, Content: "write tests"},
			{Status: session.TodoStatusCompleted, Content: "fix bug"},
		}, nil)
		if !strings.Contains(got, "## Current Todo List") {
			t.Fatal("missing todo section")
		}
		if !strings.Contains(got, "- [in_progress] write tests") {
			t.Fatalf("todo not formatted correctly: %q", got)
		}
		if !strings.Contains(got, "- [completed] fix bug") {
			t.Fatalf("todo not formatted correctly: %q", got)
		}
	})

	t.Run("includes both sections in order", func(t *testing.T) {
		t.Parallel()
		got := buildSummaryPrompt(
			[]session.Todo{{Status: session.TodoStatusPending, Content: "do thing"}},
			[]milestone.Milestone{{TurnNumber: 1, ShortSummary: "start", FullSummary: "Started."}},
		)
		mi := strings.Index(got, "## Session Milestones")
		ti := strings.Index(got, "## Current Todo List")
		if mi == -1 || ti == -1 {
			t.Fatal("expected both sections")
		}
		if mi > ti {
			t.Fatal("milestones should appear before todos")
		}
	})
}
