package proto

// ServerControl represents a server control request.
type ServerControl struct {
	Command string `json:"command"`
}

// Health is the server's liveness snapshot returned by GET /v1/health
// and POST /v1/drain.
type Health struct {
	Status string `json:"status"`
	// Draining is true once the server has been asked to drain for an
	// update: it finishes in-flight runs but accepts no new ones, and
	// exits on its own when ActiveRuns reaches zero.
	Draining bool `json:"draining"`
	// ActiveRuns is the number of sessions, across all workspaces, with
	// an active or accepted-but-not-yet-active agent run. A run blocked
	// on a permission or question prompt counts.
	ActiveRuns int `json:"active_runs"`
}

// ErrorCode values carried on proto.Error so clients can react to
// specific server conditions without parsing the message text.
const (
	// ErrorCodeDraining marks a request rejected because the server is
	// draining for an update. Clients should retry against the
	// replacement server.
	ErrorCodeDraining = "draining"
)
