package proto

// ServerConfig is the redacted view of a server's configuration served
// by GET /v1/config. It deliberately carries no provider settings, so
// API keys never leave the node.
type ServerConfig struct {
	// Configured reports whether at least one AI provider is enabled.
	Configured bool `json:"configured"`
}
