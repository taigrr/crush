package proto

// ProtocolVersion is bumped whenever the client/server wire contract
// changes in a way an older server cannot serve (a new endpoint the
// client relies on, an incompatible payload). A client that finds a
// running server on a different protocol version must not attempt a
// graceful drain handoff — the old server may not even have the drain
// endpoint — and falls back to a forced restart.
//
// History:
//   - 1: initial value, predates the constant (servers report 0).
//   - 2: POST /v1/drain, proto.Health body on GET /v1/health, and
//     proto.Error.Code.
const ProtocolVersion = 2

// MinDrainProtocolVersion is the oldest server protocol that implements
// POST /v1/drain and the proto.Health body. A client asked to update a
// server older than this cannot drain it and must force-restart.
const MinDrainProtocolVersion = 2

// VersionInfo represents version information about the server.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildID   string `json:"build_id"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
	// ProtocolVersion is the server's [ProtocolVersion]. Servers that
	// predate the field report 0.
	ProtocolVersion int `json:"protocol_version"`
}
