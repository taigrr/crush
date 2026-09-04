package config

import (
	"fmt"
	"slices"
	"strings"
)

// ResolveModelRef maps a model reference, as typed by a person or emitted
// by a model in a tool call, to a concrete selection against the current
// config. It is the grammar of the agent and review tools' `model`
// parameter.
//
// Accepted forms, tried in this order:
//
//	"<role>"           — a key of models: large, small, worker, or
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

	// Role validation and provider/bare-id resolution all consult the
	// provider catalog. A config deserialized without a providers map
	// (e.g. over the wire) leaves it nil; report a clean error rather
	// than dereferencing it (GetModel / Providers.Get would panic).
	if c.Providers == nil {
		return SelectedModel{}, fmt.Errorf("unknown model %q: no providers are configured", ref)
	}

	// Role names first. A role that is configured but points at an
	// unknown model is a config error the caller should see, not a
	// silent fall-through to bare-id matching.
	role := SelectedModelType(ref)
	if _, ok := c.Models[role]; !ok {
		// Role names are matched case-insensitively ("Large" == "large").
		for r := range c.Models {
			if strings.EqualFold(string(r), ref) {
				role = r
				break
			}
		}
	}
	if sel, ok := c.Models[role]; ok {
		if sel.Provider == "" || sel.Model == "" {
			return SelectedModel{}, fmt.Errorf("role %q is configured without a provider/model", ref)
		}
		if c.EnabledModel(sel.Provider, sel.Model) == nil {
			return SelectedModel{}, fmt.Errorf("role %q points at %s/%s, which no enabled provider offers", ref, sel.Provider, sel.Model)
		}
		return sel, nil
	}

	// Qualified form. When the prefix names a configured provider the
	// reference is treated as fully qualified and resolved only there: the
	// caller named a vendor, and silently answering with a same-named id on
	// another provider is exactly the mis-resolution the tool grammar must
	// not make. Ids that themselves contain a slash (openrouter's
	// "anthropic/claude-x") are reached when the prefix is NOT a configured
	// provider, via the bare-id scan below.
	if providerID, modelID, ok := strings.Cut(ref, "/"); ok {
		if providerCfg, found := c.Providers.Get(providerID); found {
			sel, err := resolveInProvider(providerID, providerCfg, modelID)
			if err == nil {
				return sel, nil
			}
			// Do not fall through to another vendor, but do point at one
			// when the whole string is a model id elsewhere (openrouter's
			// "anthropic/claude-x" with an "anthropic" provider configured).
			if alt := c.providersOfferingID(ref); len(alt) > 0 {
				return SelectedModel{}, fmt.Errorf("%w; did you mean %s/%s?", err, alt[0], ref)
			}
			return SelectedModel{}, err
		}
	}

	// Bare id: exact match first, then case-insensitive so a typed
	// "GPT-4o" still resolves when only one model matches.
	var exact, folded []SelectedModel
	for providerID, providerCfg := range c.Providers.Seq2() {
		if providerCfg.Disable {
			continue
		}
		for _, m := range providerCfg.Models {
			switch {
			case m.ID == ref:
				exact = append(exact, SelectedModel{Provider: providerID, Model: m.ID})
			case strings.EqualFold(m.ID, ref):
				folded = append(folded, SelectedModel{Provider: providerID, Model: m.ID})
			}
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = folded
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

// providersOfferingID returns the enabled providers whose catalog lists id
// verbatim, sorted for deterministic messages.
func (c *Config) providersOfferingID(id string) []string {
	var out []string
	for providerID, providerCfg := range c.Providers.Seq2() {
		if providerCfg.Disable {
			continue
		}
		for _, m := range providerCfg.Models {
			if m.ID == id {
				out = append(out, providerID)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}
