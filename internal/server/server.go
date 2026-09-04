package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/server/swaggerui"
	_ "github.com/taigrr/crush/internal/swagger"
)

// ErrServerClosed is returned when the server is closed.
var ErrServerClosed = http.ErrServerClosed

// ParseHostURL parses a host URL into a [url.URL].
func ParseHostURL(host string) (*url.URL, error) {
	proto, addr, ok := strings.Cut(host, "://")
	if !ok {
		return nil, fmt.Errorf("invalid host format: %s", host)
	}

	var basePath string
	if proto == "tcp" {
		parsed, err := url.Parse("tcp://" + addr)
		if err != nil {
			return nil, fmt.Errorf("invalid tcp address: %v", err)
		}
		addr = parsed.Host
		basePath = parsed.Path
	}
	return &url.URL{
		Scheme: proto,
		Host:   addr,
		Path:   basePath,
	}, nil
}

// DefaultHost returns the default server host.
func DefaultHost() string {
	sock := "crush.sock"
	usr, err := user.Current()
	if err == nil && usr.Uid != "" {
		sock = fmt.Sprintf("crush-%s.sock", usr.Uid)
	}
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("npipe:////./pipe/%s", sock)
	}
	return fmt.Sprintf("unix:///tmp/%s", sock)
}

// Server represents a Crush server bound to a specific address.
type Server struct {
	// Addr can be a TCP address, a Unix socket path, or a Windows named pipe.
	Addr    string
	network string

	h  *http.Server
	ln net.Listener

	backend *backend.Backend
	logger  *slog.Logger
}

// SetLogger sets the logger for the server.
func (s *Server) SetLogger(logger *slog.Logger) {
	s.logger = logger
}

// NewServer creates a new [Server] with the given network and address.
func NewServer(cfg *config.ConfigStore, network, address string) *Server {
	s := new(Server)
	s.Addr = address
	s.network = network

	// The backend is created with a shutdown callback that stops the
	// HTTP server. The control-command path (Backend.Shutdown) has
	// already torn down workspaces and cancelled in-flight runs before
	// invoking this, so Stop only needs to drain/close HTTP.
	s.backend = backend.New(context.Background(), cfg, func() {
		go s.stopHTTP()
	})
	s.installHandler()
	if network == "tcp" {
		s.h.Addr = address
	}
	return s
}

// installHandler builds the protocol/router around s.backend and
// assigns the resulting http.Server to s.h. Extracted from
// [NewServer] so test harnesses can wire a Server around a
// pre-constructed backend.
func (s *Server) installHandler() {
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	c := &controllerV1{backend: s.backend, server: s}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", c.handleGetHealth)
	mux.HandleFunc("GET /v1/version", c.handleGetVersion)
	mux.HandleFunc("GET /v1/config", c.handleGetConfig)
	mux.HandleFunc("POST /v1/control", c.handlePostControl)
	mux.HandleFunc("POST /v1/drain", c.handlePostDrain)
	mux.HandleFunc("GET /v1/workspaces", c.handleGetWorkspaces)
	mux.HandleFunc("GET /v1/workspace-overviews", c.handleGetWorkspaceOverviews)
	mux.HandleFunc("POST /v1/peek-messages", c.handlePostPeekMessages)
	mux.HandleFunc("POST /v1/peek-session-info", c.handlePostPeekSessionInfo)
	mux.HandleFunc("POST /v1/workspaces", c.handlePostWorkspaces)
	mux.HandleFunc("DELETE /v1/workspaces/{id}", c.handleDeleteWorkspaces)
	mux.HandleFunc("POST /v1/workspaces/{id}/current-session", c.handlePostWorkspaceCurrentSession)
	mux.HandleFunc("GET /v1/workspaces/{id}", c.handleGetWorkspace)
	mux.HandleFunc("GET /v1/workspaces/{id}/config", c.handleGetWorkspaceConfig)
	mux.HandleFunc("GET /v1/workspaces/{id}/events", c.handleGetWorkspaceEvents)
	mux.HandleFunc("GET /v1/events", c.handleGetGlobalEvents)
	mux.HandleFunc("GET /v1/workspaces/{id}/providers", c.handleGetWorkspaceProviders)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions", c.handleGetWorkspaceSessions)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions", c.handlePostWorkspaceSessions)
	mux.HandleFunc("GET /v1/session-import/sources", c.handleGetSessionImportSources)
	mux.HandleFunc("GET /v1/session-import/candidates", c.handleGetSessionImportCandidates)
	mux.HandleFunc("POST /v1/workspaces/{id}/session-import", c.handlePostSessionImport)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}", c.handleGetWorkspaceSession)
	mux.HandleFunc("PUT /v1/workspaces/{id}/sessions/{sid}", c.handlePutWorkspaceSession)
	mux.HandleFunc("DELETE /v1/workspaces/{id}/sessions/{sid}", c.handleDeleteWorkspaceSession)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions/{sid}/archive", c.handleArchiveWorkspaceSession)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions/{sid}/unarchive", c.handleUnarchiveWorkspaceSession)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions/{sid}/seen", c.handleMarkWorkspaceSessionSeen)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions/{sid}/favorite", c.handleSetWorkspaceSessionFavorite)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/archived", c.handleGetWorkspaceArchivedSessions)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/history", c.handleGetWorkspaceSessionHistory)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/messages", c.handleGetWorkspaceSessionMessages)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/messages/user", c.handleGetWorkspaceSessionUserMessages)
	mux.HandleFunc("GET /v1/workspaces/{id}/messages/user", c.handleGetWorkspaceAllUserMessages)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/filetracker/files", c.handleGetWorkspaceSessionFileTrackerFiles)
	mux.HandleFunc("POST /v1/workspaces/{id}/filetracker/read", c.handlePostWorkspaceFileTrackerRead)
	mux.HandleFunc("GET /v1/workspaces/{id}/filetracker/lastread", c.handleGetWorkspaceFileTrackerLastRead)
	mux.HandleFunc("GET /v1/workspaces/{id}/embeddings/pending", c.handleGetWorkspaceEmbeddingsPending)
	mux.HandleFunc("POST /v1/workspaces/{id}/embeddings/backfill", c.handlePostWorkspaceEmbeddingsBackfill)
	mux.HandleFunc("GET /v1/workspaces/{id}/embeddings/status", c.handleGetWorkspaceEmbeddingsStatus)
	mux.HandleFunc("POST /v1/workspaces/{id}/history/search", c.handlePostWorkspaceHistorySearch)
	mux.HandleFunc("GET /v1/workspaces/{id}/lsps", c.handleGetWorkspaceLSPs)
	mux.HandleFunc("GET /v1/workspaces/{id}/lsps/{lsp}/diagnostics", c.handleGetWorkspaceLSPDiagnostics)
	mux.HandleFunc("POST /v1/workspaces/{id}/lsps/start", c.handlePostWorkspaceLSPStart)
	mux.HandleFunc("POST /v1/workspaces/{id}/lsps/stop", c.handlePostWorkspaceLSPStopAll)
	mux.HandleFunc("GET /v1/workspaces/{id}/permissions/skip", c.handleGetWorkspacePermissionsSkip)
	mux.HandleFunc("POST /v1/workspaces/{id}/permissions/skip", c.handlePostWorkspacePermissionsSkip)
	mux.HandleFunc("GET /v1/workspaces/{id}/permissions/sysadmin", c.handleGetWorkspacePermissionsSysadmin)
	mux.HandleFunc("POST /v1/workspaces/{id}/permissions/sysadmin", c.handlePostWorkspacePermissionsSysadmin)
	mux.HandleFunc("POST /v1/workspaces/{id}/permissions/grant", c.handlePostWorkspacePermissionsGrant)
	mux.HandleFunc("POST /v1/workspaces/{id}/questions/answer", c.handlePostWorkspaceQuestionsAnswer)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent", c.handleGetWorkspaceAgent)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent", c.handlePostWorkspaceAgent)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/init", c.handlePostWorkspaceAgentInit)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/update", c.handlePostWorkspaceAgentUpdate)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/sessions/{sid}", c.handleGetWorkspaceAgentSession)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/cancel", c.handlePostWorkspaceAgentSessionCancel)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/interrupt", c.handlePostWorkspaceAgentSessionInterrupt)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/tools/{tcid}/background", c.handlePostWorkspaceAgentSessionToolBackground)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/cancel", c.handlePostWorkspaceAgentCancel)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/queued", c.handleGetWorkspaceAgentSessionPromptQueued)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/list", c.handleGetWorkspaceAgentSessionPromptList)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/prompts/clear", c.handlePostWorkspaceAgentSessionPromptClear)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/summarize", c.handlePostWorkspaceAgentSessionSummarize)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/title", c.handlePostWorkspaceAgentSessionTitle)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/sessions/{sid}/goal", c.handleGetWorkspaceAgentSessionGoal)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/goal", c.handlePostWorkspaceAgentSessionGoal)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/goal/clear", c.handlePostWorkspaceAgentSessionGoalClear)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/cwd", c.handlePostWorkspaceAgentSessionWorkingDir)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/shell", c.handlePostWorkspaceAgentSessionShell)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/default-small-model", c.handleGetWorkspaceAgentDefaultSmallModel)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/set", c.handlePostWorkspaceConfigSet)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/remove", c.handlePostWorkspaceConfigRemove)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/model", c.handlePostWorkspaceConfigModel)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/compact", c.handlePostWorkspaceConfigCompact)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/provider-key", c.handlePostWorkspaceConfigProviderKey)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/import-copilot", c.handlePostWorkspaceConfigImportCopilot)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/refresh-oauth", c.handlePostWorkspaceConfigRefreshOAuth)
	mux.HandleFunc("POST /v1/workspaces/{id}/config/reload", c.handlePostWorkspaceConfigReload)
	mux.HandleFunc("GET /v1/workspaces/{id}/project/needs-init", c.handleGetWorkspaceProjectNeedsInit)
	mux.HandleFunc("POST /v1/workspaces/{id}/project/init", c.handlePostWorkspaceProjectInit)
	mux.HandleFunc("GET /v1/workspaces/{id}/project/init-prompt", c.handleGetWorkspaceProjectInitPrompt)
	mux.HandleFunc("GET /v1/workspaces/{id}/skills", c.handleGetWorkspaceSkills)
	mux.HandleFunc("POST /v1/workspaces/{id}/skills/read", c.handlePostWorkspaceSkillRead)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/refresh-tools", c.handlePostWorkspaceMCPRefreshTools)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/read-resource", c.handlePostWorkspaceMCPReadResource)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/get-prompt", c.handlePostWorkspaceMCPGetPrompt)
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/states", c.handleGetWorkspaceMCPStates)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/refresh-prompts", c.handlePostWorkspaceMCPRefreshPrompts)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/refresh-resources", c.handlePostWorkspaceMCPRefreshResources)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/docker/enable", c.handlePostWorkspaceMCPEnableDocker)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/docker/disable", c.handlePostWorkspaceMCPDisableDocker)
	mux.HandleFunc("POST /v1/workspaces/{id}/mcp/authenticate", c.handlePostWorkspaceMCPAuthenticate)
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/pending-auth", c.handleGetWorkspaceMCPPendingAuth)
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/auth-url", c.handleGetWorkspaceMCPAuthURL)
	mux.HandleFunc("GET /v1/workspaces/{id}/skills/states", c.handleGetWorkspaceSkillsStates)
	mux.HandleFunc("GET /v1/workspaces/{id}/snapshots/enabled", c.handleGetWorkspaceSnapshotsEnabled)
	mux.HandleFunc("GET /v1/workspaces/{id}/messages/{msgid}/snapshot", c.handleGetWorkspaceSnapshotByMessage)
	mux.HandleFunc("POST /v1/workspaces/{id}/snapshots/gc", c.handlePostWorkspaceSnapshotGC)
	mux.HandleFunc("GET /v1/workspaces/{id}/snapshots/stats", c.handleGetWorkspaceSnapshotStats)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/snapshots", c.handleGetWorkspaceSnapshots)
	mux.HandleFunc("GET /v1/workspaces/{id}/snapshots/{snapid}", c.handleGetWorkspaceSnapshot)
	mux.HandleFunc("POST /v1/workspaces/{id}/snapshots/{snapid}/restore", c.handlePostWorkspaceSnapshotRestore)
	mux.HandleFunc("GET /v1/workspaces/{id}/snapshots/{snapid}/diff", c.handleGetWorkspaceSnapshotDiff)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/milestones", c.handleGetWorkspaceMilestones)
	mux.HandleFunc("GET /v1/workspaces/{id}/worktrees/enabled", c.handleGetWorkspaceWorktreesEnabled)
	mux.HandleFunc("GET /v1/workspaces/{id}/worktrees", c.handleGetAllWorkspaceWorktrees)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/worktrees", c.handleGetWorkspaceWorktrees)
	mux.HandleFunc("GET /v1/workspaces/{id}/worktrees/{wtid}", c.handleGetWorkspaceWorktree)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/worktrees/active", c.handleGetWorkspaceActiveWorktree)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions/{sid}/worktrees", c.handlePostWorkspaceWorktree)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions/{sid}/worktrees/{wtid}/switch", c.handlePostWorkspaceWorktreeSwitch)
	mux.HandleFunc("POST /v1/workspaces/{id}/worktrees/{wtid}/merge", c.handlePostWorkspaceWorktreeMerge)
	mux.HandleFunc("DELETE /v1/workspaces/{id}/worktrees/{wtid}", c.handleDeleteWorkspaceWorktree)
	mux.HandleFunc("GET /v1/workspaces/{id}/git/branches", c.handleGetWorkspaceGitBranches)
	mux.HandleFunc("POST /v1/workspaces/{id}/fork", c.handlePostWorkspaceFork)
	mux.Handle("/v1/docs/", swaggerui.Handler("/v1/docs/"))
	s.h = &http.Server{
		Protocols: &p,
		Handler:   s.recoverHandler(s.loggingHandler(mux)),
	}
}

// Handler returns the server's HTTP handler. Exposed so test harnesses
// can wrap it in an httptest.Server without going through the
// production listener setup.
func (s *Server) Handler() http.Handler {
	return s.h.Handler
}

// Serve accepts incoming connections on the listener.
func (s *Server) Serve(ln net.Listener) error {
	return s.h.Serve(ln)
}

// ListenAndServe starts the server and begins accepting connections.
func (s *Server) ListenAndServe() error {
	if s.ln != nil {
		return fmt.Errorf("server already started")
	}
	ln, err := listen(s.network, s.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.Addr, err)
	}
	return s.Serve(ln)
}

func (s *Server) closeListener() {
	if s.ln != nil {
		s.ln.Close()
		s.ln = nil
	}
}

// Close force closes all listeners and connections.
func (s *Server) Close() error {
	defer func() { s.closeListener() }()
	return s.h.Close()
}

// Shutdown gracefully shuts down the server without interrupting active
// connections.
func (s *Server) Shutdown(ctx context.Context) error {
	defer func() { s.closeListener() }()
	return s.h.Shutdown(ctx)
}

// Stop performs an immediate, complete shutdown: it tears down every
// workspace (cancelling in-flight agent runs and marking streaming tool
// calls cancelled) and then stops the HTTP server. Use this for
// signal-driven shutdown (SIGINT/SIGTERM). It is synchronous so callers
// can return only once teardown has finished.
func (s *Server) Stop() {
	s.backend.ShutdownWorkspaces()
	s.stopHTTP()
}

// stopHTTP drains the HTTP server. Long-lived SSE streams block on their
// request context, which a graceful Shutdown never cancels, so a purely
// graceful Shutdown would hang. Attempt a brief graceful drain, then
// force-close so the process exits promptly.
func (s *Server) stopHTTP() {
	slog.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		slog.Warn("Graceful shutdown timed out; forcing close", "error", err)
		if cerr := s.Close(); cerr != nil {
			slog.Error("Failed to force-close server", "error", cerr)
		}
	}
}

func (s *Server) logError(r *http.Request, msg string, args ...any) {
	if s.logger != nil {
		s.logger.With(
			slog.String("method", r.Method),
			slog.String("url", r.URL.String()),
			slog.String("remote_addr", r.RemoteAddr),
		).Error(msg, args...)
	}
}

func (s *Server) logInfo(r *http.Request, msg string, args ...any) {
	if s.logger != nil {
		s.logger.With(
			slog.String("method", r.Method),
			slog.String("url", r.URL.String()),
			slog.String("remote_addr", r.RemoteAddr),
		).Info(msg, args...)
	}
}
