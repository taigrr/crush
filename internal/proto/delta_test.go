package proto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiffAndApplyAppendOnly(t *testing.T) {
	t.Parallel()
	prev := Message{ID: "m", Role: Assistant, Parts: []ContentPart{
		ReasoningContent{Thinking: "plan"},
		TextContent{Text: "Hel"},
		ToolCall{ID: "c1", Name: "edit", Input: `{"pa`},
	}}
	next := Message{ID: "m", Role: Assistant, UpdatedAt: 5, Parts: []ContentPart{
		ReasoningContent{Thinking: "plan more"},
		TextContent{Text: "Hello"},
		ToolCall{ID: "c1", Name: "edit", Input: `{"path":1}`},
	}}
	d, ok := DiffMessage(prev, next)
	require.True(t, ok)
	require.Len(t, d.Appends, 3)
	require.Equal(t, PartAppend{Part: 1, Field: DeltaFieldText, From: 3, Text: "lo"}, d.Appends[1])

	got := prev
	require.True(t, ApplyDelta(&got, d))
	require.Equal(t, next.Parts, got.Parts)
	require.EqualValues(t, 5, got.UpdatedAt)

	// A delta for a diverged copy is refused and changes nothing.
	stale := Message{ID: "m", Parts: []ContentPart{ReasoningContent{Thinking: "pl"}, TextContent{Text: "Hel"}, ToolCall{Input: `{"pa`}}}
	require.False(t, ApplyDelta(&stale, d))
	require.Equal(t, "pl", stale.Parts[0].(ReasoningContent).Thinking)
}

func TestDiffRejectsNonAppendChanges(t *testing.T) {
	t.Parallel()
	base := Message{ID: "m", Parts: []ContentPart{TextContent{Text: "abc"}, ToolCall{ID: "c", Input: "x"}}}
	cases := map[string]Message{
		"rewrite":      {ID: "m", Parts: []ContentPart{TextContent{Text: "abX"}, ToolCall{ID: "c", Input: "x"}}},
		"new part":     {ID: "m", Parts: []ContentPart{TextContent{Text: "abc"}, ToolCall{ID: "c", Input: "x"}, Finish{Reason: FinishReasonEndTurn}}},
		"tool done":    {ID: "m", Parts: []ContentPart{TextContent{Text: "abc"}, ToolCall{ID: "c", Input: "x", Finished: true}}},
		"other id":     {ID: "n", Parts: base.Parts},
		"type changed": {ID: "m", Parts: []ContentPart{ReasoningContent{Thinking: "abc"}, ToolCall{ID: "c", Input: "x"}}},
	}
	for name, next := range cases {
		_, ok := DiffMessage(base, next)
		require.False(t, ok, name)
	}
}
