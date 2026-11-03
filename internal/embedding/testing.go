package embedding

import "github.com/taigrr/fantasy"

// SetModelForTest installs model as the embedding model of a Service
// built by [New] or [Build], so tests in other packages can exercise the
// vector signal without a provider. It is a no-op for an inert service.
func SetModelForTest(s Service, model fantasy.EmbeddingModel) {
	if svc, ok := s.(*service); ok && svc.cfg != nil {
		svc.mu.Lock()
		svc.model = model
		svc.mu.Unlock()
	}
}
