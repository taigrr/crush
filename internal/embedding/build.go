package embedding

import "github.com/taigrr/crush/internal/db"

// Params is everything needed to build a Service, resolved by the
// caller from config (provider primitives + the active signature).
type Params struct {
	// Configured reports whether an embedder is set. When false the
	// Service is inert (Embed no-ops, Search degrades to substring).
	Configured bool
	Model      string
	Dimensions int64
	Hybrid     bool
	Signature  string
	Provider   ProviderParams
}

// Build constructs a Service from resolved params. When p.Configured is
// false it returns an inert service that still serves substring search.
func Build(q db.Querier, p Params) Service {
	if !p.Configured {
		return New(q, nil, ProviderParams{}, "")
	}
	return New(q, &Config{
		Model:      p.Model,
		Dimensions: p.Dimensions,
		Hybrid:     p.Hybrid,
	}, p.Provider, p.Signature)
}
