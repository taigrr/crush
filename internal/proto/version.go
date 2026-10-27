package proto

// VersionInfo represents version information about the server.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildID   string `json:"build_id"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
	Protocol  int    `json:"protocol,omitempty"`
	// Persistent is true when the server runs as a daemon that does not
	// exit after its last workspace closes.
	Persistent bool `json:"persistent,omitempty"`
}

// ProtocolVersion is the client/server API protocol version. Bump it on
// incompatible API changes.
const ProtocolVersion = 1
