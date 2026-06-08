package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/editor"
)

func TestFilterAvailableTools(t *testing.T) {
	t.Parallel()

	plain := NewGlobTool(func(context.Context) string { return "." })
	gatedOn := WithContextGate(NewShowLocationsTool(), func(context.Context) bool { return true })
	gatedOff := WithContextGate(NewEditorContextTool(), func(context.Context) bool { return false })

	got := FilterAvailableTools(t.Context(), []fantasy.AgentTool{plain, gatedOn, gatedOff})

	names := make([]string, len(got))
	for i, tool := range got {
		names[i] = tool.Info().Name
	}
	require.Contains(t, names, plain.Info().Name)
	require.Contains(t, names, ShowLocationsToolName)
	require.NotContains(t, names, EditorContextToolName)
}

func TestEditorAttachedGate(t *testing.T) {
	t.Parallel()

	// No bridge in context: editor tools must be filtered out.
	noEditor := FilterAvailableTools(t.Context(), []fantasy.AgentTool{
		WithContextGate(NewEditorContextTool(), EditorAttached),
		WithContextGate(NewShowLocationsTool(), EditorAttached),
	})
	require.Empty(t, noEditor)

	// Attached bridge: editor tools are advertised.
	ctx := WithEditorBridge(t.Context(), stubBridge{available: true})
	withEditor := FilterAvailableTools(ctx, []fantasy.AgentTool{
		WithContextGate(NewEditorContextTool(), EditorAttached),
		WithContextGate(NewShowLocationsTool(), EditorAttached),
	})
	require.Len(t, withEditor, 2)
}

// stubBridge is a minimal editor.Bridge whose availability is fixed.
type stubBridge struct{ available bool }

func (s stubBridge) Available() bool { return s.available }
func (stubBridge) Context(context.Context) (editor.EditorContext, error) {
	return editor.EditorContext{}, editor.ErrUnavailable
}
func (stubBridge) ShowLocations(context.Context, string, []editor.Location) error { return nil }
func (stubBridge) FlashEdit(context.Context, string, int, int) error              { return nil }
func (stubBridge) NotifyFileChanged(context.Context, string) error                { return nil }
func (stubBridge) SetWorkingDir(context.Context, string) error                    { return nil }
func (stubBridge) Close() error                                                   { return nil }
