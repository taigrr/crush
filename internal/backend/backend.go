// Package backend provides transport-agnostic operations for managing
// workspaces, sessions, agents, permissions, and events. It is consumed
// by protocol-specific layers such as HTTP (server) and ACP.
package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/editor"
	editornvim "github.com/taigrr/crush/internal/editor/nvim"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/version"
)

// Common errors returned by backend operations.
var (
	ErrWorkspaceNotFound       = errors.New("workspace not found")
	ErrLSPClientNotFound       = errors.New("LSP client not found")
	ErrAgentNotInitialized     = errors.New("agent coordinator not initialized")
	ErrPathRequired            = errors.New("path is required")
	ErrInvalidPermissionAction = errors.New("invalid permission action")
	ErrUnknownCommand          = errors.New("unknown command")
	ErrInvalidClientID         = errors.New("invalid client_id")
	ErrClientNotAttached       = errors.New("client not attached")
)

// DefaultCreateGrace is the window in which a client must open an SSE
// stream after creating a workspace before its creation hold is
// released. Exposed as a package variable so tests can shorten it.
var DefaultCreateGrace = 30 * time.Second

// ShutdownFunc is called when the backend needs to trigger a server
// shutdown (e.g. when the last workspace is removed).
type ShutdownFunc func()

// Backend provides transport-agnostic business logic for the Crush
// server. It manages workspaces and delegates to [app.App] services.
//
// Locking order: when both [Backend.mu] and [Workspace.clientsMu] are
// held at once, [Backend.mu] is acquired first.
type Backend struct {
	workspaces *csync.Map[string, *Workspace]
	// pathIndex maps a resolved absolute workspace path to its
	// workspace ID. Reads and writes are serialised via mu so
	// concurrent CreateWorkspace calls at the same path deduplicate
	// deterministically.
	pathIndex map[string]string
	mu        sync.Mutex

	cfg         *config.ConfigStore
	ctx         context.Context
	shutdownFn  ShutdownFunc
	createGrace time.Duration
}

// clientState tracks one client's claim on a workspace.
//
//   - streams counts the number of live SSE event streams the client
//     currently has open against the workspace.
//   - holdTimer is non-nil iff the client created the workspace but has
//     not yet attached an SSE stream; it fires after createGrace and
//     releases the hold.
//   - currentSessionID records which session this client is currently
//     viewing. Empty string means the client has no session selected
//     (e.g. the landing screen). Cleared automatically when the
//     clientState entry is removed.
//
// streams and holdTimer are mutually exclusive in practice (the hold
// timer is stopped the moment an SSE stream attaches), but both being
// zero/nil means the entry has been released and should be removed.
type clientState struct {
	streams          int
	holdTimer        *time.Timer
	currentSessionID string

	// env is the client's process environment ("KEY=VALUE" form),
	// captured when the client first registers. Used to detect a
	// per-client editor (e.g. Neovim via $NVIM).
	env []string
	// bridge is the client's editor bridge, built lazily from env on
	// first use and cached. Always non-nil once bridgeOnce has run;
	// resolves to editor.Noop when no editor is attached.
	bridge     editor.Bridge
	bridgeOnce bool
}

// closeBridge releases the client's editor bridge if one was built.
// Callers must hold ws.clientsMu.
func (cs *clientState) closeBridge() {
	if cs.bridge != nil {
		_ = cs.bridge.Close()
		cs.bridge = nil
	}
}

// Workspace represents a running [app.App] workspace with its
// associated resources and state.
type Workspace struct {
	*app.App
	ID     string
	Path   string
	Cfg    *config.ConfigStore
	Env    []string
	Skills *skills.Manager

	// resolvedPath is the path used as the dedup key in
	// Backend.pathIndex.
	resolvedPath string

	// clientsMu guards clients. It is held only briefly (no IO).
	clientsMu sync.Mutex
	// clients tracks each client's claim on this workspace. Refcount
	// is a derived value: len(clients).
	clients map[string]*clientState

	// shutdownFn is the function invoked by [Backend.teardown] to
	// release the workspace's underlying resources. It defaults to the
	// embedded [app.App.Shutdown]; tests may override it to avoid
	// driving a full [app.App] through shutdown.
	shutdownFn func()
}

// invokeShutdown calls the workspace shutdown hook if set, falling
// back to the embedded [app.App.Shutdown] when not.
func (w *Workspace) invokeShutdown() {
	if w.shutdownFn != nil {
		w.shutdownFn()
		return
	}
	if w.App != nil {
		w.Shutdown()
	}
}

// New creates a new [Backend].
func New(ctx context.Context, cfg *config.ConfigStore, shutdownFn ShutdownFunc) *Backend {
	return &Backend{
		workspaces:  csync.NewMap[string, *Workspace](),
		pathIndex:   make(map[string]string),
		cfg:         cfg,
		ctx:         ctx,
		shutdownFn:  shutdownFn,
		createGrace: DefaultCreateGrace,
	}
}

// SetCreateGrace overrides the create-grace window. Intended for tests
// that need short timeouts.
func (b *Backend) SetCreateGrace(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.createGrace = d
}

// GetWorkspace retrieves a workspace by ID.
func (b *Backend) GetWorkspace(id string) (*Workspace, error) {
	ws, ok := b.workspaces.Get(id)
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	return ws, nil
}

// ListWorkspaces returns all running workspaces.
func (b *Backend) ListWorkspaces() []proto.Workspace {
	workspaces := []proto.Workspace{}
	for _, ws := range b.workspaces.Seq2() {
		workspaces = append(workspaces, workspaceToProto(ws))
	}
	return workspaces
}

// CreateWorkspace initializes a new workspace from the given
// parameters, or returns an existing workspace if one already exists at
// the same resolved path (first-wins semantics).
//
// args.ClientID must be a valid UUID identifying the calling client;
// the resulting workspace registers a creation hold on behalf of that
// client which is released either by the first SSE attach or by the
// grace window expiring.
func (b *Backend) CreateWorkspace(args proto.Workspace) (*Workspace, proto.Workspace, error) {
	if args.Path == "" {
		return nil, proto.Workspace{}, ErrPathRequired
	}
	clientID, err := validateClientID(args.ClientID)
	if err != nil {
		return nil, proto.Workspace{}, err
	}

	key, err := resolveWorkspaceKey(args.Path)
	if err != nil {
		return nil, proto.Workspace{}, fmt.Errorf("failed to resolve workspace path: %w", err)
	}

	b.mu.Lock()
	if !args.Isolated {
		if existingID, ok := b.pathIndex[key]; ok {
			if ws, found := b.workspaces.Get(existingID); found {
				logFirstWinsMismatch(ws, args)
				b.registerClient(ws, clientID, args.Env)
				b.mu.Unlock()
				return ws, workspaceToProto(ws), nil
			}
			delete(b.pathIndex, key)
		}
	}
	b.mu.Unlock()

	id := uuid.New().String()
	cfg, err := config.Init(args.Path, args.DataDir, args.Debug)
	if err != nil {
		return nil, proto.Workspace{}, fmt.Errorf("failed to initialize config: %w", err)
	}

	cfg.Overrides().SkipPermissionRequests = args.YOLO

	if err := createDotCrushDir(cfg.Config().Options.DataDirectory); err != nil {
		return nil, proto.Workspace{}, fmt.Errorf("failed to create data directory: %w", err)
	}

	conn, err := db.Connect(b.ctx, cfg.Config().Options.DataDirectory, db.WithDataDirLock(true))
	if err != nil {
		return nil, proto.Workspace{}, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Discover skills once per workspace, before app.New. The backend
	// hosts multiple workspaces concurrently, so the manager is
	// constructed WITHOUT WithGlobalMirror to prevent last-writer-wins
	// cross-talk between workspaces.
	discoveryCfg := skillsDiscoveryConfig(cfg)
	allSkills, activeSkills, skillStates := skills.DiscoverFromConfig(discoveryCfg)
	skillsMgr := skills.NewManager(
		allSkills, activeSkills, skillStates,
		skills.WithResolvedPaths(discoveryCfg.ResolvePaths()),
		skills.WithWorkingDir(discoveryCfg.WorkingDir),
	)

	appWorkspace, err := app.New(b.ctx, conn, cfg, skillsMgr)
	if err != nil {
		return nil, proto.Workspace{}, fmt.Errorf("failed to create app workspace: %w", err)
	}

	ws := &Workspace{
		App:          appWorkspace,
		ID:           id,
		Path:         args.Path,
		Cfg:          cfg,
		Env:          args.Env,
		Skills:       skillsMgr,
		resolvedPath: key,
		clients:      make(map[string]*clientState),
	}

	b.mu.Lock()
	if !args.Isolated {
		// Re-check the index under the lock: a concurrent caller may
		// have won the race between the initial unlock and here.
		if existingID, ok := b.pathIndex[key]; ok {
			if existing, found := b.workspaces.Get(existingID); found {
				logFirstWinsMismatch(existing, args)
				b.registerClient(existing, clientID, args.Env)
				b.mu.Unlock()
				ws.invokeShutdown()
				return existing, workspaceToProto(existing), nil
			}
			delete(b.pathIndex, key)
		}
	}
	b.workspaces.Set(id, ws)
	if !args.Isolated {
		b.pathIndex[key] = id
	}
	b.registerClient(ws, clientID, args.Env)
	b.mu.Unlock()

	if args.Version != "" && args.Version != version.Version {
		slog.Warn(
			"Client/server version mismatch",
			"client", args.Version,
			"server", version.Version,
		)
		appWorkspace.SendEvent(util.NewWarnMsg(fmt.Sprintf(
			"Server version %q differs from client version %q. Consider restarting the server.",
			version.Version, args.Version,
		)))
	}

	return ws, workspaceToProto(ws), nil
}

// AttachClient registers a new SSE stream for the given client on the
// workspace. The stream's deferred cleanup must call DetachClient with
// the same arguments to release the claim.
func (b *Backend) AttachClient(workspaceID, clientID string) error {
	if _, err := validateClientID(clientID); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return ErrWorkspaceNotFound
	}

	ws.clientsMu.Lock()
	defer ws.clientsMu.Unlock()
	cs, ok := ws.clients[clientID]
	if !ok {
		ws.clients[clientID] = &clientState{streams: 1}
		return nil
	}
	if cs.holdTimer != nil {
		cs.holdTimer.Stop()
		cs.holdTimer = nil
	}
	cs.streams++
	return nil
}

// DetachClient releases one SSE stream's hold on the workspace.
func (b *Backend) DetachClient(workspaceID, clientID string) {
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return
	}
	b.detachStream(ws, clientID)
}

// releaseHold releases the creation hold for a client, if any.
func (b *Backend) releaseHold(workspaceID, clientID string) error {
	if _, err := validateClientID(clientID); err != nil {
		return err
	}
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return nil
	}
	b.releaseHoldLocked(ws, clientID)
	return nil
}

// registerClient installs (idempotently) the given client's claim on
// the workspace and starts a grace timer if the entry is fresh. env is
// the client's process environment, retained so the client's editor
// bridge can be built lazily on first use.
func (b *Backend) registerClient(ws *Workspace, clientID string, env []string) {
	ws.clientsMu.Lock()
	defer ws.clientsMu.Unlock()
	if _, ok := ws.clients[clientID]; ok {
		return
	}
	cs := &clientState{env: env}
	cs.holdTimer = time.AfterFunc(b.createGrace, func() {
		b.expireHold(ws, clientID, cs)
	})
	ws.clients[clientID] = cs
}

// clientBridge returns the editor bridge for the named client on the
// workspace, building it lazily from the client's captured environment
// on first use. Always returns a non-nil bridge (editor.Noop when the
// client has no attached editor or is unknown).
func (b *Backend) clientBridge(ws *Workspace, clientID string) editor.Bridge {
	ws.clientsMu.Lock()
	cs, ok := ws.clients[clientID]
	if !ok {
		ws.clientsMu.Unlock()
		return editor.Noop{}
	}
	if cs.bridgeOnce {
		bridge := cs.bridge
		ws.clientsMu.Unlock()
		return bridge
	}
	env := cs.env
	ws.clientsMu.Unlock()

	// Dial outside the lock: connecting to the editor performs IO and
	// must not block other clients touching the workspace.
	var bridge editor.Bridge = editor.Noop{}
	if dialed, ok := editornvim.NewFromEnv(env); ok {
		bridge = dialed
		slog.Info("Editor bridge connected", "client", clientID, "editor", "neovim")
	}

	ws.clientsMu.Lock()
	defer ws.clientsMu.Unlock()
	// Re-check: the client may have detached (and been removed) while we
	// dialed, or another caller may have won the race.
	cs, ok = ws.clients[clientID]
	if !ok {
		_ = bridge.Close()
		return editor.Noop{}
	}
	if cs.bridgeOnce {
		_ = bridge.Close()
		return cs.bridge
	}
	cs.bridge = bridge
	cs.bridgeOnce = true
	return bridge
}

func (b *Backend) expireHold(ws *Workspace, clientID string, timer *clientState) {
	ws.clientsMu.Lock()
	cs, ok := ws.clients[clientID]
	if !ok || cs != timer || cs.holdTimer == nil || cs.streams > 0 {
		ws.clientsMu.Unlock()
		return
	}
	cs.holdTimer = nil
	cs.closeBridge()
	delete(ws.clients, clientID)
	teardown := len(ws.clients) == 0
	ws.clientsMu.Unlock()
	if teardown {
		b.teardown(ws)
	}
}

func (b *Backend) releaseHoldLocked(ws *Workspace, clientID string) {
	ws.clientsMu.Lock()
	cs, ok := ws.clients[clientID]
	if !ok {
		ws.clientsMu.Unlock()
		return
	}
	if cs.holdTimer != nil {
		cs.holdTimer.Stop()
		cs.holdTimer = nil
	}
	teardown := false
	if cs.streams == 0 {
		cs.closeBridge()
		delete(ws.clients, clientID)
		teardown = len(ws.clients) == 0
	}
	ws.clientsMu.Unlock()
	if teardown {
		b.teardown(ws)
	}
}

func (b *Backend) detachStream(ws *Workspace, clientID string) {
	ws.clientsMu.Lock()
	cs, ok := ws.clients[clientID]
	if !ok {
		ws.clientsMu.Unlock()
		return
	}
	if cs.streams > 0 {
		cs.streams--
	}
	teardown := false
	if cs.streams == 0 && cs.holdTimer == nil {
		cs.closeBridge()
		delete(ws.clients, clientID)
		teardown = len(ws.clients) == 0
	}
	ws.clientsMu.Unlock()
	if teardown {
		b.teardown(ws)
	}
}

func (b *Backend) teardown(ws *Workspace) {
	b.mu.Lock()
	ws.clientsMu.Lock()
	if len(ws.clients) > 0 {
		ws.clientsMu.Unlock()
		b.mu.Unlock()
		return
	}
	ws.clientsMu.Unlock()
	if existing, ok := b.pathIndex[ws.resolvedPath]; ok && existing == ws.ID {
		delete(b.pathIndex, ws.resolvedPath)
	}
	b.workspaces.Del(ws.ID)
	remaining := b.workspaces.Len()
	b.mu.Unlock()

	ws.invokeShutdown()

	if remaining == 0 && b.shutdownFn != nil {
		slog.Info("Last workspace removed, shutting down server...")
		b.shutdownFn()
	}
}

// DeleteWorkspace releases the named client's creation hold; live
// streams from the same client remain attached until their own
// deferred DetachClient runs.
func (b *Backend) DeleteWorkspace(id, clientID string) error {
	return b.releaseHold(id, clientID)
}

// SetCurrentSession records which session the given client is
// currently viewing within the workspace. Passing an empty sessionID
// clears the client's current-session entry (e.g. the client has
// returned to the landing screen).
//
// The client must be actually attached — i.e. its [clientState] entry
// must exist and have at least one live stream. A bare creation hold
// (streams == 0) is rejected with [ErrClientNotAttached]. This
// guards against zombie writes from a client that has detached and
// against ghost presence from a hold-only client that never opened an
// SSE stream.
func (b *Backend) SetCurrentSession(workspaceID, clientID, sessionID string) error {
	if _, err := validateClientID(clientID); err != nil {
		return err
	}
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return ErrWorkspaceNotFound
	}
	ws.clientsMu.Lock()
	defer ws.clientsMu.Unlock()
	cs, ok := ws.clients[clientID]
	if !ok || cs.streams == 0 {
		// No entry, or hold-only (no live stream): refuse the
		// write. The presence record this is meant to feed
		// should only reflect clients that can actually observe
		// session events.
		return ErrClientNotAttached
	}
	cs.currentSessionID = sessionID
	return nil
}

// AttachedClients returns the number of clients currently viewing
// sessionID in the given workspace. Only clients with at least one live
// SSE stream (streams > 0) AND a matching currentSessionID are counted;
// pure creation holds do not contribute. Returns [ErrWorkspaceNotFound]
// if the workspace is unknown.
func (b *Backend) AttachedClients(workspaceID, sessionID string) (int, error) {
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return 0, ErrWorkspaceNotFound
	}
	return ws.AttachedClientsForSession(sessionID), nil
}

// AttachedClientsForSession returns the number of clients in this
// workspace whose currentSessionID equals sessionID and which have at
// least one live SSE stream. Hold-only clients (streams == 0) do not
// contribute. Acquires the workspace's [clientsMu] briefly; the
// returned count is a point-in-time snapshot.
func (w *Workspace) AttachedClientsForSession(sessionID string) int {
	w.clientsMu.Lock()
	defer w.clientsMu.Unlock()
	n := 0
	for _, cs := range w.clients {
		if cs.streams > 0 && cs.currentSessionID == sessionID {
			n++
		}
	}
	return n
}

// GetWorkspaceProto returns the proto representation of a workspace.
func (b *Backend) GetWorkspaceProto(id string) (proto.Workspace, error) {
	ws, err := b.GetWorkspace(id)
	if err != nil {
		return proto.Workspace{}, err
	}
	return workspaceToProto(ws), nil
}

// VersionInfo returns server version information.
func (b *Backend) VersionInfo() proto.VersionInfo {
	return proto.VersionInfo{
		Version:   version.Version,
		Commit:    version.Commit,
		BuildID:   version.BuildID,
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// Config returns the server-level configuration.
func (b *Backend) Config() *config.ConfigStore {
	return b.cfg
}

// Shutdown initiates a graceful server shutdown.
func (b *Backend) Shutdown() {
	if b.shutdownFn != nil {
		b.shutdownFn()
	}
}

// resolveWorkspaceKey returns a stable canonical form of path suitable
// for use as a dedup key.
func resolveWorkspaceKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// validateClientID returns the trimmed UUID string or an error if the
// input is empty or not a valid UUID.
func validateClientID(id string) (string, error) {
	if id == "" {
		return "", ErrInvalidClientID
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidClientID, err)
	}
	return id, nil
}

func workspaceToProto(ws *Workspace) proto.Workspace {
	cfg := ws.Cfg.Config()
	out := proto.Workspace{
		ID:        ws.ID,
		Path:      ws.Path,
		GitBranch: getGitBranch(ws.Path),
		YOLO:      ws.Cfg.Overrides().SkipPermissionRequests,
		DataDir:   cfg.Options.DataDirectory,
		Debug:     cfg.Options.Debug,
		Config:    cfg,
		Env:       ws.Env,
		Version:   version.Version,
	}
	if ws.Skills != nil {
		out.Skills = skillStatesToProto(ws.Skills.States())
	}
	return out
}

// skillsDiscoveryConfig adapts a *config.ConfigStore to the
// skills.DiscoveryConfig that DiscoverFromConfig consumes.
func skillsDiscoveryConfig(cfg *config.ConfigStore) skills.DiscoveryConfig {
	opts := cfg.Config().Options
	var paths, disabled []string
	if opts != nil {
		paths = opts.SkillsPaths
		disabled = opts.DisabledSkills
	}
	var resolver func(string) (string, error)
	if r := cfg.Resolver(); r != nil {
		resolver = r.ResolveValue
	}
	return skills.DiscoveryConfig{
		SkillsPaths:    paths,
		DisabledSkills: disabled,
		WorkingDir:     cfg.WorkingDir(),
		Resolver:       resolver,
	}
}

// skillStatesToProto converts internal skill discovery states into the
// wire format.
func skillStatesToProto(states []*skills.SkillState) []proto.SkillState {
	if len(states) == 0 {
		return nil
	}
	out := make([]proto.SkillState, len(states))
	for i, s := range states {
		entry := proto.SkillState{
			Name:  s.Name,
			Path:  s.Path,
			State: proto.SkillDiscoveryState(s.State),
		}
		if s.Err != nil {
			entry.Error = s.Err
		}
		out[i] = entry
	}
	return out
}

// logFirstWinsMismatch emits a debug line whenever a second
// CreateWorkspace at the same resolved path arrives with flags that
// differ from the originating workspace.
func logFirstWinsMismatch(existing *Workspace, args proto.Workspace) {
	existingCfg := existing.Cfg.Config()
	existingYOLO := existing.Cfg.Overrides().SkipPermissionRequests
	if existingYOLO == args.YOLO &&
		existingCfg.Options.Debug == args.Debug &&
		existingCfg.Options.DataDirectory == args.DataDir &&
		stringSlicesEqual(existing.Env, args.Env) {
		return
	}
	slog.Debug(
		"Workspace flag mismatch on duplicate create; first wins",
		"workspace_id", existing.ID,
		"path", existing.Path,
		"existing_yolo", existingYOLO,
		"requested_yolo", args.YOLO,
		"existing_debug", existingCfg.Options.Debug,
		"requested_debug", args.Debug,
		"existing_data_dir", existingCfg.Options.DataDirectory,
		"requested_data_dir", args.DataDir,
		"existing_env", existing.Env,
		"requested_env", args.Env,
	)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// getGitBranch returns the current git branch for the given directory.
// Returns empty string if not in a git repo or on error.
func getGitBranch(dir string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
