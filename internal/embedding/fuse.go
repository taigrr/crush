package embedding

import "sort"

// MatchType records which signal(s) produced a hit.
type MatchType string

const (
	MatchExact    MatchType = "exact"
	MatchSemantic MatchType = "semantic"
	MatchBoth     MatchType = "both"
)

// rrfK is the Reciprocal Rank Fusion constant. 60 is the value from the
// original RRF paper and a common default; it dampens the contribution
// of low-ranked items without needing score calibration between the two
// signals.
const rrfK = 60.0

// semanticFloor is the minimum cosine similarity for a document to count
// as a semantic match. Below it the doc is treated as not semantically
// related, so unrelated rows that merely have a stored vector don't get
// tagged "semantic"/"both" or add noise to the fusion. This is a coarse
// model-independent floor; tune per spec §11 if needed.
const semanticFloor = 0.25

// ranked is a minimal view of a result used for fusion: an opaque key
// identifying the document and its 1-based rank within one signal's
// ordered list.
type ranked struct {
	key  string
	rank int
}

// fused is the result of merging two ranked lists.
type fused struct {
	key   string
	score float64
	match MatchType
}

// reciprocalRankFusion merges an exact-ordered list and a
// semantic-ordered list into a single ranking. Each list is assumed to
// be in descending relevance order. A document's fused score is the sum
// of 1/(k+rank) across every list it appears in, so documents both
// signals agree on rank highest while signal-exclusive hits still
// surface. The returned slice is sorted by descending score; ties break
// by key for determinism.
func reciprocalRankFusion(exact, semantic []ranked) []fused {
	type acc struct {
		score    float64
		inExact  bool
		inSemant bool
	}
	scores := make(map[string]*acc)
	add := func(list []ranked, semantic bool) {
		for _, r := range list {
			a := scores[r.key]
			if a == nil {
				a = &acc{}
				scores[r.key] = a
			}
			a.score += 1.0 / (rrfK + float64(r.rank))
			if semantic {
				a.inSemant = true
			} else {
				a.inExact = true
			}
		}
	}
	add(exact, false)
	add(semantic, true)

	out := make([]fused, 0, len(scores))
	for key, a := range scores {
		m := MatchExact
		switch {
		case a.inExact && a.inSemant:
			m = MatchBoth
		case a.inSemant:
			m = MatchSemantic
		}
		out = append(out, fused{key: key, score: a.score, match: m})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].key < out[j].key
	})
	return out
}
