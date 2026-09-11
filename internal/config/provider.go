package config

import (
	"sync"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/catwalk/pkg/embedded"
)

var (
	providerOnce sync.Once
	providerList []catwalk.Provider
)

// UpdateProviders is a no-op kept for CLI compatibility.
func UpdateProviders(_ string) error {
	return nil
}

// UpdateHyper is a no-op kept for CLI compatibility.
func UpdateHyper(_ string) error {
	return nil
}

// Providers returns the compiled-in provider catalog. No network calls, no
// server dependency, no local files. Custom providers from config are merged
// by the caller.
//
// When options.disable_default_providers is set, the embedded catalog is
// suppressed and this returns nothing: the flag's contract is that only
// explicitly-configured providers exist. Honoring it here is what keeps the
// model picker and other catalog consumers in sync with provider loading in
// [Config.setupProviders], which already skips the embedded set. Without this
// the picker still offers every embedded provider, and selecting a model from
// one prompts for an API key the user never intended to supply.
func Providers(cfg *Config) ([]catwalk.Provider, error) {
	if cfg != nil && cfg.Options != nil && cfg.Options.DisableDefaultProviders {
		return nil, nil
	}
	providerOnce.Do(func() {
		providerList = embedded.GetAll()
	})
	return providerList, nil
}
