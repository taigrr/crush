// Package app wires together services, coordinates agents, and manages
// application lifecycle.
package app

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/checkpoint"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/filetracker"
	"github.com/taigrr/crush/internal/fork"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/historysearch"
	"github.com/taigrr/crush/internal/log"
	"github.com/taigrr/crush/internal/lsp"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/milestone"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/sessionimport"
	"github.com/taigrr/crush/internal/shell"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/update"
	"github.com/taigrr/crush/internal/version"
	"github.com/taigrr/crush/internal/worktree"
	"github.com/taigrr/fantasy"
)

// UpdateAvailableMsg is sent when a new version is available.
type UpdateAvailableMsg struct {
	CurrentVersion string
	LatestVersion  string
	IsDevelopment  bool
}

type App struct {
	// historyEmbedder replaces the configured embedder for history search
	// (tests only, see SetHistoryEmbedderForTest).
	historyEmbedder atomic.Pointer[embedding.Service]

	Sessions    session.Service
	Messages    message.Service
	History     history.Service
	Permissions permission.Service
	Questions   question.Service
	FileTracker filetracker.Service
	Checkpoints checkpoint.Service
	Worktrees   worktree.Service
	Forks       fork.Service
	Milestones  milestone.Service
	embeddings  embedding.Service

	AgentCoordinator agent.Coordinator

	SessionImporter func(context.Context, sessionimport.Session) (sessionimport.Result, error)

	LSPManager *lsp.Manager

	Skills *skills.Manager

	config *config.ConfigStore

	dbConn *sql.DB

	serviceEventsWG *sync.WaitGroup
	eventsCtx       context.Context
	events          *pubsub.Broker[tea.Msg]
	tuiWG           *sync.WaitGroup

	// workingDir is the effective working directory for tools and shell
	// commands. For user-created linked worktrees this is the worktree
	// cwd the user launched from, which may differ from
	// config.WorkingDir() (the project root hosting .crush/).
	workingDir string

	// global context and cleanup functions
	globalCtx    context.Context
	cleanupFuncs []func(context.Context) error
	// lsp tracks this workspace's LSP client states and events.
	lsp                *lspStatus
	agentNotifications *pubsub.Broker[notify.Notification]
	// runCompletions is the authoritative per-run completion signal,
	// emitted once per top-level agent turn after all message
	// updates have been flushed. Bridged into app.events so SSE
	// subscribers (notably `crush run` in client/server mode) can
	// drive their exit on a deterministic, payload-bearing event
	// instead of guessing from message finish parts.
	runCompletions *pubsub.Broker[notify.RunComplete]
}

// New initializes a new application instance. skillsMgr carries the
// per-workspace skill discovery results computed by the caller; the
// caller is responsible for constructing it (typically via
// backend.CreateWorkspace). workingDir is the effective working directory
// for tools and shell commands; it may differ from store.WorkingDir() when
// the client launched from a linked worktree (store.WorkingDir() is always
// the project root hosting .crush/).
func New(ctx context.Context, conn *sql.DB, store *config.ConfigStore, skillsMgr *skills.Manager, workingDir string) (*App, error) {
	q := db.New(conn)
	sessions := session.NewService(q, conn)
	messages := message.NewService(q)
	files := history.NewService(q, conn)
	cfg := store.Config()
	skipPermissionsRequests := store.Overrides().SkipPermissionRequests
	var allowedTools []string
	if cfg.Permissions != nil && cfg.Permissions.AllowedTools != nil {
		allowedTools = cfg.Permissions.AllowedTools
	}

	// store.WorkingDir() is the project root (hosting .crush/).
	// workingDir (the parameter) is the effective cwd for tools — for
	// user-created linked worktrees it is the worktree the user launched
	// from, which may differ from the project root.
	projectRoot := config.ProjectRoot(store.WorkingDir())

	// Initialize checkpoint service for filesystem snapshots.
	checkpointCfg := checkpoint.ServiceConfig{
		ProjectDir: projectRoot,
		Enabled:    cfg.Snapshots.IsEnabled(),
	}
	if cfg.Snapshots != nil {
		checkpointCfg.Exclude = cfg.Snapshots.Exclude
	}
	if cfg.Worktree != nil {
		for _, hook := range cfg.Worktree.PostCreate {
			checkpointCfg.PostRestoreHooks = append(checkpointCfg.PostRestoreHooks, checkpoint.PostRestoreHook{
				IfExists: hook.IfExists,
				Run:      hook.Run,
			})
		}
	}
	checkpoints, err := checkpoint.NewService(checkpointCfg, q, conn)
	if err != nil {
		slog.Warn("Failed to initialize checkpoint service", "error", err)
		// Continue without snapshots - it's not critical.
		checkpoints, _ = checkpoint.NewService(checkpoint.ServiceConfig{Enabled: false}, q, conn)
	}

	// Initialize worktree service.
	worktreeCfg := worktree.ServiceConfig{
		ProjectDir: projectRoot,
		Enabled:    cfg.Worktree.IsEnabled(),
	}
	if cfg.Worktree != nil {
		worktreeCfg.PostCreateHooks = cfg.Worktree.PostCreate
	}
	worktrees, err := worktree.NewService(worktreeCfg, q, conn, checkpoints)
	if err != nil {
		slog.Warn("Failed to initialize worktree service", "error", err)
		worktrees, _ = worktree.NewService(worktree.ServiceConfig{Enabled: false}, q, conn, checkpoints)
	}

	// Initialize fork service.
	forks := fork.NewService(q, conn, sessions, messages, checkpoints, worktrees)

	app := &App{
		Sessions:    sessions,
		Messages:    messages,
		History:     files,
		Permissions: permission.NewPermissionService(store.WorkingDir(), skipPermissionsRequests, allowedTools),
		Questions:   question.NewQuestionService(),
		FileTracker: filetracker.NewService(q),
		Checkpoints: checkpoints,
		Worktrees:   worktrees,
		Forks:       forks,
		Milestones:  milestone.NewService(conn, q),
		SessionImporter: func(ctx context.Context, imported sessionimport.Session) (sessionimport.Result, error) {
			result, err := sessionimport.Import(ctx, conn, imported)
			if err != nil {
				return result, err
			}
			if result.Imported > 0 {
				sessions.NotifyImported(ctx, result.ID, result.Imported == result.Messages)
			}
			return result, nil
		},
		embeddings: embedding.Build(q, store.EmbeddingParams()),
		LSPManager: lsp.NewManager(store),
		Skills:     skillsMgr,

		workingDir: workingDir,
		globalCtx:  ctx,

		config: store,
		dbConn: conn,

		events:             pubsub.NewBroker[tea.Msg](),
		serviceEventsWG:    &sync.WaitGroup{},
		lsp:                newLSPStatus(),
		tuiWG:              &sync.WaitGroup{},
		agentNotifications: pubsub.NewBroker[notify.Notification](),
		runCompletions:     pubsub.NewBroker[notify.RunComplete](),
	}

	app.setupEvents()

	// Backfill swarm identities for any legacy sessions that predate
	// the color/animal columns, and keep new sessions in sync via a
	// pubsub subscription so identity is assigned before the target
	// can be addressed. Wired onto app.eventsCtx / serviceEventsWG so
	// the subscriber exits cleanly on Shutdown; backfill uses a
	// background context so a quick shutdown of the caller
	// (tests, short-lived subcommands) doesn't cancel the initial
	// scan mid-way. Subscribing happens synchronously here so any
	// session.Create that fires before the receiver goroutine
	// starts is still buffered by the broker.
	sessionEvents := app.Sessions.Subscribe(app.eventsCtx)
	app.serviceEventsWG.Go(func() {
		app.assignSwarmIdentityOnCreate(app.eventsCtx, sessionEvents)
	})
	app.serviceEventsWG.Go(func() {
		// Detached from eventsCtx so shutdown does not abort the scan
		// mid-way, but tracked on serviceEventsWG so it finishes before
		// the database is released, and bounded so it cannot stall
		// shutdown indefinitely.
		backfillCtx, cancel := context.WithTimeout(context.WithoutCancel(app.eventsCtx), swarmBackfillTimeout)
		defer cancel()
		if err := app.backfillSwarmIdentities(backfillCtx); err != nil {
			slog.Warn("Swarm identity backfill failed", "error", err)
		}
	})

	// Check for updates in the background.
	go app.checkForUpdates(ctx)

	// Arm initialization synchronously before launching it so WaitForInit
	// blocks for the in-flight init instead of racing the goroutine and
	// returning before any MCP tools register.
	mcp.ArmInit()
	go mcp.Initialize(ctx, app.Permissions, store)

	// Embed messages in the background for hybrid history search. No-op
	// when no embedder is configured.
	go historysearch.RunIndexer(ctx, app.Messages, app.embeddings)

	// Release the shared database connection on shutdown. The pool
	// closes the underlying *sql.DB when the last reference is released.
	dataDir := cfg.Options.DataDirectory
	app.cleanupFuncs = append(
		app.cleanupFuncs,
		func(context.Context) error { return db.Release(dataDir) },
		func(ctx context.Context) error { return mcp.Close(ctx) },
	)

	// TODO: remove the concept of agent config, most likely.
	if !cfg.IsConfigured() {
		slog.Warn("No agent configuration found")
		return app, nil
	}
	if err := app.InitCoderAgent(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize coder agent: %w", err)
	}

	// Set up callback for LSP state updates.
	app.LSPManager.SetCallback(func(name string, client *lsp.Client) {
		if client == nil {
			app.lsp.updateLSPState(name, lsp.StateUnstarted, nil, nil, 0)
			return
		}
		client.SetDiagnosticsCallback(app.lsp.updateLSPDiagnostics)
		var warn error
		if client.GetServerState() == lsp.StateWarn {
			warn = errors.New(client.RootWarning())
		}
		app.lsp.updateLSPState(name, client.GetServerState(), warn, client, 0)
	})
	go app.LSPManager.TrackConfigured()

	// Validate worktree state on startup.
	// It shells out to git, so it runs in the background rather than
	// delaying the workspace; tracked so it finishes before shutdown
	// releases the database.
	if worktrees != nil && worktrees.IsEnabled() {
		app.serviceEventsWG.Go(func() {
			if err := worktrees.ValidateState(app.eventsCtx); err != nil {
				slog.Warn("Worktree validation failed", "error", err)
			}
		})
	}

	return app, nil
}

// WorkingDir returns the effective working directory for tools and shell
// commands. For user-created linked worktrees this is the cwd the user
// launched from, which may differ from config.WorkingDir() (the project
// root hosting .crush/).
func (app *App) WorkingDir() string {
	return cmp.Or(app.workingDir, app.config.WorkingDir())
}

// Config returns the pure-data configuration.
func (app *App) Config() *config.Config {
	return app.config.Config()
}

// BackfillEmbeddings embeds every past message that lacks a vector under
// the active embedding model. It builds a fresh embedding service from
// the current config (rather than the long-lived startup service) so it
// is correct even after the embedder was changed mid-session. Returns
// the number of messages embedded; a no-op (0) when no embedder is
// configured.
func (app *App) BackfillEmbeddings(ctx context.Context) (int, error) {
	emb := embedding.Build(db.New(app.dbConn), app.config.EmbeddingParams())
	return historysearch.Backfill(ctx, app.Messages, app.Sessions, emb, nil)
}

// PendingEmbeddingCount reports how many past messages would be embedded
// by BackfillEmbeddings. Zero when no embedder is configured.
func (app *App) PendingEmbeddingCount(ctx context.Context) (int, error) {
	emb := embedding.Build(db.New(app.dbConn), app.config.EmbeddingParams())
	return historysearch.PendingCount(ctx, app.Messages, app.Sessions, emb)
}

// EmbeddingStatus reports the embedding index state for the sidebar
// progress display: whether an embedder is configured, how many messages
// are embedded under the active model, and the total embeddable count.
func (app *App) EmbeddingStatus(ctx context.Context) (proto.EmbeddingStatus, error) {
	emb := embedding.Build(db.New(app.dbConn), app.config.EmbeddingParams())
	if !emb.Enabled() {
		return proto.EmbeddingStatus{}, nil
	}
	embedded, _, err := emb.Counts(ctx)
	if err != nil {
		return proto.EmbeddingStatus{}, err
	}
	pending, err := historysearch.PendingCount(ctx, app.Messages, app.Sessions, emb)
	if err != nil {
		return proto.EmbeddingStatus{}, err
	}
	return proto.EmbeddingStatus{
		Enabled:  true,
		Embedded: int(embedded),
		Total:    int(embedded) + pending,
	}, nil
}

// HistorySource is this workspace as a history search source, tagged
// with root.
func (app *App) HistorySource(root string) historysearch.Source {
	return historysearch.Source{
		Root:     root,
		Messages: app.Messages,
		Sessions: app.Sessions,
		Emb:      app.historyEmbedding(),
	}
}

func (app *App) historyEmbedding() embedding.Service {
	if e := app.historyEmbedder.Load(); e != nil {
		return *e
	}
	return embedding.Build(db.New(app.dbConn), app.config.EmbeddingParams())
}

// SetHistoryEmbedderForTest makes history search use an embedder built
// from params over this workspace's database, with model as its
// embedding model, and returns it (so a test can store vectors).
func (app *App) SetHistoryEmbedderForTest(params embedding.Params, model fantasy.EmbeddingModel) embedding.Service {
	e := embedding.Build(db.New(app.dbConn), params)
	embedding.SetModelForTest(e, model)
	app.historyEmbedder.Store(&e)
	return e
}

// SearchHistory runs the hybrid (substring + semantic) history search
// over this workspace through [historysearch.SearchAcross], the same
// search, ranking, and limits as the cross-workspace search and `crush
// search`. Hits carry no workspace tag; the backend stamps it.
func (app *App) SearchHistory(ctx context.Context, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error) {
	sessionLimit, candidateLimit := historysearch.ResolveLimits(params.Limit)
	res, err := historysearch.SearchAcross(ctx, []historysearch.Source{app.HistorySource("")}, params.Query,
		historysearch.Scope(params.Scope), params.Semantic, candidateLimit, sessionLimit)
	if err != nil {
		return proto.SearchHistoryResult{}, err
	}
	return HistoryResultToProto(res, nil), nil
}

// HistoryResultToProto converts a history search result to its wire
// form. workspaceID maps a hit's root to a workspace ID (nil or "" leaves
// it empty).
func HistoryResultToProto(res historysearch.CrossResult, workspaceID func(root string) string) proto.SearchHistoryResult {
	hits := make([]proto.SessionHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hit := proto.SessionHit{
			SessionID:     h.SessionID,
			SessionTitle:  h.SessionTitle,
			WorkspaceRoot: h.Root,
			Score:         h.Score,
			Match:         string(h.Match),
			Snippet:       h.Snippet,
			MessageID:     h.MessageID,
			Role:          h.Role,
			CreatedAt:     h.CreatedAt,
		}
		if workspaceID != nil {
			hit.WorkspaceID = workspaceID(h.Root)
		}
		hits = append(hits, hit)
	}
	return proto.SearchHistoryResult{
		Hits:           hits,
		Total:          res.Total,
		SemanticUsed:   res.SemanticUsed,
		Mode:           string(res.Mode),
		FallbackReason: string(res.FallbackReason),
	}
}

// Store returns the config store.
func (app *App) Store() *config.ConfigStore {
	return app.config
}

// Events returns a per-caller subscription channel for application events.
// Each caller receives its own channel; all callers receive every event.
func (app *App) Events(ctx context.Context) <-chan pubsub.Event[tea.Msg] {
	return app.events.Subscribe(ctx)
}

// SendEvent publishes a message to all event subscribers.
func (app *App) SendEvent(msg tea.Msg) {
	app.events.Publish(pubsub.UpdatedEvent, msg)
}

// AgentNotifications returns the broker for agent notification events.
func (app *App) AgentNotifications() *pubsub.Broker[notify.Notification] {
	return app.agentNotifications
}

// RunCompletions returns the broker for the authoritative per-run
// terminal RunComplete events. The dispatcher (backend.runAgent) uses
// it to emit a reliable terminal event when a run fails before the
// coordinator could publish one of its own.
func (app *App) RunCompletions() *pubsub.Broker[notify.RunComplete] {
	return app.runCompletions
}

// ArchiveSession archives a session and deletes its snapshot refs to allow GC.
func (app *App) ArchiveSession(ctx context.Context, sessionID string) error {
	// Delete snapshot refs first so git objects become unreachable.
	if err := app.Checkpoints.DeleteSessionSnapshots(ctx, sessionID); err != nil {
		slog.Warn("Failed to delete session snapshots during archive", "session_id", sessionID, "error", err)
		// Continue with archiving even if snapshot deletion fails.
	}

	// Mark the session as archived.
	if err := app.Sessions.Archive(ctx, sessionID); err != nil {
		return fmt.Errorf("archiving session: %w", err)
	}

	return nil
}

// MarkSessionSeen bumps the session's LastSeenAt so its derived unread
// state (LastFinishedAt > LastSeenAt) clears. Unlike SetCurrentSession's
// implicit mark-seen, this targets an arbitrary (possibly non-current)
// session in this workspace.
func (app *App) MarkSessionSeen(ctx context.Context, sessionID string) error {
	if err := app.Sessions.MarkSeen(ctx, sessionID); err != nil {
		return fmt.Errorf("marking session seen: %w", err)
	}
	return nil
}

func (app *App) UpdateAgentModel(ctx context.Context) error {
	if app.AgentCoordinator == nil {
		return fmt.Errorf("agent configuration is missing")
	}
	// Apply when idle: if the agent is mid-run, a server-side goroutine
	// blocks until it finishes and then applies, so the model/reasoning
	// change takes effect before the next user message instead of erroring
	// with "agent busy". Returns immediately so the RPC doesn't hang.
	_, err := app.AgentCoordinator.UpdateModelsWhenIdle(ctx)
	return err
}

// GetDefaultSmallModel returns the default small model for the given
// provider. Falls back to the large model if no default is found.
func (app *App) GetDefaultSmallModel(providerID string) config.SelectedModel {
	cfg := app.config.Config()
	largeModelCfg := cfg.Models[config.SelectedModelTypeLarge]

	// Find the provider in the known providers list to get its default small model.
	knownProviders, _ := config.Providers(cfg)
	var knownProvider *catwalk.Provider
	for _, p := range knownProviders {
		if string(p.ID) == providerID {
			knownProvider = &p
			break
		}
	}

	// For unknown/local providers, use the large model as small.
	if knownProvider == nil {
		slog.Warn("Using large model as small model for unknown provider", "provider", providerID, "model", largeModelCfg.Model)
		return largeModelCfg
	}

	defaultSmallModelID := knownProvider.DefaultSmallModelID
	model := cfg.GetModel(providerID, defaultSmallModelID)
	if model == nil {
		slog.Warn("Default small model not found, using large model", "provider", providerID, "model", largeModelCfg.Model)
		return largeModelCfg
	}

	slog.Info("Using provider default small model", "provider", providerID, "model", defaultSmallModelID)
	return config.SelectedModel{
		Provider:        providerID,
		Model:           defaultSmallModelID,
		MaxTokens:       model.DefaultMaxTokens,
		ReasoningEffort: model.DefaultReasoningEffort,
	}
}

func (app *App) setupEvents() {
	ctx, cancel := context.WithCancel(app.globalCtx)
	app.eventsCtx = ctx
	app.subscribe(ctx, "sessions", app.Sessions.Subscribe)
	app.subscribe(ctx, "messages", app.Messages.Subscribe)
	app.subscribe(ctx, "permissions", app.Permissions.Subscribe)
	app.subscribe(ctx, "permissions-notifications", app.Permissions.SubscribeNotifications)
	app.subscribe(ctx, "questions", app.Questions.Subscribe)
	app.subscribe(ctx, "questions-notifications", app.Questions.SubscribeNotifications)
	app.subscribe(ctx, "history", app.History.Subscribe)
	app.subscribe(ctx, "fork-progress", app.Forks.SubscribeProgress)
	app.subscribe(ctx, "agent-notifications", app.agentNotifications.Subscribe)
	app.subscribeMustDeliver(ctx, "run-completions", app.runCompletions.Subscribe)
	app.subscribe(ctx, "mcp", mcp.SubscribeEvents)
	app.subscribe(ctx, "lsp", app.SubscribeLSPEvents)
	if app.Skills != nil {
		app.subscribe(ctx, "skills", app.Skills.SubscribeEvents)
	}
	cleanupFunc := func(context.Context) error {
		cancel()
		app.serviceEventsWG.Wait()
		app.events.Shutdown()
		return nil
	}
	app.cleanupFuncs = append(app.cleanupFuncs, cleanupFunc)
}

// subscribe fans a service's event stream into the shared app.events
// broker on app.serviceEventsWG, re-publishing each upstream event as a
// tea.Msg. The goroutine exits when ctx is cancelled or the upstream
// channel closes. It is a generic method (Go 1.27) so it can live in the
// App namespace while still inferring the upstream event type T.
func (app *App) subscribe[T any](
	ctx context.Context,
	name string,
	subscriber func(context.Context) <-chan pubsub.Event[T],
) {
	app.serviceEventsWG.Go(func() {
		subCh := subscriber(ctx)
		for {
			select {
			case event, ok := <-subCh:
				if !ok {
					slog.Debug("Subscription channel closed", "name", name)
					return
				}
				app.events.Publish(pubsub.UpdatedEvent, tea.Msg(event))
			case <-ctx.Done():
				slog.Debug("Subscription cancelled", "name", name)
				return
			}
		}
	})
}

// subscribeMustDeliver is the bounded-blocking fan-in variant of
// [App.subscribe]: it re-publishes upstream events onto the shared
// app.events broker using PublishMustDeliver instead of Publish. Use
// this for terminal events that subscribers cannot tolerate losing —
// notably RunComplete, which is the authoritative end-of-run signal
// for `crush run`. A lossy fan-in here can drop the only terminal
// event and hang non-interactive clients waiting on it.
func (app *App) subscribeMustDeliver[T any](
	ctx context.Context,
	name string,
	subscriber func(context.Context) <-chan pubsub.Event[T],
) {
	app.serviceEventsWG.Go(func() {
		subCh := subscriber(ctx)
		for {
			select {
			case event, ok := <-subCh:
				if !ok {
					slog.Debug("Subscription channel closed", "name", name)
					return
				}
				app.events.PublishMustDeliver(ctx, pubsub.UpdatedEvent, tea.Msg(event))
			case <-ctx.Done():
				slog.Debug("Subscription cancelled", "name", name)
				return
			}
		}
	})
}

func (app *App) InitCoderAgent(ctx context.Context) error {
	coderAgentCfg := app.config.Config().Agents[config.AgentCoder]
	if coderAgentCfg.ID == "" {
		return fmt.Errorf("coder agent configuration is missing")
	}
	var err error
	app.AgentCoordinator, err = agent.NewCoordinator(
		ctx,
		app.config,
		app.Sessions,
		app.Messages,
		app.Checkpoints,
		app.Permissions,
		app.Questions,
		app.History,
		app.FileTracker,
		app.Milestones,
		app.LSPManager,
		app.embeddings,
		app.agentNotifications,
		app.runCompletions,
		app.Skills,
		app.Worktrees,
		app.workingDir,
	)
	if err != nil {
		slog.Error("Failed to create coder agent", "err", err)
		return err
	}
	return nil
}

// Subscribe sends events to the TUI as tea.Msgs.
func (app *App) Subscribe(program *tea.Program) {
	defer log.RecoverPanic("app.Subscribe", func() {
		slog.Info("TUI subscription panic: attempting graceful shutdown")
		program.Quit()
	})

	app.tuiWG.Add(1)
	tuiCtx, tuiCancel := context.WithCancel(app.globalCtx)
	app.cleanupFuncs = append(app.cleanupFuncs, func(context.Context) error {
		slog.Debug("Cancelling TUI message handler")
		tuiCancel()
		app.tuiWG.Wait()
		return nil
	})
	defer app.tuiWG.Done()

	events := app.events.Subscribe(tuiCtx)
	for {
		select {
		case <-tuiCtx.Done():
			slog.Debug("TUI message handler shutting down")
			return
		case ev, ok := <-events:
			if !ok {
				slog.Debug("TUI message channel closed")
				return
			}
			program.Send(ev.Payload)
		}
	}
}

// Shutdown performs a graceful shutdown of the application.
func (app *App) Shutdown() {
	start := time.Now()
	defer func() { slog.Debug("Shutdown took " + time.Since(start).String()) }()

	// First, cancel all agents and wait for them to finish. This must complete
	// before closing the DB so agents can finish writing their state.
	if app.AgentCoordinator != nil {
		app.AgentCoordinator.CancelAll()
	}

	// Shared shutdown context for all timeout-bounded cleanup.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Drain any debounced message updates before the DB-close cleanup
	// runs in the parallel block below. message.Service buffers
	// streaming deltas (see internal/message/message.go) and we must
	// land them while the connection is still open.
	if app.Messages != nil {
		if err := app.Messages.FlushAll(shutdownCtx); err != nil {
			slog.Error("Failed to flush pending message updates on shutdown", "error", err)
		}
	}

	// Now run remaining cleanup tasks in parallel.
	var wg sync.WaitGroup

	// Send exit event
	wg.Go(func() {
	})

	// Kill all background shells.
	wg.Go(func() {
		shell.GetBackgroundShellManager().KillAll(shutdownCtx)
	})

	// Shutdown all LSP clients.
	wg.Go(func() {
		app.LSPManager.KillAll(shutdownCtx)
	})

	// Call all cleanup functions.
	for _, cleanup := range app.cleanupFuncs {
		if cleanup != nil {
			wg.Go(func() {
				if err := cleanup(shutdownCtx); err != nil {
					slog.Error("Failed to cleanup app properly on shutdown", "error", err)
				}
			})
		}
	}
	wg.Wait()
}

// checkForUpdates checks for available updates.
func (app *App) checkForUpdates(ctx context.Context) {
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	info, err := update.Check(checkCtx, version.Version, update.Default)
	if err != nil || !info.Available() {
		return
	}
	app.events.Publish(pubsub.UpdatedEvent, UpdateAvailableMsg{
		CurrentVersion: info.Current,
		LatestVersion:  info.Latest,
		IsDevelopment:  info.IsDevelopment(),
	})
}

// DB returns the workspace database connection.
func (app *App) DB() *sql.DB {
	return app.dbConn
}
