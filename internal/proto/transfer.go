package proto

import "github.com/taigrr/crush/internal/transfer"

// MoveSessionRequest asks the node owning a session to move it.
type MoveSessionRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
	// TargetNode is the destination cluster node ID.
	TargetNode string                  `json:"target_node"`
	OnConflict transfer.ConflictPolicy `json:"on_conflict,omitempty"`
	// Resume continues a paused in-flight turn on the destination.
	Resume bool `json:"resume,omitempty"`
	// GC garbage-collects the Crush snapshot repository before sending,
	// so unreachable snapshot objects are not shipped.
	GC bool `json:"gc,omitempty"`
}

// MoveSessionResponse reports a completed move.
type MoveSessionResponse struct {
	TargetNode     string        `json:"target_node"`
	TargetNodeName string        `json:"target_node_name"`
	Root           string        `json:"root"`
	SessionID      string        `json:"session_id"`
	Plan           transfer.Plan `json:"plan"`
	BackupPath     string        `json:"backup_path,omitempty"`
	Resumed        bool          `json:"resumed,omitempty"`
}

// TransferPreflightRequest asks a destination how it would receive a
// session, without changing anything.
type TransferPreflightRequest struct {
	Root          string                  `json:"root"`
	SessionID     string                  `json:"session_id"`
	Git           *transfer.GitState      `json:"git,omitempty"`
	SchemaVersion int64                   `json:"schema_version"`
	Version       string                  `json:"version"`
	OnConflict    transfer.ConflictPolicy `json:"on_conflict,omitempty"`
}

// TransferPreflightResponse is the destination's plan.
type TransferPreflightResponse struct {
	Plan     transfer.Plan `json:"plan"`
	NodeID   string        `json:"node_id,omitempty"`
	NodeName string        `json:"node_name,omitempty"`
	// Have lists files the destination already holds, so the sender can
	// skip re-sending identical ones.
	Have []transfer.FileSig `json:"have,omitempty"`
}

// TransferImportResponse reports an applied bundle.
type TransferImportResponse struct {
	Root       string        `json:"root"`
	SessionID  string        `json:"session_id"`
	Plan       transfer.Plan `json:"plan"`
	BackupPath string        `json:"backup_path,omitempty"`
	Resumed    bool          `json:"resumed,omitempty"`
}

// MigrateRequest asks for a migration plan.
type MigrateRequest struct {
	// Targets limits destinations to these node IDs (empty = all online).
	Targets []string `json:"targets,omitempty"`
	// Strategy is "capacity" (default) or "even".
	Strategy string `json:"strategy,omitempty"`
	// AllSessions includes idle sessions of attached workspaces, not only
	// running jobs.
	AllSessions bool                    `json:"all_sessions,omitempty"`
	OnConflict  transfer.ConflictPolicy `json:"on_conflict,omitempty"`
}

// PlannedMove is one session's destination.
type PlannedMove struct {
	WorkspaceID string                  `json:"workspace_id"`
	Root        string                  `json:"root"`
	SessionID   string                  `json:"session_id"`
	Title       string                  `json:"title"`
	Busy        bool                    `json:"busy,omitempty"`
	TargetNode  string                  `json:"target_node"`
	TargetName  string                  `json:"target_name"`
	OnConflict  transfer.ConflictPolicy `json:"on_conflict,omitempty"`
}

// MigrationPlan is a set of moves.
type MigrationPlan struct {
	Strategy string        `json:"strategy"`
	Source   string        `json:"source"`
	Moves    []PlannedMove `json:"moves"`
}

// MigrationOutcome is one executed move.
type MigrationOutcome struct {
	PlannedMove
	Plan    transfer.Plan `json:"plan,omitempty"`
	Resumed bool          `json:"resumed,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// MigrationResult reports an executed plan.
type MigrationResult struct {
	Moved    int                `json:"moved"`
	Outcomes []MigrationOutcome `json:"outcomes"`
}

// MigrateExecuteRequest runs a previously returned plan.
type MigrateExecuteRequest struct {
	Plan   MigrationPlan `json:"plan"`
	Resume bool          `json:"resume,omitempty"`
}

// TransferStatus is a move in progress on the source node.
type TransferStatus struct {
	SessionID  string `json:"session_id"`
	Title      string `json:"title"`
	Root       string `json:"root"`
	TargetName string `json:"target_name"`
	Stage      string `json:"stage"`
	Files      int    `json:"files"`
	TotalFiles int    `json:"total_files"`
	Bytes      int64  `json:"bytes"`
	TotalBytes int64  `json:"total_bytes"`
	// CurrentFile is the project-relative path being sent right now.
	CurrentFile string `json:"current_file,omitempty"`
	StartedAt   int64  `json:"started_at"` // Unix ms
}
