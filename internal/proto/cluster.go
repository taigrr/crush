package proto

import "github.com/taigrr/crush/internal/cluster"

// ClusterInitRequest creates a new cluster on the server.
type ClusterInitRequest struct {
	// Endpoints overrides the auto-detected advertised addresses.
	Endpoints []cluster.Endpoint `json:"endpoints,omitempty"`
}

// ClusterJoinRequest asks the server to join an existing cluster.
type ClusterJoinRequest struct {
	// Addr is a member's cluster address (host or host:port).
	Addr  string `json:"addr"`
	Token string `json:"token"`
	// Endpoints overrides the auto-detected advertised addresses.
	Endpoints []cluster.Endpoint `json:"endpoints,omitempty"`
	// TimeoutSeconds bounds the wait for approval (default 10 minutes).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// ClusterRefRequest names a node, join request, or new node name.
type ClusterRefRequest struct {
	Ref string `json:"ref"`
}

// ClusterUpgradeRequest asks every member to install a release.
type ClusterUpgradeRequest struct {
	// Version is the release tag; empty means latest.
	Version string `json:"version,omitempty"`
}

// ClusterTokenResponse carries a join token and per-member errors from
// a broadcast operation.
type ClusterTokenResponse struct {
	Token  string   `json:"token,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// ClusterPingResult is one member's reachability.
type ClusterPingResult struct {
	NodeID    string `json:"node_id"`
	Name      string `json:"name"`
	RTTMillis int64  `json:"rtt_ms"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}
