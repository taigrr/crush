package config

import (
	"fmt"
	"slices"
	"strings"
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
