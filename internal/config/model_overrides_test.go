package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
)

func ptr[T any](v T) *T { return &v }

func TestApplyModelOverrides(t *testing.T) {
	t.Parallel()

	model := &catwalk.Model{DefaultMaxTokens: 4096}

	t.Run("falls back to model default max tokens when unset", func(t *testing.T) {
		t.Parallel()
		target := SelectedModel{}
		applyModelOverrides(&target, SelectedModel{}, model)
		require.Equal(t, int64(4096), target.MaxTokens)
	})

	t.Run("uses override max tokens when positive", func(t *testing.T) {
		t.Parallel()
		target := SelectedModel{}
		applyModelOverrides(&target, SelectedModel{MaxTokens: 1000}, model)
		require.Equal(t, int64(1000), target.MaxTokens)
	})

	t.Run("Think is copied unconditionally so it can be turned off", func(t *testing.T) {
		t.Parallel()
		target := SelectedModel{Think: true}
		applyModelOverrides(&target, SelectedModel{Think: false}, model)
		require.False(t, target.Think)
	})

	t.Run("Think can be turned on", func(t *testing.T) {
		t.Parallel()
		target := SelectedModel{Think: false}
		applyModelOverrides(&target, SelectedModel{Think: true}, model)
		require.True(t, target.Think)
	})

	t.Run("pointer fields preserved when override nil", func(t *testing.T) {
		t.Parallel()
		target := SelectedModel{Temperature: ptr(0.7), TopP: ptr(0.9)}
		applyModelOverrides(&target, SelectedModel{}, model)
		require.NotNil(t, target.Temperature)
		require.Equal(t, 0.7, *target.Temperature)
		require.NotNil(t, target.TopP)
	})

	t.Run("pointer and string fields applied when set", func(t *testing.T) {
		t.Parallel()
		target := SelectedModel{}
		applyModelOverrides(&target, SelectedModel{
			ReasoningEffort:  "high",
			Temperature:      ptr(0.2),
			TopP:             ptr(0.1),
			TopK:             ptr(int64(40)),
			FrequencyPenalty: ptr(0.5),
			PresencePenalty:  ptr(0.6),
			ProviderOptions:  map[string]any{"foo": "bar"},
		}, model)
		require.Equal(t, "high", target.ReasoningEffort)
		require.Equal(t, 0.2, *target.Temperature)
		require.Equal(t, 0.1, *target.TopP)
		require.Equal(t, int64(40), *target.TopK)
		require.Equal(t, 0.5, *target.FrequencyPenalty)
		require.Equal(t, 0.6, *target.PresencePenalty)
		require.Equal(t, map[string]any{"foo": "bar"}, target.ProviderOptions)
	})
}
