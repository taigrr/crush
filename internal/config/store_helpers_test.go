package config

// withConfig sets the store's config pointer and returns the store, so tests
// can keep building ConfigStore values with struct literals.
func withConfig(s *ConfigStore, cfg *Config) *ConfigStore {
	s.config.Store(cfg)
	return s
}
