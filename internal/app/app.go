// Package app wires together services, coordinates agents, and manages
// application lifecycle.
package app

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/charmbracelet/x/term"
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
	"github.com/taigrr/crush/internal/format"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/historysearch"
	"github.com/taigrr/crush/internal/journal"
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
	"github.com/taigrr/crush/internal/ui/anim"
	"github.com/taigrr/crush/internal/ui/styles/themes"
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

	// Journal persists the coder agent's prompt queue and swarm reply
	// obligations so they survive a graceful server swap. Nil in
	// test-only apps built without a database.
	Journal *journal.Store

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
	globalCtx          context.Context
	cleanupFuncs       []func(context.Context) error
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
	permissions := permission.NewPermissionService(store.WorkingDir(), skipPermissionsRequests, allowedTools)
	if cfg.Permissions != nil && cfg.Permissions.Sysadmin {
		permissions.SetSysadminMode(true)
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
		Permissions: permissions,
		Questions:   question.NewQuestionService(),
		FileTracker: filetracker.NewService(q),
		Checkpoints: checkpoints,
		Worktrees:   worktrees,
		Forks:       forks,
		Milestones:  milestone.NewService(conn, q),
		Journal:     journal.New(conn, q, store.Config().Options.DataDirectory),
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
	go func() {
		if err := app.backfillSwarmIdentities(context.Background()); err != nil {
			slog.Warn("Swarm identity backfill failed", "error", err)
		}
	}()

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
			updateLSPState(name, lsp.StateUnstarted, nil, nil, 0)
			return
		}
		client.SetDiagnosticsCallback(updateLSPDiagnostics)
		updateLSPState(name, client.GetServerState(), nil, client, 0)
	})
	go app.LSPManager.TrackConfigured()

	// Validate worktree state on startup.
	if worktrees != nil && worktrees.IsEnabled() {
		if err := worktrees.ValidateState(ctx); err != nil {
			slog.Warn("Worktree validation failed", "error", err)
		}
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

// searchSessionCandidateFactor over-fetches message-level hits relative
// to the number of sessions requested so the per-session collapse can
// pick each session's globally best hit rather than just its best within
// a small page. searchMinCandidates is the floor so short session limits
// still draw from a healthy candidate pool.
const (
	searchSessionCandidateFactor = 10
	searchMinCandidates          = 200
	searchDefaultSessionLimit    = 50
	// searchMaxSessionLimit caps the client-supplied Limit so a hostile
	// or buggy caller can't force an unbounded candidate fetch over the
	// whole message corpus. SearchHistory is a public-ish RPC; Limit
	// comes off the wire.
	searchMaxSessionLimit = 200
)

// ResolveSearchLimits turns a client-supplied session limit into the
// effective (sessionLimit, candidateLimit) pair: it applies the default
// when non-positive, clamps to searchMaxSessionLimit so a hostile Limit
// can't force an unbounded candidate fetch, then derives the over-fetch
// candidate window. Exported so the cross-workspace fan-out (backend) can
// size its per-workspace candidate fetch identically.
func ResolveSearchLimits(limit int) (sessionLimit, candidateLimit int) {
	sessionLimit = limit
	if sessionLimit <= 0 {
		sessionLimit = searchDefaultSessionLimit
	}
	if sessionLimit > searchMaxSessionLimit {
		sessionLimit = searchMaxSessionLimit
	}
	candidateLimit = max(sessionLimit*searchSessionCandidateFactor, searchMinCandidates)
	return sessionLimit, candidateLimit
}

// SearchHistory runs hybrid (substring + semantic) search over this
// workspace's conversation history and collapses the per-message hits to
// one representative hit per session (best-hit-wins). It over-fetches
// message-level candidates and collapses before applying the session
// limit, so each session's representative is its best matching message
// across the candidate window rather than merely within a fixed page.
// The returned hits carry no workspace tag; the backend stamps
// WorkspaceID/WorkspaceRoot so cross-workspace callers can group and
// route results.
func (app *App) SearchHistory(ctx context.Context, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error) {
	sessionLimit, candidateLimit := ResolveSearchLimits(params.Limit)

	res, err := app.SearchHistoryHits(ctx, params.Query, historysearch.Scope(params.Scope), params.Semantic, candidateLimit)
	if err != nil {
		return proto.SearchHistoryResult{}, err
	}
	return collapseToSessions(res, sessionLimit), nil
}

// SearchHistoryHits runs the hybrid search over this workspace and returns
// the RAW, ranked message-level hits without the per-session collapse. The
// cross-workspace fan-out uses this so it can merge message-level hits from
// several workspaces and re-rank globally before collapsing once. Offset is
// intentionally not forwarded (see SearchHistory).
func (app *App) SearchHistoryHits(ctx context.Context, query string, scope historysearch.Scope, semantic *bool, candidateLimit int) (embedding.SearchResult, error) {
	emb := embedding.Build(db.New(app.dbConn), app.config.EmbeddingParams())
	return historysearch.Search(ctx, app.Messages, app.Sessions, emb, query, historysearch.Options{
		Scope:    scope,
		Semantic: semantic,
		Limit:    candidateLimit,
		Offset:   0,
	})
}

// collapseToSessions dedups per-message hits by session, keeping the
// top-scoring hit as each session's representative, then caps the result
// to sessionLimit rows. Input hits are already ranked by fused score, so
// the first hit seen for a session is its best within the candidate
// window. Total reflects the number of distinct sessions found in that
// window (an approximation when the corpus exceeds the candidate limit).
func collapseToSessions(res embedding.SearchResult, sessionLimit int) proto.SearchHistoryResult {
	seen := make(map[string]struct{}, len(res.Hits))
	hits := make([]proto.SessionHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		if _, ok := seen[h.SessionID]; ok {
			continue
		}
		seen[h.SessionID] = struct{}{}
		hits = append(hits, proto.SessionHit{
			SessionID:    h.SessionID,
			SessionTitle: h.SessionTitle,
			Score:        h.Score,
			Match:        string(h.Match),
			Snippet:      h.Snippet,
			MessageID:    h.SourceID,
			Role:         h.Role,
			CreatedAt:    h.CreatedAt,
		})
	}
	total := len(hits)
	if sessionLimit > 0 && len(hits) > sessionLimit {
		hits = hits[:sessionLimit]
	}
	return proto.SearchHistoryResult{
		Hits:         hits,
		Total:        total,
		SemanticUsed: res.SemanticUsed,
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

// resolveSession resolves which session to use for a non-interactive run
// If continueSessionID is set, it looks up that session by ID
// If useLast is set, it returns the most recently updated top-level session
// Otherwise, it creates a new session
func (app *App) resolveSession(ctx context.Context, continueSessionID string, useLast bool) (session.Session, error) {
	switch {
	case continueSessionID != "":
		if app.Sessions.IsAgentToolSession(continueSessionID) {
			return session.Session{}, fmt.Errorf("cannot continue an agent tool session: %s", continueSessionID)
		}
		sess, err := app.Sessions.Get(ctx, continueSessionID)
		if err != nil {
			return session.Session{}, fmt.Errorf("session not found: %s", continueSessionID)
		}
		if sess.ParentSessionID != "" {
			return session.Session{}, fmt.Errorf("cannot continue a child session: %s", continueSessionID)
		}
		return sess, nil

	case useLast:
		sess, err := app.Sessions.GetLast(ctx)
		if err != nil {
			return session.Session{}, fmt.Errorf("no sessions found to continue")
		}
		return sess, nil

	default:
		return app.Sessions.Create(ctx, agent.DefaultSessionName)
	}
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

// streamDelta computes the incremental text to print for a streamed assistant
// message. content is the full accumulated message content; readBytes is how
// many bytes were already consumed for this message. It returns the new
// delta to print, the updated read-bytes count, and whether anything has been
// printed yet (carried across calls via alreadyPrinted).
//
// The first bytes of a message have their leading whitespace trimmed because
// models often emit indentation/formatting we don't want at the start. A
// whitespace-only delta is suppressed until something real has been printed,
// after which deltas are forwarded verbatim. It is an error for content to be
// shorter than readBytes, which would indicate the message shrank.
func streamDelta(content string, readBytes int, alreadyPrinted bool) (delta string, newReadBytes int, printed bool, err error) {
	if len(content) < readBytes {
		return "", readBytes, alreadyPrinted, fmt.Errorf("message content is shorter than read bytes: %d < %d", len(content), readBytes)
	}

	part := content[readBytes:]
	if readBytes == 0 {
		part = strings.TrimLeft(part, " \t")
	}

	printed = alreadyPrinted
	if alreadyPrinted || strings.TrimSpace(part) != "" {
		printed = true
	} else {
		// Suppress whitespace-only output until real content appears, but
		// still advance the cursor so later diffs are correct.
		part = ""
	}
	return part, len(content), printed, nil
}

// RunNonInteractive runs the application in non-interactive mode with the
// given prompt, printing to stdout.
func (app *App) RunNonInteractive(ctx context.Context, output io.Writer, prompt, largeModel, smallModel string, hideSpinner bool, continueSessionID string, useLast bool) error {
	slog.Info("Running in non-interactive mode")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if largeModel != "" || smallModel != "" {
		if err := app.overrideModelsForNonInteractive(ctx, largeModel, smallModel); err != nil {
			return fmt.Errorf("failed to override models: %w", err)
		}
	}

	var (
		spinner   *format.Spinner
		stdoutTTY bool
		stderrTTY bool
		stdinTTY  bool
		progress  bool
	)

	if f, ok := output.(*os.File); ok {
		stdoutTTY = term.IsTerminal(f.Fd())
	}
	stderrTTY = term.IsTerminal(os.Stderr.Fd())
	stdinTTY = term.IsTerminal(os.Stdin.Fd())
	progress = app.config.Config().Options.Progress == nil || *app.config.Config().Options.Progress

	if !hideSpinner && stderrTTY {
		t := themes.ThemeForProvider(app.config.Config().Models[config.SelectedModelTypeLarge].Provider)

		// Detect background color to set the appropriate color for the
		// spinner's 'Generating...' text. Without this, that text would be
		// unreadable in light terminals.
		hasDarkBG := true
		if f, ok := output.(*os.File); ok && stdinTTY && stdoutTTY {
			hasDarkBG = lipgloss.HasDarkBackground(os.Stdin, f)
		}
		defaultFG := lipgloss.LightDark(hasDarkBG)(charmtone.Pepper, t.WorkingLabelColor)

		spinner = format.NewSpinner(ctx, cancel, anim.Settings{
			Size:         10,
			Label:        "Generating",
			LabelColor:   defaultFG,
			GradColorA:   t.WorkingGradFromColor,
			GradColorB:   t.WorkingGradToColor,
			CycleColors:  true,
			LowBandwidth: app.config.Config().LowBandwidthEnabled(),
		})
		spinner.Start()
	}

	// Helper function to stop spinner once.
	stopSpinner := func() {
		if !hideSpinner && spinner != nil {
			spinner.Stop()
			spinner = nil
		}
	}

	// Wait for MCP initialization to complete before reading MCP tools.
	if err := mcp.WaitForInit(ctx); err != nil {
		return fmt.Errorf("failed to wait for MCP initialization: %w", err)
	}

	// force update of agent models before running so mcp tools are loaded
	app.AgentCoordinator.UpdateModels(ctx)

	defer stopSpinner()

	sess, err := app.resolveSession(ctx, continueSessionID, useLast)
	if err != nil {
		return fmt.Errorf("failed to create session for non-interactive mode: %w", err)
	}

	if continueSessionID != "" || useLast {
		slog.Info("Continuing session for non-interactive run", "session_id", sess.ID)
	} else {
		slog.Info("Created session for non-interactive run", "session_id", sess.ID)
	}

	// Automatically approve all permission requests for this non-interactive
	// session.
	app.Permissions.AutoApproveSession(sess.ID)

	type response struct {
		result *fantasy.AgentResult
		err    error
	}
	done := make(chan response, 1)

	go func(ctx context.Context, sessionID, prompt string) {
		result, err := app.AgentCoordinator.Run(ctx, sess.ID, prompt)
		if err != nil {
			done <- response{
				err: fmt.Errorf("failed to start agent processing stream: %w", err),
			}
			return
		}
		done <- response{
			result: result,
		}
	}(ctx, sess.ID, prompt)

	messageEvents := app.Messages.Subscribe(ctx)
	messageReadBytes := make(map[string]int)
	var printed bool

	defer func() {
		if progress && stderrTTY {
			_, _ = fmt.Fprintf(os.Stderr, ansi.ResetProgressBar)
		}

		// Always print a newline at the end. If output is a TTY this will
		// prevent the prompt from overwriting the last line of output.
		_, _ = fmt.Fprintln(output)
	}()

	for {
		if progress && stderrTTY {
			// HACK: Reinitialize the terminal progress bar on every iteration
			// so it doesn't get hidden by the terminal due to inactivity.
			_, _ = fmt.Fprintf(os.Stderr, ansi.SetIndeterminateProgressBar)
		}

		select {
		case result := <-done:
			stopSpinner()
			if result.err != nil {
				if errors.Is(result.err, context.Canceled) || errors.Is(result.err, agent.ErrRequestCancelled) {
					slog.Debug("Non-interactive: agent processing cancelled", "session_id", sess.ID)
					return nil
				}
				return fmt.Errorf("agent processing failed: %w", result.err)
			}
			return nil

		case event := <-messageEvents:
			msg := event.Payload
			if msg.SessionID == sess.ID && msg.Role == message.Assistant && len(msg.Parts) > 0 {
				stopSpinner()

				content := msg.Content().String()
				part, n, nowPrinted, err := streamDelta(content, messageReadBytes[msg.ID], printed)
				if err != nil {
					slog.Error("Non-interactive: message content is shorter than read bytes", "message_length", len(content), "read_bytes", messageReadBytes[msg.ID])
					return err
				}
				if part != "" {
					fmt.Fprint(output, part)
				}
				printed = nowPrinted
				messageReadBytes[msg.ID] = n
			}

		case <-ctx.Done():
			stopSpinner()
			return ctx.Err()
		}
	}
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

// RefreshAgent re-applies configuration (models, system prompt, tools)
// to the existing coder agent, deferring until idle if it is busy. Falls
// back to a model update for coordinators without Refresh.
func (app *App) RefreshAgent(ctx context.Context) error {
	if app.AgentCoordinator == nil {
		return fmt.Errorf("agent configuration is missing")
	}
	if r, ok := app.AgentCoordinator.(interface {
		Refresh(context.Context) (bool, error)
	}); ok {
		_, err := r.Refresh(ctx)
		return err
	}
	return app.UpdateAgentModel(ctx)
}

// overrideModelsForNonInteractive parses the model strings and temporarily
// overrides the model configurations, then rebuilds the agent.
// Format: "model-name" (searches all providers) or "provider/model-name".
// Model matching is case-insensitive.
// If largeModel is provided but smallModel is not, the small model defaults to
// the provider's default small model.
func (app *App) overrideModelsForNonInteractive(ctx context.Context, largeModel, smallModel string) error {
	providers := app.config.Config().Providers.Copy()

	largeMatches, smallMatches, err := findModels(providers, largeModel, smallModel)
	if err != nil {
		return err
	}

	var largeProviderID string

	// Override large model.
	if largeModel != "" {
		found, err := validateMatches(largeMatches, largeModel, "large")
		if err != nil {
			return err
		}
		largeProviderID = found.provider
		slog.Info("Overriding large model for non-interactive run", "provider", found.provider, "model", found.modelID)
		app.config.Config().Models[config.SelectedModelTypeLarge] = config.SelectedModel{
			Provider: found.provider,
			Model:    found.modelID,
		}
	}

	// Override small model.
	switch {
	case smallModel != "":
		found, err := validateMatches(smallMatches, smallModel, "small")
		if err != nil {
			return err
		}
		slog.Info("Overriding small model for non-interactive run", "provider", found.provider, "model", found.modelID)
		app.config.Config().Models[config.SelectedModelTypeSmall] = config.SelectedModel{
			Provider: found.provider,
			Model:    found.modelID,
		}

	case largeModel != "":
		// No small model specified, but large model was - use provider's default.
		smallCfg := app.GetDefaultSmallModel(largeProviderID)
		app.config.Config().Models[config.SelectedModelTypeSmall] = smallCfg
	}

	return app.AgentCoordinator.UpdateModels(ctx)
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
	app.subscribe(ctx, "lsp", SubscribeLSPEvents)
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
		app.journalForCoordinator(),
		app.workingDir,
	)
	if err != nil {
		slog.Error("Failed to create coder agent", "err", err)
		return err
	}
	return nil
}

// journalForCoordinator returns the journal as the coordinator's
// interface, or a nil interface when no store exists, so the
// coordinator's nil check works (a typed nil pointer would not).
func (app *App) journalForCoordinator() agent.Journal {
	if app.Journal == nil {
		return nil
	}
	return app.Journal
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
