package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddingSignature(t *testing.T) {
	t.Parallel()

	base := &EmbeddingConfig{Provider: "bedrock", Model: "amazon.titan-embed-text-v2:0", Dimensions: 1024, Normalize: true}

	// Same fields => same signature.
	same := &EmbeddingConfig{Provider: "bedrock", Model: "amazon.titan-embed-text-v2:0", Dimensions: 1024, Normalize: true}
	require.Equal(t, base.Signature(), same.Signature())

	// HybridSearch is NOT part of the signature.
	tru := true
	withHybrid := *base
	withHybrid.HybridSearch = &tru
	require.Equal(t, base.Signature(), withHybrid.Signature())

	// Any comparability-affecting field changes the signature.
	for _, mut := range []func(*EmbeddingConfig){
		func(e *EmbeddingConfig) { e.Provider = "openai" },
		func(e *EmbeddingConfig) { e.Model = "text-embedding-3-small" },
		func(e *EmbeddingConfig) { e.Dimensions = 512 },
		func(e *EmbeddingConfig) { e.Normalize = false },
	} {
		c := *base
		mut(&c)
		require.NotEqual(t, base.Signature(), c.Signature())
	}

	// Nil is the empty signature.
	var nilCfg *EmbeddingConfig
	require.Empty(t, nilCfg.Signature())
}

func TestEmbeddingHybridEnabled(t *testing.T) {
	t.Parallel()

	tru, fls := true, false
	require.False(t, (*EmbeddingConfig)(nil).HybridEnabled())
	require.True(t, (&EmbeddingConfig{}).HybridEnabled())                    // unset => default on
	require.True(t, (&EmbeddingConfig{HybridSearch: &tru}).HybridEnabled())  // explicit on
	require.False(t, (&EmbeddingConfig{HybridSearch: &fls}).HybridEnabled()) // explicit off
}
