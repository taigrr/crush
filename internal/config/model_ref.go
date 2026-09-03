package config

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/sahilm/fuzzy"
)

// ResolveModelRef maps a model reference, as typed by a person or emitted
// by a model in a tool call, to a concrete selection against the current
// config. It is the single grammar shared by the agent, review, and swarm
// tools' `model` parameter and by `crush run -m`.
//
// Accepted forms, tried in this order:
//
//	"<role>"           — a key of models: large, small, orchestrator, or
//	                     any user-defined role. Returns that selection
//	                     (with its reasoning effort and tuning intact).
//	"provider/model"   — fully qualified; the prefix must name a
//	                     configured provider.
//	"model"            — bare id, resolved by scanning every enabled
//	                     provider. Ambiguous across providers is an error
//	                     rather than a guess: delegation is billed and
//	                     long-running, so picking the wrong vendor is
//	                     worse than asking the caller to qualify it.
//
// Model ids may themselves contain "/" (openrouter's "anthropic/claude-x"),
// so the qualified form splits on the FIRST separator only and is treated
// as qualified only when the prefix is a configured provider; otherwise
// the whole string is tried as a bare id.
func (c *Config) ResolveModelRef(ref string) (SelectedModel, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return SelectedModel{}, fmt.Errorf("model reference is empty")
	}

	// Role names first. A role that is configured but points at an
	// unknown model is a config error the caller should see, not a
	// silent fall-through to bare-id matching.
	if sel, ok := c.Models[SelectedModelType(ref)]; ok {
		if sel.Provider == "" || sel.Model == "" {
			return SelectedModel{}, fmt.Errorf("role %q is configured without a provider/model", ref)
		}
		if c.GetModel(sel.Provider, sel.Model) == nil {
			return SelectedModel{}, fmt.Errorf("role %q points at %s/%s, which no configured provider offers", ref, sel.Provider, sel.Model)
		}
		return sel, nil
	}

	if providerID, modelID, ok := strings.Cut(ref, "/"); ok {
		if providerCfg, found := c.Providers.Get(providerID); found {
			return resolveInProvider(providerID, providerCfg, modelID)
		}
	}

	var matches []SelectedModel
	for providerID, providerCfg := range c.Providers.Seq2() {
		if providerCfg.Disable {
			continue
		}
		for _, m := range providerCfg.Models {
			if m.ID == ref {
				matches = append(matches, SelectedModel{Provider: providerID, Model: m.ID})
			}
		}
	}

	switch len(matches) {
	case 0:
		return SelectedModel{}, fmt.Errorf(
			"unknown model %q: not a configured role and no enabled provider has a model with that id", ref,
		)
	case 1:
		return matches[0], nil
	default:
		providers := make([]string, 0, len(matches))
		for _, m := range matches {
			providers = append(providers, m.Provider)
		}
		// Sorted so the user-facing message is deterministic; map
		// iteration order is random.
		slices.Sort(providers)
		return SelectedModel{}, fmt.Errorf(
			"ambiguous model %q: configured on providers %s; qualify it as <provider>/%s",
			ref, strings.Join(providers, ", "), ref,
		)
	}
}

// resolveInProvider looks up modelID within an already-resolved provider.
func resolveInProvider(providerID string, providerCfg ProviderConfig, modelID string) (SelectedModel, error) {
	if providerCfg.Disable {
		return SelectedModel{}, fmt.Errorf("provider %q is disabled", providerID)
	}
	for _, m := range providerCfg.Models {
		if m.ID == modelID {
			return SelectedModel{Provider: providerID, Model: m.ID}, nil
		}
	}
	return SelectedModel{}, fmt.Errorf("provider %q has no model %q", providerID, modelID)
}

// ReasoningEffortLevels is the vocabulary a trailing token in a typed model
// reference may use to request a reasoning effort (e.g. "/model fable high").
// A token is only honored when the resolved model lists it in its
// reasoning_levels; otherwise the whole string is treated as a model
// reference so a model whose id ends in one of these words still resolves.
var ReasoningEffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// ModelMatch is one candidate produced by ResolveModelRefLoose when a
// reference is ambiguous, so callers can list the options.
type ModelMatch struct {
	Selection SelectedModel
	Name      string
}

// ErrAmbiguousModelRef is returned by ResolveModelRefLoose when several
// distinct models match a reference equally well.
type ErrAmbiguousModelRef struct {
	Ref     string
	Matches []ModelMatch
}

func (e *ErrAmbiguousModelRef) Error() string {
	ids := make([]string, 0, len(e.Matches))
	for _, m := range e.Matches {
		ids = append(ids, m.Selection.Provider+"/"+m.Selection.Model)
	}
	return fmt.Sprintf("ambiguous model %q: matches %s", e.Ref, strings.Join(ids, ", "))
}

// ResolveModelRefLoose resolves a model reference typed by a person. It
// accepts everything ResolveModelRef does, plus:
//
//   - an optional trailing reasoning-effort token ("fable high"), applied
//     when the resolved model supports that level;
//   - case-insensitive substring and fuzzy matching against model ids and
//     display names across every enabled provider ("fable" -> the fable
//     model), so the reference can be typed the way a person thinks of it.
//
// Ties between equally good matches are broken in favour of models that
// currently hold a role (large/small/orchestrator/...), then models in
// the recent list, then the shortest id. A tie that survives all three is
// reported as *ErrAmbiguousModelRef with the candidates.
func (c *Config) ResolveModelRefLoose(ref string) (SelectedModel, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return SelectedModel{}, fmt.Errorf("model reference is empty")
	}

	// Exact grammar first: roles, provider/model, bare id.
	if sel, err := c.ResolveModelRef(ref); err == nil {
		return sel, nil
	}

	// Peel an effort suffix and retry exact.
	modelRef, effort := splitEffortSuffix(ref)
	if effort != "" {
		if sel, err := c.ResolveModelRef(modelRef); err == nil {
			return c.applyEffort(sel, effort, ref)
		}
	}

	// Fuzzy: try the full string, then without the effort suffix.
	sel, err := c.fuzzyModel(ref)
	if err == nil {
		return sel, nil
	}
	if effort != "" {
		if sel, ferr := c.fuzzyModel(modelRef); ferr == nil {
			return c.applyEffort(sel, effort, ref)
		}
	}
	return SelectedModel{}, err
}

func splitEffortSuffix(ref string) (modelRef, effort string) {
	fields := strings.Fields(ref)
	if len(fields) < 2 {
		return ref, ""
	}
	last := strings.ToLower(fields[len(fields)-1])
	if !slices.Contains(ReasoningEffortLevels, last) {
		return ref, ""
	}
	return strings.Join(fields[:len(fields)-1], " "), last
}

func (c *Config) applyEffort(sel SelectedModel, effort, ref string) (SelectedModel, error) {
	m := c.GetModel(sel.Provider, sel.Model)
	if m == nil || !slices.Contains(m.ReasoningLevels, effort) {
		levels := "none"
		if m != nil && len(m.ReasoningLevels) > 0 {
			levels = strings.Join(m.ReasoningLevels, ", ")
		}
		return SelectedModel{}, fmt.Errorf("%q: %s does not support reasoning effort %q (levels: %s)", ref, sel.Model, effort, levels)
	}
	sel.ReasoningEffort = effort
	return sel, nil
}

// fuzzyModel scores ref against every enabled provider's model ids and
// names. Matching is case-insensitive; a substring hit outranks a
// scattered fuzzy hit, and among substring hits a shorter id wins after the
// role/recent tie-breakers.
func (c *Config) fuzzyModel(ref string) (SelectedModel, error) {
	q := strings.ToLower(strings.Join(strings.Fields(ref), " "))
	compact := strings.ReplaceAll(q, " ", "")

	type cand struct {
		sel   SelectedModel
		name  string
		score int
	}
	var cands []cand
	for providerID, p := range c.Providers.Seq2() {
		if p.Disable {
			continue
		}
		for _, m := range p.Models {
			id := strings.ToLower(m.ID)
			name := strings.ToLower(m.Name)
			score := 0
			switch {
			case id == compact || name == q:
				score = 4
			case strings.Contains(id, compact) || strings.Contains(name, q):
				score = 3
			case containsAllFields(id, name, q):
				score = 2
			case len(fuzzy.Find(compact, []string{id})) > 0 || len(fuzzy.Find(compact, []string{name})) > 0:
				score = 1
			default:
				continue
			}
			cands = append(cands, cand{sel: SelectedModel{Provider: providerID, Model: m.ID}, name: m.Name, score: score})
		}
	}
	if len(cands) == 0 {
		return SelectedModel{}, fmt.Errorf("unknown model %q: no enabled provider has a model matching it", ref)
	}

	best := 0
	for _, cd := range cands {
		best = max(best, cd.score)
	}
	var top []cand
	for _, cd := range cands {
		if cd.score == best {
			top = append(top, cd)
		}
	}
	if len(top) == 1 {
		return top[0].sel, nil
	}

	// Tie-breakers: role holders, then recents, then shortest id.
	inRole := func(s SelectedModel) bool {
		for _, r := range c.Models {
			if r.Provider == s.Provider && r.Model == s.Model {
				return true
			}
		}
		return false
	}
	inRecent := func(s SelectedModel) bool {
		for _, list := range c.RecentModels {
			for _, r := range list {
				if r.Provider == s.Provider && r.Model == s.Model {
					return true
				}
			}
		}
		return false
	}
	for _, pick := range []func(SelectedModel) bool{inRole, inRecent} {
		var kept []cand
		for _, cd := range top {
			if pick(cd.sel) {
				kept = append(kept, cd)
			}
		}
		if len(kept) == 1 {
			return kept[0].sel, nil
		}
		if len(kept) > 1 {
			top = kept
		}
	}
	slices.SortStableFunc(top, func(a, b cand) int {
		return cmp.Or(cmp.Compare(len(a.sel.Model), len(b.sel.Model)), strings.Compare(a.sel.Provider, b.sel.Provider))
	})
	if len(top[0].sel.Model) < len(top[1].sel.Model) {
		return top[0].sel, nil
	}
	matches := make([]ModelMatch, 0, len(top))
	for _, cd := range top {
		matches = append(matches, ModelMatch{Selection: cd.sel, Name: cd.name})
	}
	return SelectedModel{}, &ErrAmbiguousModelRef{Ref: ref, Matches: matches}
}

// containsAllFields reports whether every whitespace-separated field of q
// appears in id or name ("fable 5.1" against "claude-fable-5-1" fails on
// "5.1", but "fable 5-1" passes).
func containsAllFields(id, name, q string) bool {
	for _, f := range strings.Fields(q) {
		if !strings.Contains(id, f) && !strings.Contains(name, f) {
			return false
		}
	}
	return true
}
