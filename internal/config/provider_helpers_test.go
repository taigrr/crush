package config

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
)

func TestMergeProviderModels(t *testing.T) {
	t.Parallel()

	t.Run("config models take precedence and come first", func(t *testing.T) {
		t.Parallel()
		got := mergeProviderModels(
			[]catwalk.Model{{ID: "a", Name: "Custom A"}},
			[]catwalk.Model{{ID: "a", Name: "Builtin A"}, {ID: "b", Name: "Builtin B"}},
		)
		require.Equal(t, []catwalk.Model{
			{ID: "a", Name: "Custom A"},
			{ID: "b", Name: "Builtin B"},
		}, got)
	})

	t.Run("dedups within config models keeping first", func(t *testing.T) {
		t.Parallel()
		got := mergeProviderModels(
			[]catwalk.Model{{ID: "a", Name: "First"}, {ID: "a", Name: "Second"}},
			nil,
		)
		require.Equal(t, []catwalk.Model{{ID: "a", Name: "First"}}, got)
	})

	t.Run("defaults empty name to id", func(t *testing.T) {
		t.Parallel()
		got := mergeProviderModels(
			[]catwalk.Model{{ID: "a"}},
			[]catwalk.Model{{ID: "b"}},
		)
		require.Equal(t, []catwalk.Model{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}, got)
	})

	t.Run("empty inputs yield empty non-nil slice", func(t *testing.T) {
		t.Parallel()
		got := mergeProviderModels(nil, nil)
		require.NotNil(t, got)
		require.Empty(t, got)
	})
}

type fakeResolver struct {
	values map[string]string
	err    error
}

func (f fakeResolver) ResolveValue(v string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if resolved, ok := f.values[v]; ok {
		return resolved, nil
	}
	return v, nil
}

func TestResolveProviderHeaders(t *testing.T) {
	t.Parallel()

	t.Run("extra overrides default", func(t *testing.T) {
		t.Parallel()
		got, err := resolveProviderHeaders(
			fakeResolver{}, "p",
			map[string]string{"X": "default", "Y": "keep"},
			map[string]string{"X": "override"},
		)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"X": "override", "Y": "keep"}, got)
	})

	t.Run("resolves templated values", func(t *testing.T) {
		t.Parallel()
		got, err := resolveProviderHeaders(
			fakeResolver{values: map[string]string{"$(token)": "abc"}}, "p",
			nil, map[string]string{"Authorization": "$(token)"},
		)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"Authorization": "abc"}, got)
	})

	t.Run("drops headers that resolve to empty", func(t *testing.T) {
		t.Parallel()
		got, err := resolveProviderHeaders(
			fakeResolver{values: map[string]string{"$EMPTY": ""}}, "p",
			nil, map[string]string{"X": "$EMPTY", "Y": "keep"},
		)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"Y": "keep"}, got)
	})

	t.Run("propagates resolver error", func(t *testing.T) {
		t.Parallel()
		_, err := resolveProviderHeaders(
			fakeResolver{err: errors.New("boom")}, "p",
			nil, map[string]string{"X": "$(bad)"},
		)
		require.Error(t, err)
	})

	t.Run("no headers yields empty non-nil map", func(t *testing.T) {
		t.Parallel()
		got, err := resolveProviderHeaders(fakeResolver{}, "p", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Empty(t, got)
	})
}
