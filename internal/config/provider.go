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
// When cfg sets options.disable_default_providers, no embedded providers are
// returned so that every catalog consumer (model picker, CLI listings,
// provider setup) agrees on the set of available providers. A nil cfg or nil
// Options yields the full embedded catalog.
func Providers(cfg *Config) ([]catwalk.Provider, error) {
	if cfg != nil && cfg.Options != nil && cfg.Options.DisableDefaultProviders {
		return nil, nil
	}
	providerOnce.Do(func() {
		providerList = embedded.GetAll()
	})
	return providerList, nil
}
