// Package backend provides transport-agnostic operations for managing
// workspaces, sessions, agents, permissions, and events. It is consumed
// by protocol-specific layers such as HTTP (server) and ACP.
package backend

import (
	"context"
	"database/sql"
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
	"github.com/taigrr/crush/internal/home"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/registry"
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
	ErrWorkspaceClosing        = errors.New("workspace closing")
	// ErrToolCallNotBackgroundable is returned by BackgroundToolCall when
	// the named tool call is unknown, already finished, does not support
	// moving to the background, or belongs to another session.
	ErrToolCallNotBackgroundable = errors.New("tool call cannot be moved to the background")
)

// DefaultCreateGrace is the window in which a client must open an SSE
// stream after creating a workspace before its creation hold is
// released. Exposed as a package variable so tests can shorten it.
var DefaultCreateGrace = 30 * time.Second

// DefaultReconnectGrace is the window an already-attached client has to
// re-open its event stream after the last one drops before the
// workspace is torn down. Without it, a transient SSE blip (an
// "unexpected EOF" or the brief gap during a workspace switch) drops
// the stream count to zero and, if this is the last workspace, shuts
// the whole server down out from under a still-running client — which
// then spins in its reconnect backoff against a dead socket. Exposed
// as a package variable so tests can shorten it.
var DefaultReconnectGrace = 2 * time.Minute

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

	cfg            *config.ConfigStore
	ctx            context.Context
	shutdownFn     ShutdownFunc
	createGrace    time.Duration
	reconnectGrace time.Duration

	// registry is the global, cross-workspace index of workspace roots
	// used so the picker (and server on startup) can enumerate
	// previously used workspaces without attaching them.
	registry *registry.Store

	// attention is the global, cross-workspace attention channel. Every
	// workspace's permission/question blocked+resolved transitions and
	// agent busy/idle transitions are republished here, tagged with the
	// originating workspace, so a single observe-only client stream
	// (GET /v1/events) can surface any background session's state
	// without attaching to its workspace. Lives for the backend's
	// lifetime, independent of individual workspace teardown.
	attention *pubsub.Broker[proto.AttentionEvent]

	// yoloByRoot remembers, per resolved project root, whether the user
	// turned on yolo (permission skip-requests) at runtime. Yolo is
	// otherwise stored only on the live workspace's config-store override
	// and permission service, both of which are destroyed when a
	// workspace idle-teardown fires. Without this, enabling yolo on a
	// workspace and then letting it autoclose (e.g. by switching the TUI
	// to another workspace) would silently lose the setting on the next
	// recreate, which rebuilds solely from the CLI --yolo flag. Guarded
	// by [Backend.mu]. Backend-scoped (not persisted to disk), so it
	// survives autoclose within a running server but not a full restart —
	// yolo is a deliberately explicit, dangerous mode, so it is not
	// silently persisted across restarts.
	yoloByRoot map[string]bool

	// drain is non-nil once Drain has been called. Guarded by mu. See
	// drain.go.
	drain *drainState

	// startedAt is when this backend was created; connectWorkspaceDB
	// uses it to decide whether a locked data directory may still be a
	// predecessor releasing it.
	startedAt time.Time
}

// clientState tracks one client's claim on a workspace.
//
//   - streams counts the number of live SSE event streams the client
//     currently has open against the workspace.
//   - holdTimer is non-nil while the client's claim is being held open
//     on a timer without a live SSE stream: either after creation
//     before the first stream attaches (fires after createGrace), or
//     after the last stream drops while awaiting a reconnect (fires
//     after reconnectGrace). In both cases it releases the hold when it
//     fires and is stopped the moment a stream (re)attaches.
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
	// cwd is the client's original working directory at registration
	// time, captured before workspace dedup collapses it to the
	// project root. Used to auto-detect which managed worktree (if
	// any) the client is operating from when [Backend.SetCurrentSession]
	// fires, so the session's active worktree follows the user's cwd
	// without an explicit `/worktree switch`.
	cwd string
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

	// ctx is the workspace-scoped run context. It is derived from
	// the backend context in CreateWorkspace and lives for the
	// lifetime of the workspace; cancel tears it down. Agent runs
	// dispatched on behalf of this workspace are bound to ctx so
	// their lifetime is owned by the workspace, not by any single
	// client's HTTP request.
	ctx    context.Context
	cancel context.CancelFunc

	// isolated mirrors proto.Workspace.Isolated: a private workspace
	// (e.g. `crush run`) that shares its data directory — and so its
	// journal tables — with the path-deduplicated workspace for the
	// same project. It must never replay that journal.
	isolated bool

	// initMu serializes InitAgent so two concurrent /agent/init calls
	// on an unconfigured workspace cannot both build a coordinator (and
	// both replay the journal).
	initMu sync.Mutex

	// runMu guards closing and gates dispatch of new agent runs.
	// closing is set by Shutdown so no new runs are accepted once
	// teardown has begun. runWG tracks dispatched agent goroutines
	// so Shutdown can wait for them to return before app cleanup.
	runMu   sync.Mutex
	closing bool
	runWG   sync.WaitGroup
	// liveRuns counts runAgent goroutines between dispatch and return.
	// Unlike the coordinator's busy predicates it has no gaps: a goal
	// or require_reply continuation passes through a window where the
	// session is neither active, accepted, nor queued, and a drain must
	// not mistake that instant for idle. Guarded by runMu.
	liveRuns int

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

	// busyFn reports whether the workspace has an in-flight agent run.
	// It defaults to consulting the agent coordinator; tests may
	// override it to simulate a busy workspace without a full [app.App].
	busyFn func() bool
	// busySessionsFn lists the sessions with an in-flight run. It
	// defaults to consulting the agent coordinator; tests may override
	// it alongside busyFn.
	busySessionsFn func() []string

	// attnCancel stops the attention forwarder goroutine and attnWG
	// waits for it to drain. See startAttentionForwarder.
	attnCancel context.CancelFunc
	attnWG     sync.WaitGroup
}

// agentBusy reports whether the workspace currently has an in-flight
// agent run. A busy workspace must not be torn down when its last client
// detaches: the run is bound to the workspace context, so tearing it down
// would cancel the agent mid-turn. Only an explicit server shutdown
// (Backend.Shutdown) overrides this.
func (w *Workspace) agentBusy() bool {
	if w.busyFn != nil {
		return w.busyFn()
	}
	return w.App != nil && w.AgentCoordinator != nil && w.AgentCoordinator.IsBusy()
}

// invokeShutdown calls the workspace shutdown hook if set, falling
// back to the workspace [Workspace.Shutdown] wrapper when not.
func (w *Workspace) invokeShutdown() {
	if w.shutdownFn != nil {
		w.shutdownFn()
		return
	}
	if w.App != nil {
		w.Shutdown()
	}
}

// Shutdown tears the workspace down in an order that is safe for
// agent runs whose lifetime is bound to the workspace context. It
// shadows the promoted [app.App.Shutdown] so callers reaching
// ws.Shutdown() always observe this ordering:
//
//  1. Mark the workspace closing so no new agent runs are accepted.
//  2. Cancel the workspace run context so any dispatched goroutine
//     that has not yet registered its per-session cancel still
//     observes cancellation.
//  3. Cancel active coordinator work for runs that already
//     registered their per-session cancel function.
//  4. Wait for dispatched agent goroutines to return.
//  5. Run the embedded [app.App.Shutdown] cleanup (DB, LSP, etc).
//
// CancelAll is idempotent, so the second call inside app.App.Shutdown
// is harmless; the important guarantee is that cancel -> CancelAll ->
// runWG.Wait completes before the embedded cleanup touches the DB.
func (w *Workspace) Shutdown() {
	w.runMu.Lock()
	w.closing = true
	w.runMu.Unlock()

	// Resolve any still-pending permission/question prompts as cancelled
	// BEFORE cancelling the workspace context. This unblocks agent
	// goroutines waiting in Request/Ask and publishes resolution
	// notifications that the attention forwarder republishes as
	// AttentionResolved, so no client is left with a zombie prompt for a
	// workspace that no longer exists. Then stop the forwarder and wait
	// for it to drain those buffered notifications onto the global
	// channel before the app (and its brokers) are torn down.
	if w.App != nil {
		if w.Permissions != nil {
			w.Permissions.CancelAll()
		}
		if w.Questions != nil {
			w.Questions.CancelAll()
		}
	}
	if w.attnCancel != nil {
		w.attnCancel()
	}
	w.attnWG.Wait()

	if w.cancel != nil {
		w.cancel()
	}
	if w.App != nil && w.AgentCoordinator != nil {
		w.AgentCoordinator.CancelAll()
	}
	w.runWG.Wait()
	if w.App != nil {
		w.App.Shutdown()
	}
}

// New creates a new [Backend].
func New(ctx context.Context, cfg *config.ConfigStore, shutdownFn ShutdownFunc) *Backend {
	return &Backend{
		workspaces:     csync.NewMap[string, *Workspace](),
		pathIndex:      make(map[string]string),
		cfg:            cfg,
		ctx:            ctx,
		shutdownFn:     shutdownFn,
		createGrace:    DefaultCreateGrace,
		reconnectGrace: DefaultReconnectGrace,
		registry:       registry.New(),
		attention:      pubsub.NewBroker[proto.AttentionEvent](),
		yoloByRoot:     make(map[string]bool),
		startedAt:      time.Now(),
	}
}

// SetCreateGrace overrides the create-grace window. Intended for tests
// that need short timeouts.
func (b *Backend) SetCreateGrace(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.createGrace = d
}

// SetReconnectGrace overrides the reconnect-grace window. Intended for
// tests that need short timeouts.
func (b *Backend) SetReconnectGrace(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reconnectGrace = d
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

	// effectiveCwd is the absolute, symlink-resolved form of the client's
	// original launch directory. It is preserved separately from key (the
	// project root used for .crush/ and dedup) so that tools, shell
	// commands, and file edits run in the directory the user actually
	// launched Crush from — even when that directory is a linked worktree
	// that collapses to a different project root for .crush/ purposes.
	effectiveCwd, err := filepath.Abs(home.Expand(args.Path))
	if err != nil {
		effectiveCwd = args.Path
	}
	if resolved, err := filepath.EvalSymlinks(effectiveCwd); err == nil {
		effectiveCwd = resolved
	}

	b.mu.Lock()
	if !args.Isolated {
		if existingID, ok := b.pathIndex[key]; ok {
			if ws, found := b.workspaces.Get(existingID); found {
				return b.adoptExistingWorkspace(ws, nil, clientID, args, effectiveCwd, key)
			}
			delete(b.pathIndex, key)
		}
	}
	b.mu.Unlock()

	ws, err := b.buildWorkspace(args, key, effectiveCwd)
	if err != nil {
		return nil, proto.Workspace{}, err
	}
	id := ws.ID
	cfg := ws.Cfg

	b.mu.Lock()
	if !args.Isolated {
		// Re-check the index under the lock: a concurrent caller may
		// have won the race between the initial unlock and here.
		if existingID, ok := b.pathIndex[key]; ok {
			if existing, found := b.workspaces.Get(existingID); found {
				return b.adoptExistingWorkspace(existing, ws, clientID, args, effectiveCwd, key)
			}
			delete(b.pathIndex, key)
		}
	}
	b.workspaces.Set(id, ws)
	if !args.Isolated {
		b.pathIndex[key] = id
	}
	b.registerClient(ws, clientID, args.Env, args.Path)
	b.mu.Unlock()

	// Start the cross-workspace attention forwarder for this workspace.
	b.startAttentionForwarder(ws)

	// Wire the cross-workspace swarm dispatcher into the freshly-built
	// coordinator. This is the single funnel all backend workspace
	// creation flows through, so wiring here guarantees every
	// workspace — HTTP-created, path-spawned by the swarm tool, or
	// otherwise — can see the swarm tool without depending on a
	// follow-up InitAgent call. Best-effort: swarm is non-critical, so
	// a wiring failure must not fail workspace creation.
	if err := b.wireSwarmBackend(b.ctx, ws); err != nil {
		slog.Warn("Failed to wire swarm backend into workspace", "root", key, "error", err)
	}

	// A workspace brought up mid-drain (a swarm path re-attach, or an
	// old TUI connecting) must not hand finished turns off to queued
	// prompts either; rehydrateQueue itself is a no-op while draining.
	if b.Draining() {
		ws.pauseQueueDispatch()
	}

	// Replay any prompts a previous server left journaled for this
	// workspace (see drain.go). Runs after swarm wiring so a replayed
	// swarm message can reply to its sender.
	b.rehydrateQueue(ws)

	// Record this workspace in the global registry so a future Crush
	// instance (or the server on startup) can enumerate and jump to it
	// without it being attached. Best-effort; a registry write failure
	// must not fail workspace creation.
	if !args.Isolated && b.registry != nil {
		if err := b.registry.Add(registry.Entry{
			Root:     key,
			DataDir:  cfg.Config().Options.DataDirectory,
			LastUsed: time.Now().Unix(),
		}); err != nil {
			slog.Warn("Failed to record workspace in registry", "root", key, "error", err)
		}
	}

	if args.Version != "" && args.Version != version.Version {
		slog.Warn(
			"Client/server version mismatch",
			"client", args.Version,
			"server", version.Version,
		)
		ws.SendEvent(util.NewWarnMsg(fmt.Sprintf(
			"Server version %q differs from client version %q. Consider restarting the server.",
			version.Version, args.Version,
		)))
	}

	return ws, workspaceToProtoForClient(ws, effectiveCwd), nil
}

// buildWorkspace constructs a fresh, un-registered *Workspace for the
// project rooted at key: it initializes config, seeds the yolo
// permission toggle, creates the data directory, opens the database,
// discovers skills, and builds the embedded app.App. It performs no
// registration or dedup — the caller owns inserting the returned
// workspace into b.workspaces / b.pathIndex under b.mu.
//
// key is the canonical project root, used for config loading and all
// downstream path consumers (snapshot restore target, agent default
// cwd, git branch display); effectiveCwd is the client's original
// launch directory, passed to app.New so tools and shell commands run
// where the user actually is even inside a linked worktree.
func (b *Backend) buildWorkspace(args proto.Workspace, key, effectiveCwd string) (*Workspace, error) {
	cfg, err := config.Init(key, args.DataDir, args.Debug)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize config: %w", err)
	}

	// Seed yolo from the CLI flag, then restore a runtime toggle that a
	// prior incarnation of this workspace made before it autoclosed: the
	// override and permission service that held it were destroyed on
	// teardown, so without this the setting would silently reset to the
	// flag on every recreate. args.YOLO still wins when explicitly set.
	skipPerms := args.YOLO
	if !skipPerms {
		b.mu.Lock()
		skipPerms = b.yoloByRoot[key]
		b.mu.Unlock()
	}
	cfg.Overrides().SkipPermissionRequests = skipPerms

	if err := createDotCrushDir(cfg.Config().Options.DataDirectory); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	conn, err := b.connectWorkspaceDB(b.ctx, cfg.Config().Options.DataDirectory)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
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

	appWorkspace, err := app.New(b.ctx, conn, cfg, skillsMgr, effectiveCwd)
	if err != nil {
		return nil, fmt.Errorf("failed to create app workspace: %w", err)
	}

	wsCtx, wsCancel := context.WithCancel(b.ctx)
	return &Workspace{
		App:          appWorkspace,
		ID:           uuid.New().String(),
		Path:         key,
		Cfg:          cfg,
		Env:          args.Env,
		Skills:       skillsMgr,
		isolated:     args.Isolated,
		resolvedPath: key,
		ctx:          wsCtx,
		cancel:       wsCancel,
		clients:      make(map[string]*clientState),
	}, nil
}

// dataDirLockWait bounds how long connectWorkspaceDB retries a data
// directory still locked by a previous server. Package variable so tests
// can shorten it.
var dataDirLockWait = 15 * time.Second

// takeoverWindow is how long after startup a server treats a locked data
// directory as "the predecessor is still releasing it" and waits, rather
// than failing at once as a genuine conflict with another process.
var takeoverWindow = 2 * time.Minute

// connectWorkspaceDB opens the workspace database under the data-dir
// lock. During a graceful update the previous server can still hold that
// lock for a moment after its socket disappears (it releases workspaces
// as it exits), so within the first takeoverWindow of this server's life
// ErrDataDirLocked is retried for up to dataDirLockWait (bounded by ctx)
// with a log line rather than failing the client's first connect. After
// that window, or for any other error, a locked directory is a genuine
// conflict with another crush process and fails immediately as before.
func (b *Backend) connectWorkspaceDB(ctx context.Context, dataDir string) (*sql.DB, error) {
	deadline := time.Now().Add(dataDirLockWait)
	mayWait := time.Since(b.startedAt) < takeoverWindow
	logged := false
	for {
		conn, err := db.Connect(ctx, dataDir, db.WithDataDirLock(true))
		if err == nil {
			return conn, nil
		}
		if !errors.Is(err, db.ErrDataDirLocked) || !mayWait || time.Now().After(deadline) {
			return nil, err
		}
		if !logged {
			slog.Info("Data directory still locked by a previous crush process; waiting for it to release before opening the database",
				"data_dir", dataDir, "timeout", dataDirLockWait)
			logged = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// adoptExistingWorkspace handles a CreateWorkspace dedup hit: the
// caller found an already-attached workspace for the same root and
// wants to hand it back instead of the workspace it was about to (or
// just did) build. The caller MUST hold b.mu; this releases it.
//
// loser, when non-nil, is the redundant freshly-built workspace that
// lost the attach race and must be torn down. It is shut down only
// after b.mu is released, since Shutdown can block on in-flight agent
// goroutines and must not be run under the backend lock.
//
// wireSwarmBackendIfMissing re-wires swarm on the reused workspace: a
// prior CreateWorkspace for this path may have returned before its own
// wireSwarmBackend call ran, leaving the cached *Workspace with a nil
// swarm backend otherwise. It only pays the rebuild cost when wiring is
// actually missing, so a plain already-wired switch/reconnect is a
// no-op.
func (b *Backend) adoptExistingWorkspace(existing, loser *Workspace, clientID string, args proto.Workspace, effectiveCwd, key string) (*Workspace, proto.Workspace, error) {
	logFirstWinsMismatch(existing, args)
	b.registerClient(existing, clientID, args.Env, args.Path)
	b.mu.Unlock()
	if loser != nil {
		loser.invokeShutdown()
	}
	b.reloadWorkspaceConfig(existing)
	if err := b.wireSwarmBackendIfMissing(b.ctx, existing); err != nil {
		slog.Warn("Failed to wire swarm backend into reused workspace", "root", key, "error", err)
	}
	return existing, workspaceToProtoForClient(existing, effectiveCwd), nil
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
// bridge can be built lazily on first use. cwd is the client's
// original working directory, retained for active-worktree
// auto-detection on SetCurrentSession.
func (b *Backend) registerClient(ws *Workspace, clientID string, env []string, cwd string) {
	ws.clientsMu.Lock()
	defer ws.clientsMu.Unlock()
	if _, ok := ws.clients[clientID]; ok {
		return
	}
	cs := &clientState{env: env, cwd: cwd}
	cs.holdTimer = time.AfterFunc(b.createGrace, func() {
		b.expireHold(ws, clientID, cs)
	})
	ws.clients[clientID] = cs
}

// clientCwd returns the absolute, symlink-resolved launch directory of
// the named client on the workspace, or "" when the client is unknown or
// captured no cwd. The resolution mirrors CreateWorkspace's effectiveCwd
// so a per-turn cwd compares equal to managed worktree paths and the
// workspace default.
func (b *Backend) clientCwd(ws *Workspace, clientID string) string {
	ws.clientsMu.Lock()
	cs, ok := ws.clients[clientID]
	cwd := ""
	if ok {
		cwd = cs.cwd
	}
	ws.clientsMu.Unlock()
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
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
		b.teardownIfIdle(ws)
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
		b.teardownIfIdle(ws)
	}
}

func (b *Backend) detachStream(ws *Workspace, clientID string) {
	grace := b.reconnectGrace
	ws.clientsMu.Lock()
	cs, ok := ws.clients[clientID]
	if !ok {
		ws.clientsMu.Unlock()
		return
	}
	if cs.streams > 0 {
		cs.streams--
	}
	if cs.streams == 0 && cs.holdTimer == nil {
		// The client's last event stream just dropped, but the client
		// process may still be alive and about to reconnect (a
		// transient SSE blip, or the gap during a workspace switch).
		// Arm a reconnect-grace timer rather than deleting the entry
		// and tearing down immediately: AttachClient stops this timer
		// when the same client re-attaches, and expireHold removes the
		// entry (and tears the workspace down if still idle) only once
		// the grace window elapses with no reconnect. Without this, the
		// last stream loss can shut the whole server down out from under
		// a live client.
		if grace <= 0 {
			cs.closeBridge()
			delete(ws.clients, clientID)
			teardown := len(ws.clients) == 0
			ws.clientsMu.Unlock()
			if teardown {
				b.teardownIfIdle(ws)
			}
			return
		}
		cs.holdTimer = time.AfterFunc(grace, func() {
			b.expireHold(ws, clientID, cs)
		})
	}
	ws.clientsMu.Unlock()
}

// teardownIfIdle tears the workspace down only when it has no attached
// clients and no in-flight agent run. When the last client detaches while
// the agent is still working, the workspace is kept alive so the run can
// finish; [Backend.runAgent] re-checks idleness on completion and tears
// down then. The explicit shutdown command bypasses this via
// [Backend.Shutdown], which calls shutdownFn directly.
func (b *Backend) teardownIfIdle(ws *Workspace) {
	ws.clientsMu.Lock()
	hasClients := len(ws.clients) > 0
	ws.clientsMu.Unlock()
	if hasClients {
		return
	}
	if ws.agentBusy() {
		slog.Info(
			"Last client detached but agent is busy; keeping workspace alive until the run completes",
			"workspace", ws.ID,
		)
		return
	}
	b.teardown(ws)
}

func (b *Backend) teardown(ws *Workspace) {
	b.mu.Lock()
	// Idempotency guard: teardown can be reached both from the detach
	// paths and from runAgent's post-run idle re-check. Once the
	// workspace has been removed, subsequent calls are no-ops so the
	// shutdown hook never fires twice.
	if _, ok := b.workspaces.Get(ws.ID); !ok {
		b.mu.Unlock()
		return
	}
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
	draining := b.drain != nil
	b.mu.Unlock()

	if draining {
		// A workspace going idle mid-drain must leave its journaled
		// queue for the next server, not clear it on teardown.
		ws.handOffJournal()
	}
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
	cs, ok := ws.clients[clientID]
	if !ok || cs.streams == 0 {
		// No entry, or hold-only (no live stream): refuse the
		// write. The presence record this is meant to feed
		// should only reflect clients that can actually observe
		// session events.
		ws.clientsMu.Unlock()
		return ErrClientNotAttached
	}
	cs.currentSessionID = sessionID
	cwd := cs.cwd
	ws.clientsMu.Unlock()

	b.onSessionFocused(ws, clientID, sessionID, cwd)
	return nil
}

// onSessionFocused runs the best-effort side effects that follow a
// successful SetCurrentSession presence write. None of them can fail
// the write: mark-seen and republish are advisory, and worktree sync
// runs on its own goroutine. Split out of SetCurrentSession so the
// write path stays a straight line of guards.
func (b *Backend) onSessionFocused(ws *Workspace, clientID, sessionID, cwd string) {
	if sessionID == "" {
		return
	}

	if ws.App != nil {
		// Mark the session seen: opening it clears its unread state (it
		// has no completed work the viewer has not now seen).
		// Best-effort; a failure must not prevent the presence write.
		if ws.Sessions != nil {
			if err := ws.Sessions.MarkSeen(context.Background(), sessionID); err != nil {
				slog.Debug("Failed to mark session seen", "session_id", sessionID, "error", err)
			}
		}

		// Re-surface any still-pending permission/question prompt for the
		// now-focused session. A client that just switched to this
		// workspace was not subscribed when the prompt was first
		// published, so without this its modal would never appear
		// (switch-to-grant). Republishing re-emits the request on the
		// workspace event stream, which the now-attached client receives
		// and, because it is the current session, opens.
		if ws.Permissions != nil {
			ws.Permissions.RepublishPending(sessionID)
		}
		if ws.Questions != nil {
			ws.Questions.RepublishPending(sessionID)
		}
	}

	// Best-effort: if the client's cwd lies inside a managed
	// worktree, switch the session's active worktree to match. This
	// makes the session's worktree state follow `cd` without an
	// explicit `/worktree switch`. Failures here never prevent the
	// SetCurrentSession write from succeeding; they're advisory and
	// surface as debug logs only. Intentionally independent of ws.App
	// (matches the original inline guard).
	if cwd != "" && ws.Worktrees != nil && ws.Worktrees.IsEnabled() {
		go b.maybeSyncSessionWorktree(ws, clientID, sessionID, cwd)
	}
}

// maybeSyncSessionWorktree looks up the managed worktree (if any)
// containing cwd and ensures it is the active worktree for sessionID.
// Runs off the request goroutine because Switch may shell out to git
// for restore hooks. All errors are logged at debug level only — this
// is a UX nicety, not a correctness path.
func (b *Backend) maybeSyncSessionWorktree(ws *Workspace, clientID, sessionID, cwd string) {
	ctx, cancel := context.WithTimeout(ws.ctx, 5*time.Second)
	defer cancel()

	wt, err := ws.Worktrees.GetByPath(ctx, cwd)
	if err != nil {
		// Not inside a managed worktree (or path resolution failed):
		// nothing to do.
		return
	}

	active, err := ws.Worktrees.GetActive(ctx, sessionID)
	if err == nil && active != nil && active.ID == wt.ID {
		return
	}

	if err := ws.Worktrees.Switch(ctx, sessionID, wt.ID); err != nil {
		slog.Debug("Auto-switch session worktree from cwd failed",
			"workspace", ws.ID,
			"client", clientID,
			"session", sessionID,
			"cwd", cwd,
			"worktree", wt.Name,
			"error", err)
		return
	}
	slog.Debug("Auto-switched session worktree to match client cwd",
		"workspace", ws.ID,
		"session", sessionID,
		"worktree", wt.Name,
		"path", wt.Path)
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

// CurrentSessionID returns the session the given client is currently
// viewing in the workspace, along with whether the client is attached
// with a live SSE stream. Hold-only clients (streams == 0) and unknown
// clients report ok=false. Used to scope broadcast events to the
// session a client is actually looking at.
func (b *Backend) CurrentSessionID(workspaceID, clientID string) (string, bool) {
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return "", false
	}
	ws.clientsMu.Lock()
	defer ws.clientsMu.Unlock()
	cs, ok := ws.clients[clientID]
	if !ok || cs.streams == 0 {
		return "", false
	}
	return cs.currentSessionID, true
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

		ProtocolVersion: proto.ProtocolVersion,
	}
}

// Config returns the server-level configuration.
func (b *Backend) Config() *config.ConfigStore {
	return b.cfg
}

// Shutdown initiates an immediate server shutdown. Unlike the
// idle-teardown path (which keeps a workspace alive while clients are
// attached or an agent is busy), this is the explicit "stop now"
// command: it tears down every workspace regardless of attached
// clients, cancelling each workspace run context and all in-flight
// agent runs via [Workspace.Shutdown] so streaming tool calls are
// marked cancelled. Only after every workspace has been torn down does
// it invoke the shutdown callback to stop the HTTP server.
//
// Workspaces are removed from the registry first so any concurrent
// detach/idle teardown becomes a no-op and the shutdown callback is not
// raced by the "last workspace removed" path.
func (b *Backend) Shutdown() {
	b.ShutdownWorkspaces()

	if b.shutdownFn != nil {
		b.shutdownFn()
	}
}

// ShutdownWorkspaces tears down every registered workspace without
// stopping the HTTP server. It cancels each workspace run context and
// all in-flight agent runs via [Workspace.Shutdown] so streaming tool
// calls are marked cancelled. Used by both [Backend.Shutdown] (the
// control-command path, which also stops the server afterward) and by
// signal-driven shutdown in the server command, which owns server
// teardown itself and must cancel runs before draining HTTP.
//
// Workspaces are removed from the registry first so any concurrent
// detach/idle teardown becomes a no-op and the "last workspace removed"
// path does not race the explicit teardown.
func (b *Backend) ShutdownWorkspaces() {
	b.mu.Lock()
	// Only a drain that ran to completion has handed its journals off;
	// a shutdown that interrupts a drain is a forced stop like any other.
	handedOff := b.drain != nil && b.drain.done
	wss := make([]*Workspace, 0, b.workspaces.Len())
	for id, ws := range b.workspaces.Seq2() {
		wss = append(wss, ws)
		if existing, ok := b.pathIndex[ws.resolvedPath]; ok && existing == id {
			delete(b.pathIndex, ws.resolvedPath)
		}
	}
	for _, ws := range wss {
		b.workspaces.Del(ws.ID)
	}
	b.mu.Unlock()

	// Cancel and tear down each workspace. invokeShutdown runs
	// Workspace.Shutdown: mark closing, cancel the run context,
	// CancelAll in-flight coordinator runs, wait for run goroutines,
	// then App cleanup.
	//
	// On a forced shutdown (anything but a completed drain) the
	// journaled queue is dropped outright, matching the documented
	// `--reset` contract and the historical behavior of losing
	// in-memory queues: CancelAll's own clear only reaches sessions
	// that are busy, so an idle session with a stuck queue would
	// otherwise replay it on the next server. Reply obligations are
	// dropped too: the cancelled turns tell their senders the work was
	// cancelled (failReplyObligations), so anything persisted is a
	// leftover no turn owns. Both clears must run BEFORE teardown —
	// App.Shutdown releases the pooled database connection the journal
	// store writes through.
	for _, ws := range wss {
		if !handedOff {
			ws.discardJournal()
			ws.discardReplies()
		}
		ws.invokeShutdown()
	}
}

// discardJournal drops the workspace's journaled queue. Used by forced
// shutdowns, where queued prompts are documented to be lost.
func (w *Workspace) discardJournal() {
	if w.App == nil || w.Journal == nil {
		return
	}
	if err := w.Journal.ClearQueues(context.Background()); err != nil {
		slog.Warn("Failed to discard journaled queue on forced shutdown", "workspace", w.ID, "error", err)
	}
}

// discardReplies drops the workspace's persisted reply obligations on a
// forced shutdown. The in-memory tracker is left alone: the turns being
// cancelled still consult it to tell their senders, and it dies with
// the process; DetachJournals stops those clears from being written
// back after this point.
func (w *Workspace) discardReplies() {
	if w.App == nil || w.Journal == nil {
		return
	}
	if err := w.Journal.ClearReplies(context.Background()); err != nil {
		slog.Warn("Failed to discard journaled reply obligations on forced shutdown", "workspace", w.ID, "error", err)
	}
}

// resolveWorkspaceKey returns the canonical project root for path,
// suitable for use as the workspace dedup key. All clients that share
// the same git repository — whether in the main working tree, a
// Crush-managed worktree, or a user-created sibling worktree — resolve
// to the same key so they share a single server-side workspace and
// thus a single `*App` writing to `.crush/`. Without this, concurrent
// clients would race on the same on-disk state.
//
// The key is the project root (config.ProjectRoot), which for git
// repositories is always the main working tree root. For non-git
// directories the absolute resolved cwd is used as a fallback.
//
// The original client cwd (args.Path) is preserved separately as
// effectiveCwd and threaded into the coordinator so tools and shell
// commands run in the directory the user actually launched from.
func resolveWorkspaceKey(path string) (string, error) {
	path = home.Expand(path)
	if strings.HasPrefix(path, "~") {
		// Expansion failed (unknown home directory); refuse rather
		// than silently creating a literal `~` directory tree.
		return "", fmt.Errorf("%w: cannot expand home directory in %q", ErrPathRequired, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return config.ProjectRoot(abs), nil
}

// ResolveWorkspaceByPath resolves path to a canonical workspace key
// (using the same logic as resolveWorkspaceKey) and looks it up in the
// path index. It returns the workspace ID and found=true when a
// running workspace matches, or found=false (not an error) when no
// running workspace is registered at that path.
func (b *Backend) ResolveWorkspaceByPath(ctx context.Context, path string) (workspaceID string, found bool, err error) {
	if strings.TrimSpace(path) == "" {
		return "", false, ErrPathRequired
	}
	key, err := resolveWorkspaceKey(path)
	if err != nil {
		return "", false, err
	}
	b.mu.Lock()
	id, ok := b.pathIndex[key]
	b.mu.Unlock()
	if !ok {
		return "", false, nil
	}
	return id, true, nil
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
		ID:         ws.ID,
		Path:       ws.Path,
		WorkingDir: ws.WorkingDir(),
		GitBranch:  getGitBranch(ws.Path),
		YOLO:       ws.Cfg.Overrides().SkipPermissionRequests,
		DataDir:    cfg.Options.DataDirectory,
		Debug:      cfg.Options.Debug,
		Config:     cfg,
		Env:        ws.Env,
		Version:    version.Version,
	}
	if ws.Skills != nil {
		out.Skills = skillStatesToProto(ws.Skills.States())
	}
	return out
}

// workspaceToProtoForClient is like workspaceToProto but reports the
// requesting client's own launch directory (launchCwd) as WorkingDir and
// GitBranch, rather than the workspace's shared first-client directory.
// Multiple clients (different subdirectories or sibling git worktrees) can
// share one workspace because they collapse to the same canonical project
// root for .crush/ purposes; each client must still see and operate from the
// directory it actually launched from. launchCwd is the absolute,
// symlink-resolved client cwd; an empty value falls back to the shared
// workspace view.
func workspaceToProtoForClient(ws *Workspace, launchCwd string) proto.Workspace {
	out := workspaceToProto(ws)
	if launchCwd != "" {
		out.WorkingDir = launchCwd
		out.GitBranch = getGitBranch(launchCwd)
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
	cmd := exec.CommandContext(context.Background(), "git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
