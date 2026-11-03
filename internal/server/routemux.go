package server

import "net/http"

// routeMux is an [http.ServeMux] that remembers every pattern registered
// on it, so tests can check each served route against the device scope
// table (net/http offers no way to list a mux's patterns).
type routeMux struct {
	*http.ServeMux
	patterns []string
}

func newRouteMux() *routeMux { return &routeMux{ServeMux: http.NewServeMux()} }

// Handle registers handler for pattern.
func (m *routeMux) Handle(pattern string, handler http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.Handle(pattern, handler)
}

// HandleFunc registers handler for pattern.
func (m *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(handler))
}
