package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
)

func models(ids ...string) []catwalk.Model {
	out := make([]catwalk.Model, len(ids))
	for i, id := range ids {
		out[i] = catwalk.Model{ID: id}
	}
	return out
}

func TestParseModelString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in           string
		wantProvider string
		wantModel    string
	}{
		{"", "", ""},
		{"gpt-4", "", "gpt-4"},
		{"openai/gpt-4", "openai", "gpt-4"},
		{"a/b/c", "a", "b/c"},
	}
	for _, tc := range cases {
		p, m := parseModelString(tc.in)
		require.Equal(t, tc.wantProvider, p, "provider for %q", tc.in)
		require.Equal(t, tc.wantModel, m, "model for %q", tc.in)
	}
}

func TestMatchesModel(t *testing.T) {
	t.Parallel()

	require.False(t, matchesModel("", "", "gpt-4", "openai"), "empty want id never matches")
	require.True(t, matchesModel("gpt-4", "", "GPT-4", "openai"), "case-insensitive match")
	require.True(t, matchesModel("gpt-4", "openai", "gpt-4", "openai"), "provider matches")
	require.False(t, matchesModel("gpt-4", "anthropic", "gpt-4", "openai"), "provider mismatch")
	require.False(t, matchesModel("gpt-5", "", "gpt-4", "openai"), "id mismatch")
}

func TestValidateModelMatches(t *testing.T) {
	t.Parallel()

	_, err := validateModelMatches(nil, "gpt-4", "large")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	got, err := validateModelMatches([]modelMatch{{provider: "openai", modelID: "gpt-4"}}, "gpt-4", "large")
	require.NoError(t, err)
	require.Equal(t, "openai", got.provider)

	_, err = validateModelMatches([]modelMatch{
		{provider: "openai", modelID: "gpt-4"},
		{provider: "azure", modelID: "gpt-4"},
	}, "gpt-4", "large")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple providers")
	require.Contains(t, err.Error(), "openai")
	require.Contains(t, err.Error(), "azure")
}

func TestFindModelMatches(t *testing.T) {
	t.Parallel()

	providers := map[string]config.ProviderConfig{
		"openai": {Models: models("gpt-4", "gpt-3.5")},
		"azure":  {Models: models("gpt-4")},
		"off":    {Disable: true, Models: models("gpt-4")},
	}

	large, small := findModelMatches(providers, "gpt-4", "openai/gpt-3.5")
	require.Len(t, large, 2, "gpt-4 in openai and azure, disabled provider skipped")
	require.Len(t, small, 1)
	require.Equal(t, "openai", small[0].provider)
	require.Equal(t, "gpt-3.5", small[0].modelID)
}

func TestLatestTopLevelSession(t *testing.T) {
	t.Parallel()

	require.Nil(t, latestTopLevelSession(nil))

	// Only child sessions present: must return nil, never a child.
	onlyChildren := []proto.Session{
		{ID: "c1", ParentSessionID: "p", UpdatedAt: 100},
		{ID: "c2", ParentSessionID: "p", UpdatedAt: 200},
	}
	require.Nil(t, latestTopLevelSession(onlyChildren), "must not resume a child session")

	// Newest session is a child; must pick the newest top-level instead.
	mixed := []proto.Session{
		{ID: "top-old", UpdatedAt: 50},
		{ID: "child-new", ParentSessionID: "p", UpdatedAt: 999},
		{ID: "top-new", UpdatedAt: 300},
	}
	got := latestTopLevelSession(mixed)
	require.NotNil(t, got)
	require.Equal(t, "top-new", got.ID)
}
