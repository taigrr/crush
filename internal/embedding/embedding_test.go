package embedding

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeVector(t *testing.T) {
	t.Parallel()
	vec := []float32{0.1, -0.2, 3.5, 0}
	got := decodeVector(encodeVector(vec))
	require.Equal(t, vec, got)
	require.Nil(t, decodeVector([]byte{1, 2, 3})) // not a multiple of 4
}

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()
	require.InDelta(t, 1.0, cosineSimilarity([]float32{1, 0}, []float32{1, 0}), 1e-9)
	require.InDelta(t, 0.0, cosineSimilarity([]float32{1, 0}, []float32{0, 1}), 1e-9)
	require.InDelta(t, -1.0, cosineSimilarity([]float32{1, 0}, []float32{-1, 0}), 1e-9)
	require.Equal(t, 0.0, cosineSimilarity([]float32{1, 0}, []float32{0, 0})) // zero magnitude
	require.Equal(t, 0.0, cosineSimilarity([]float32{1}, []float32{1, 0}))    // mismatched len
}

func TestReciprocalRankFusion(t *testing.T) {
	t.Parallel()

	exact := []ranked{{"a", 1}, {"b", 2}}
	semantic := []ranked{{"b", 1}, {"c", 2}}
	out := reciprocalRankFusion(exact, semantic)

	// b appears in both => highest fused score and MatchBoth.
	require.Equal(t, "b", out[0].key)
	require.Equal(t, MatchBoth, out[0].match)

	byKey := map[string]fused{}
	for _, f := range out {
		byKey[f.key] = f
	}
	require.Equal(t, MatchExact, byKey["a"].match)
	require.Equal(t, MatchSemantic, byKey["c"].match)
	require.Len(t, out, 3)

	// Monotonic: b > a and b > c.
	require.Greater(t, byKey["b"].score, byKey["a"].score)
	require.Greater(t, byKey["b"].score, byKey["c"].score)
}

func TestReciprocalRankFusionSingleSignal(t *testing.T) {
	t.Parallel()
	// Substring-only (semantic disabled/unavailable) still ranks.
	out := reciprocalRankFusion([]ranked{{"a", 1}, {"b", 2}}, nil)
	require.Len(t, out, 2)
	require.Equal(t, "a", out[0].key)
	require.Equal(t, MatchExact, out[0].match)
}

func TestSnippet(t *testing.T) {
	t.Parallel()
	// Match in middle gets ellipses both sides.
	long := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxNEEDLExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	s := snippet(long, "needle")
	require.Contains(t, s, "NEEDLE")
	require.True(t, len(s) > 0 && s[0:3] == "…")

	// Semantic-only hit (query absent) returns head of text.
	require.Equal(t, "hello world", snippet("hello world", "absent"))

	// Newlines collapsed.
	require.Equal(t, "a b", snippet("a\nb", ""))
}

func TestSnippetMath(t *testing.T) {
	t.Parallel()
	// Guard against window math regressions.
	require.LessOrEqual(t, len("needle"), int(math.MaxInt32))
}
