package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/embedding"
)

func TestSearchJSONOutputMode(t *testing.T) {
	t.Parallel()
	out := searchJSONOutput("q", embedding.SearchResult{Mode: embedding.SearchModeText, FallbackReason: embedding.FallbackNoEmbedder})
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "text", got["mode"])
	require.Equal(t, "no_embedder", got["fallback_reason"])
	require.Equal(t, false, got["semantic_used"])
	require.Equal(t, []any{}, got["hits"])

	hybrid := searchJSONOutput("q", embedding.SearchResult{Mode: embedding.SearchModeHybrid, SemanticUsed: true})
	raw, err = json.Marshal(hybrid)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fallback_reason")
}

func TestSearchModeLabel(t *testing.T) {
	t.Parallel()
	require.Equal(t, "hybrid", searchModeLabel(embedding.SearchModeHybrid, ""))
	require.Equal(t, "text, disabled by request", searchModeLabel(embedding.SearchModeText, embedding.FallbackDisabledByRequest))
	require.Equal(t, "text", searchModeLabel("", ""))
}
