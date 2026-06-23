package historysearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/message"
)

// recordingEmbedder captures Embed calls so we can assert the indexer's
// filtering (finished + embeddable role + dedup) without a real model.
type recordingEmbedder struct {
	embedding.Service
	embedded map[string]bool
	has      map[string]bool
}

func (r *recordingEmbedder) Enabled() bool { return true }
func (r *recordingEmbedder) HasVector(_ context.Context, _ embedding.SourceType, id string) (bool, error) {
	return r.has[id], nil
}

func (r *recordingEmbedder) Embed(_ context.Context, _ embedding.SourceType, id, _ string, _ string) error {
	r.embedded[id] = true
	return nil
}

func TestIndexMessageFiltering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	finishedUser := message.Message{ID: "u1", SessionID: "s", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello"}, message.Finish{}}}
	require.True(t, finishedUser.IsFinished())

	rec := &recordingEmbedder{embedded: map[string]bool{}, has: map[string]bool{}}
	indexMessage(ctx, rec, finishedUser)
	require.True(t, rec.embedded["u1"], "finished user message should be embedded")

	// Unfinished message is skipped.
	rec = &recordingEmbedder{embedded: map[string]bool{}, has: map[string]bool{}}
	unfinished := message.Message{ID: "u2", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}}
	indexMessage(ctx, rec, unfinished)
	require.Empty(t, rec.embedded)

	// Tool-role message is skipped even when finished.
	rec = &recordingEmbedder{embedded: map[string]bool{}, has: map[string]bool{}}
	toolMsg := message.Message{ID: "t1", Role: message.Tool, Parts: []message.ContentPart{message.TextContent{Text: "x"}, message.Finish{}}}
	indexMessage(ctx, rec, toolMsg)
	require.Empty(t, rec.embedded)

	// Already-embedded message is skipped (dedup).
	rec = &recordingEmbedder{embedded: map[string]bool{}, has: map[string]bool{"u1": true}}
	indexMessage(ctx, rec, finishedUser)
	require.Empty(t, rec.embedded)
}
