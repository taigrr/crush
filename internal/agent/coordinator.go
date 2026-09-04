package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/hyper"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/prompt"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/checkpoint"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/filetracker"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/hooks"
	"github.com/taigrr/crush/internal/log"
	"github.com/taigrr/crush/internal/lsp"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/milestone"
	"github.com/taigrr/crush/internal/oauth/copilot"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/swarm"
	"github.com/taigrr/crush/internal/worktree"
	"github.com/taigrr/fantasy"
	"golang.org/x/sync/errgroup"

	openaisdk "github.com/charmbracelet/openai-go/option"
	"github.com/qjebbs/go-jsons"
	"github.com/taigrr/fantasy/providers/anthropic"
	"github.com/taigrr/fantasy/providers/azure"
	"github.com/taigrr/fantasy/providers/bedrock"
	"github.com/taigrr/fantasy/providers/google"
	"github.com/taigrr/fantasy/providers/openai"
	"github.com/taigrr/fantasy/providers/openaicompat"
	"github.com/taigrr/fantasy/providers/openrouter"
	"github.com/taigrr/fantasy/providers/vercel"
)

// Coordinator errors.
var (
	errCoderAgentNotConfigured         = errors.New("coder agent not configured")
	errModelProviderNotConfigured      = errors.New("model provider not configured")
	errLargeModelNotSelected           = errors.New("large model not selected")
	errSmallModelNotSelected           = errors.New("small model not selected")
	errLargeModelProviderNotConfigured = errors.New("large model provider not configured")
	errSmallModelProviderNotConfigured = errors.New("small model provider not configured")
	errLargeModelNotFound              = errors.New("large model not found in provider config")
	errSmallModelNotFound              = errors.New("small model not found in provider config")
)

// Copilot models that use the Responses API instead of Chat Completions.
var copilotResponsesModels = map[string]bool{
	"gpt-5.2":       true,
	"gpt-5.2-codex": true,
	"gpt-5.3-codex": true,
	"gpt-5.4":       true,
	"gpt-5.4-mini":  true,
	"gpt-5.5":       true,
	"gpt-5-mini":    true,
}

// OpenCode models that user Anthropic Messages API instead of Chat Completions.
var opencodeMessagesModels = map[string]bool{
	"qwen3.7-max": true,
}

type Coordinator interface {
	// INFO: (kujtim) this is not used yet we will use this when we have multiple agents
	// SetMainAgent(string)
	Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	// RunAccepted runs a call that was already accepted via
	// BeginAccepted on the fire-and-forget dispatch path. The handle is
	// the only carrier of accept-state across the backend.runAgent /
	// Coordinator / sessionAgent.Run layers: it reaches
	// sessionAgent.Run as SessionAgentCall.Accepted, where it is
	// consumed under dispatchMu once the accepted -> (cancel-on-entry |
	// queued | active) transition is chosen.
	RunAccepted(ctx context.Context, accept *AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	BeginAccepted(sessionID string) *AcceptedRun
	Cancel(sessionID string)
	CancelAll()
	// SoftInterrupt asks the tools running in the session's current step
	// to wrap up early without cancelling them; see
	// SessionAgent.SoftInterrupt.
	SoftInterrupt(sessionID string)
	IsSessionBusy(sessionID string) bool
	// IsSessionBusyOrAccepted also counts a run that has been accepted
	// (dispatched) but not yet registered as active. Observers listing
	// sessions should use this so a just-dispatched turn is reported
	// busy; Run's own queue/idle decision must keep using IsSessionBusy.
	IsSessionBusyOrAccepted(sessionID string) bool
	IsBusy() bool
	QueuedPrompts(sessionID string) int
	QueuedPromptsList(sessionID string) []string
	ClearQueue(sessionID string)
	Summarize(context.Context, string) error
	RegenerateTitle(ctx context.Context, sessionID string) error
	Model() Model
	UpdateModels(ctx context.Context) error
	UpdateModelsWhenIdle(ctx context.Context) (deferred bool, err error)

	// Goal control for the autonomous /goal feature.
	SetGoal(sessionID, condition string)
	ClearGoal(sessionID string)
	GoalStatus(sessionID string) (condition string, turns, maxTurns int, active bool)
}

// SwarmConfigurable is implemented by concrete coordinators that
// support the cross-workspace swarm tool. It is deliberately a
// side-channel interface (rather than a method on [Coordinator]) so
// test mocks don't have to know about swarm.
type SwarmConfigurable interface {
	// SetSwarmBackend wires the swarm dispatcher and refreshes the
	// coder agent's tool set so the swarm tool takes effect on the
	// very next turn. Refreshing tools (rather than swapping the
	// agent pointer) uses the csync-guarded tool slice so it is
	// safe to call while a run is in flight.
	SetSwarmBackend(ctx context.Context, be tools.SwarmBackend, workspaceID string, cfg func() swarm.Config) error
	// SwarmWired reports whether a non-nil swarm dispatcher is
	// currently wired in. Used to verify that workspace creation
	// injected the backend without having to inspect the tool set.
	SwarmWired() bool
	// WireSwarmBackendIfMissing calls SetSwarmBackend only when
	// SwarmWired reports false, deferring the rebuild until the
	// coder agent is idle if it's currently busy. Safe (and cheap)
	// to call on an already-wired, possibly busy, long-lived
	// coordinator — see the method doc for why that distinction
	// matters.
	WireSwarmBackendIfMissing(ctx context.Context, be tools.SwarmBackend, workspaceID string, cfg func() swarm.Config) error
}

type coordinator struct {
	cfg         *config.ConfigStore
	sessions    session.Service
	messages    message.Service
	checkpoints checkpoint.Service
	permissions permission.Service
	questions   question.Service
	history     history.Service
	filetracker filetracker.Service
	milestones  milestone.Service
	lspManager  *lsp.Manager
	worktrees   worktree.Service
	embeddings  embedding.Service
	notify      pubsub.Publisher[notify.Notification]
	runComplete pubsub.Publisher[notify.RunComplete]

	currentAgent SessionAgent
	agents       map[string]SessionAgent

	// overrideCache memoizes models built for per-call `model` overrides
	// (the agent/review tools) within one UpdateModels cycle. It is
	// cleared on every UpdateModels, which runs before each top-level
	// turn and after any credential refresh, so a stale provider is
	// never served. Within a turn, N sub-agent calls naming the same
	// model share one provider/LanguageModel. Keyed by overrideCacheKey.
	overrideCache *csync.Map[string, Model]

	// Skills discovery results (session-start snapshot).
	allSkills    []*skills.Skill // Pre-filter: all discovered after dedup.
	activeSkills []*skills.Skill // Post-filter: active skills only.
	skillTracker *skills.Tracker

	// effectiveWorkingDir is the working directory for tools and shell
	// commands. For user-created linked worktrees this is the actual cwd
	// the user launched from, which may differ from cfg.WorkingDir()
	// (the project root hosting .crush/). Empty means use cfg.WorkingDir().
	effectiveWorkingDir string

	// readyWg gates runs on the asynchronous system-prompt and tool
	// build started by buildAgent (and by Refresh). It is a pointer
	// guarded by readyMu so Refresh can install a fresh group — an
	// errgroup caches its first error forever, so a one-off MCP or
	// prompt failure would otherwise poison every future run until the
	// workspace is torn down.
	readyMu sync.Mutex
	readyWg *errgroup.Group

	// parentCostMu serializes the read-modify-write in
	// updateParentSessionCost. Sub-agents can run concurrently (the
	// review tool fans out to N reviewers in parallel), and each
	// accumulates its child cost onto the shared parent session; without
	// this lock the last writer wins and costs are silently dropped.
	parentCostMu sync.Mutex

	// swarmBackend, when non-nil and !isSubAgent, causes the swarm
	// tool to be registered on the coder agent. It is set
	// post-construction by the backend (which owns the cross-workspace
	// index) via SetSwarmBackend so the coordinator doesn't have to
	// import the backend package. workspaceID is stamped onto every
	// outgoing swarm message so the receiving side knows where it
	// originated.
	swarmBackend     tools.SwarmBackend
	swarmWorkspaceID string
	// swarmConfig returns the runtime swarm identity config (theme
	// palette + animals). Nil means "use defaults".
	swarmConfig func() swarm.Config
	// swarmMu guards the swarmBackend/workspaceID/config triple, plus
	// swarmWiring, so SetSwarmBackend (which can be called from the
	// backend at any time) races cleanly against buildAgent reads on
	// the run goroutine.
	swarmMu sync.Mutex
	// swarmWiring is true while a WireSwarmBackendIfMissing call is
	// in flight (synchronously or deferred behind WaitForIdle) for
	// this coordinator, so a second concurrent caller doesn't launch
	// a redundant deferred goroutine or race the first's rebuild.
	swarmWiring bool

	// swarmReplies tracks reply obligations created by require_reply
	// swarm messages: a session that received one may not end its turn
	// until it has messaged the sender back. Registered on delivery in
	// run, fulfilled by the swarm tool, enforced by the end-of-turn
	// continuation loop.
	swarmReplies *swarm.ReplyTracker

	// journal, when non-nil, persists the coder agent's prompt queue
	// and the reply tracker so both survive a graceful server swap.
	journal Journal
}

// Journal is the persistence sink for the coordinator's transient
// state (queued prompts and swarm reply obligations). *journal.Store
// satisfies it; nil disables persistence.
type Journal interface {
	QueueJournal
	swarm.ReplyJournal
}

// PauseQueueDispatch implements [Drainable].
func (c *coordinator) PauseQueueDispatch() {
	if d, ok := c.currentAgent.(interface{ PauseQueueDispatch() }); ok {
		d.PauseQueueDispatch()
	}
}

// DetachJournals implements [Drainable].
func (c *coordinator) DetachJournals() {
	if d, ok := c.currentAgent.(interface{ DetachQueueJournal() }); ok {
		d.DetachQueueJournal()
	}
	c.swarmReplies.DetachJournal()
}

// swarmPartsSteer reports whether any swarm part was sent in btw mode,
// which is what Backend.SwarmSend maps to a steer on the live path.
func swarmPartsSteer(parts []message.SwarmMessage) bool {
	for _, p := range parts {
		if p.BTW {
			return true
		}
	}
	return false
}

// DeferPrompt implements [Drainable].
func (c *coordinator) DeferPrompt(sessionID, runID, prompt string, attachments []message.Attachment, parts []message.SwarmMessage) {
	c.deferPrompt(sessionID, runID, prompt, attachments, parts, false)
}

// RequeueFront implements [Drainable].
func (c *coordinator) RequeueFront(sessionID, runID, prompt string, attachments []message.Attachment, parts []message.SwarmMessage) {
	c.deferPrompt(sessionID, runID, prompt, attachments, parts, true)
}

func (c *coordinator) deferPrompt(sessionID, runID, prompt string, attachments []message.Attachment, parts []message.SwarmMessage, front bool) {
	// A deferred call runs via the recursive queue hand-off inside
	// sessionAgent, never through coordinator.run, so the reply
	// obligations its swarm parts demand are recorded here — as
	// undelivered, since the agent has not seen the message: enforcing
	// them at the end of the CURRENT turn would nudge the agent about a
	// message it never received and then forward a bogus reply. The
	// OnQueueDispatch hook flips them to delivered when the call runs.
	c.registerUndeliveredReplyObligations(sessionID, parts)
	d, ok := c.currentAgent.(interface{ deferCall(SessionAgentCall, bool) })
	if !ok {
		return
	}
	call := SessionAgentCall{
		SessionID:   sessionID,
		RunID:       runID,
		Prompt:      prompt,
		Attachments: attachments,
		SwarmParts:  parts,
		// A btw aside is a steer wherever it enters the queue: it still
		// waits for its own turn here (it carries a RunID), but it wakes
		// the target's running tools so that turn ends sooner.
		Steer: swarmPartsSteer(parts),
	}
	// Same model/tuning a live prompt gets; without it the deferred
	// call would run with no max-tokens and no provider options.
	if tuning, _, err := c.callTuning(context.Background(), sessionID); err == nil {
		call = tuning.apply(call)
		call.Attachments = tuning.filterAttachments(call.Attachments)
	} else {
		slog.Warn("Deferring prompt without model tuning", "session_id", sessionID, "error", err)
	}
	d.deferCall(call, front)
}

// BusySessions implements [Drainable].
func (c *coordinator) BusySessions() []string {
	if d, ok := c.currentAgent.(interface{ BusySessions() []string }); ok {
		return d.BusySessions()
	}
	return nil
}

// SetSwarmBackend wires the cross-workspace swarm dispatcher into
// the coordinator and refreshes the coder agent's tool set so the
// swarm tool takes effect immediately. Refreshing tools (rather than
// rebuilding the agent) matches UpdateModels's approach and avoids
// swapping the c.currentAgent pointer that other goroutines read
// without locking. Guarded by swarmMu so concurrent callers
// serialise. Passing nil be removes the tool on the next refresh.
//
// On failure (buildTools error, or the coder agent not configured),
// the swarmBackend/workspaceID/config triple is rolled back to its
// pre-call value rather than left holding the failed attempt's
// (non-nil) be. This matters because SwarmWired reports readiness
// purely from swarmBackend != nil: if a failed call left it non-nil
// anyway, SwarmWired would report "wired" forever even though the
// tool set was never actually refreshed, and callers that gate
// re-wiring on SwarmWired (see WireSwarmBackendIfMissing) would stop
// retrying — silently and permanently losing the swarm tool, which
// is the exact failure mode this rollback prevents.
func (c *coordinator) SetSwarmBackend(ctx context.Context, be tools.SwarmBackend, workspaceID string, cfg func() swarm.Config) error {
	c.swarmMu.Lock()
	prevBackend, prevWorkspaceID, prevConfig := c.swarmBackend, c.swarmWorkspaceID, c.swarmConfig
	c.swarmBackend = be
	c.swarmWorkspaceID = workspaceID
	c.swarmConfig = cfg
	c.swarmMu.Unlock()

	rollback := func() {
		c.swarmMu.Lock()
		c.swarmBackend, c.swarmWorkspaceID, c.swarmConfig = prevBackend, prevWorkspaceID, prevConfig
		c.swarmMu.Unlock()
	}

	// Skip when the coder agent hasn't been built yet (defensive —
	// production callers always InitCoderAgent first). This is not a
	// failure: the fields above are left set so the next buildTools
	// call (once currentAgent exists) picks them up.
	if c.currentAgent == nil {
		return nil
	}
	agentCfg, ok := c.cfg.Config().Agents[config.AgentCoder]
	if !ok {
		rollback()
		return errCoderAgentNotConfigured
	}
	newTools, err := c.buildTools(ctx, agentCfg, false)
	if err != nil {
		rollback()
		return err
	}
	c.currentAgent.SetTools(newTools)
	return nil
}

// SwarmWired reports whether a swarm dispatcher is currently wired in.
func (c *coordinator) SwarmWired() bool {
	c.swarmMu.Lock()
	defer c.swarmMu.Unlock()
	return c.swarmBackend != nil
}

// WireSwarmBackendIfMissing wires be into the coordinator only when a
// swarm backend is not already wired in (per SwarmWired) and no other
// wiring attempt is already in flight for this coordinator. It exists
// so callers that may run on an already-attached, potentially busy
// workspace — e.g. CreateWorkspace's dedup/reuse branches, hit on
// every client reconnect or TUI workspace switch — can self-heal a
// workspace that genuinely lost its swarm wiring without paying for
// (or racing) a full tool-set rebuild on the common "already wired"
// path.
//
// If the coder agent is currently busy, the rebuild is deferred until
// it goes idle (mirroring UpdateModelsWhenIdle) rather than run
// inline: buildTools -> buildAgent schedules new work on the
// coordinator's shared readyWg via readyWg.Go (i.e. wg.Add), and
// every in-flight run blocks on that same readyWg via
// readyWg.Wait() in run(). sync.WaitGroup's contract forbids a
// positive-delta Add starting concurrently with a Wait once the
// counter has reached zero, so rebuilding inline while busy risks
// panicking the process or a run observing a half-populated tool set.
func (c *coordinator) WireSwarmBackendIfMissing(ctx context.Context, be tools.SwarmBackend, workspaceID string, cfg func() swarm.Config) error {
	c.swarmMu.Lock()
	if c.swarmBackend != nil || c.swarmWiring {
		c.swarmMu.Unlock()
		return nil
	}
	c.swarmWiring = true
	c.swarmMu.Unlock()

	clearWiring := func() {
		c.swarmMu.Lock()
		c.swarmWiring = false
		c.swarmMu.Unlock()
	}

	if c.currentAgent != nil && c.currentAgent.IsBusy() {
		go func() {
			defer clearWiring()
			waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := c.currentAgent.WaitForIdle(waitCtx); err != nil {
				slog.Warn("Gave up waiting for agent idle before wiring swarm backend", "error", err)
				return
			}
			if err := c.SetSwarmBackend(context.Background(), be, workspaceID, cfg); err != nil {
				slog.Warn("Failed to wire swarm backend after waiting for idle", "error", err)
			}
		}()
		return nil
	}
	defer clearWiring()
	return c.SetSwarmBackend(ctx, be, workspaceID, cfg)
}

func NewCoordinator(
	ctx context.Context,
	cfg *config.ConfigStore,
	sessions session.Service,
	messages message.Service,
	checkpoints checkpoint.Service,
	permissions permission.Service,
	questions question.Service,
	history history.Service,
	filetracker filetracker.Service,
	milestones milestone.Service,
	lspManager *lsp.Manager,
	embeddings embedding.Service,
	notify pubsub.Publisher[notify.Notification],
	runComplete pubsub.Publisher[notify.RunComplete],
	skillsMgr *skills.Manager,
	worktrees worktree.Service,
	journal Journal,
	effectiveWorkingDir string,
) (Coordinator, error) {
	// Skills are pre-discovered by the caller (see app.New /
	// backend.CreateWorkspace) and passed in via the manager. If no
	// manager was provided (legacy callers), fall back to an in-line
	// discovery so the coordinator still works.
	var allSkills, activeSkills []*skills.Skill
	if skillsMgr != nil {
		allSkills = skillsMgr.AllSkills()
		activeSkills = skillsMgr.ActiveSkills()
	} else {
		allSkills, activeSkills = discoverSkills(cfg)
	}
	skillTracker := skills.NewTracker(activeSkills)

	replies := swarm.NewReplyTracker()
	if journal != nil {
		replies = swarm.NewPersistentReplyTracker(journal)
	}

	c := &coordinator{
		cfg:                 cfg,
		sessions:            sessions,
		messages:            messages,
		checkpoints:         checkpoints,
		permissions:         permissions,
		questions:           questions,
		history:             history,
		filetracker:         filetracker,
		milestones:          milestones,
		lspManager:          lspManager,
		worktrees:           worktrees,
		embeddings:          embeddings,
		notify:              notify,
		runComplete:         runComplete,
		agents:              make(map[string]SessionAgent),
		overrideCache:       csync.NewMap[string, Model](),
		allSkills:           allSkills,
		activeSkills:        activeSkills,
		skillTracker:        skillTracker,
		effectiveWorkingDir: effectiveWorkingDir,
		swarmReplies:        replies,
		journal:             journal,
	}

	agentCfg, ok := cfg.Config().Agents[config.AgentCoder]
	if !ok {
		return nil, errCoderAgentNotConfigured
	}

	// TODO: make this dynamic when we support multiple agents
	prompt, err := coderPrompt(prompt.WithWorkingDir(cmp.Or(effectiveWorkingDir, c.cfg.WorkingDir())))
	if err != nil {
		return nil, err
	}

	agent, err := c.buildAgent(ctx, prompt, agentCfg, false)
	if err != nil {
		return nil, err
	}
	c.currentAgent = agent
	c.agents[config.AgentCoder] = agent
	return c, nil
}

// workingDir resolves the working directory for a turn. If the turn's
// session has an active (Crush-managed) worktree, tools operate inside
// that worktree; otherwise they fall back to the cwd of the client that
// initiated the turn (set via tools.WithWorkingDir), then to the
// session's recorded working dir when the turn carries no client cwd,
// and finally to the workspace defaults. The per-request cwd matters
// because sibling git worktrees collapse to the same project root and
// thus share a single workspace and coordinator: without it every client
// would resolve to whichever client created the workspace first
// (c.effectiveWorkingDir), so launching Crush from a sibling worktree
// could run tools in an unrelated worktree. The session ID is read from
// ctx (set via tools.SessionIDContextKey), so a missing session, a
// disabled worktree service, or a lookup error all gracefully degrade to
// the request cwd or workspace root.
func (c *coordinator) workingDir(ctx context.Context) string {
	requestCwd := tools.GetWorkingDirFromContext(ctx)
	root := cmp.Or(
		requestCwd,
		c.effectiveWorkingDir,
		c.cfg.WorkingDir(),
	)
	sessionID := tools.GetSessionFromContext(ctx)

	// When worktrees are enabled, the worktree system owns per-session
	// working directories. An active (Crush-managed) worktree wins;
	// otherwise tools run in the live request cwd so that `cd`-following
	// (launching from, or moving into, a sibling worktree) keeps working.
	// The recorded session working dir only participates when the turn
	// carries NO client cwd at all: that is the swarm/API-driven case (a
	// `swarm new` worker pinned to a sibling worktree, or a queued swarm
	// send to it), where nothing else identifies which tree the session
	// belongs to. Interactive clients always carry a cwd, so letting the
	// recorded dir win over a live cwd (which froze the cwd and broke
	// worktree support) cannot recur.
	if c.worktrees != nil && c.worktrees.IsEnabled() {
		if sessionID != "" {
			if wt, err := c.worktrees.GetActive(ctx, sessionID); err == nil && wt != nil && wt.Path != "" {
				return wt.Path
			}
		}
		if requestCwd == "" {
			if dir := c.sessionWorkingDir(ctx, sessionID); dir != "" {
				return dir
			}
		}
		return root
	}

	// Non-worktree workspace: the recorded session working directory is the
	// per-session persistence, so a session resumed from a different client
	// (with a different launch cwd) still runs its tools where it began.
	if dir := c.sessionWorkingDir(ctx, sessionID); dir != "" {
		return dir
	}

	return root
}

// sessionWorkingDir returns the session's recorded working dir, or ""
// when there is no session, no session service, or nothing recorded.
func (c *coordinator) sessionWorkingDir(ctx context.Context, sessionID string) string {
	if sessionID == "" || c.sessions == nil {
		return ""
	}
	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return ""
	}
	return sess.WorkingDir
}

// stampSessionWorkingDir records the session's working directory the first
// time it runs, if it has none yet. The value is the initiating client's
// request cwd, falling back to the workspace defaults. Best-effort and
// idempotent: once a session has a working_dir it is never overwritten.
//
// This only applies to non-worktree workspaces. When worktrees are enabled
// the worktree system owns per-session directories, so recording a working
// dir here would be redundant and could later point at a removed worktree.
func (c *coordinator) stampSessionWorkingDir(ctx context.Context, sessionID string) {
	if sessionID == "" || c.sessions == nil {
		return
	}
	if c.worktrees != nil && c.worktrees.IsEnabled() {
		return
	}
	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil || sess.WorkingDir != "" {
		return
	}
	dir := cmp.Or(
		tools.GetWorkingDirFromContext(ctx),
		c.effectiveWorkingDir,
		c.cfg.WorkingDir(),
	)
	if dir == "" {
		return
	}
	if err := c.sessions.SetWorkingDir(ctx, sessionID, dir); err != nil {
		slog.Debug("Failed to record session working dir", "session_id", sessionID, "error", err)
	}
}

// Run implements Coordinator.
func (c *coordinator) Run(ctx context.Context, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return c.run(ctx, nil, sessionID, prompt, attachments...)
}

// RunAccepted implements Coordinator.
func (c *coordinator) RunAccepted(ctx context.Context, accept *AcceptedRun, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return c.run(ctx, accept, sessionID, prompt, attachments...)
}

// callTuning is the per-turn model selection and provider tuning a
// SessionAgentCall carries: the session's model (with its resolver),
// max output tokens, merged provider options, and sampling parameters.
// Shared by run and DeferPrompt so a call that enters the queue without
// passing through run (a drain-time swarm delivery, a replayed tail)
// runs with exactly the tuning a live prompt would have had.
type callTuning struct {
	model        Model
	resolveModel func(context.Context) (*Model, error)
	maxTokens    int64
	options      fantasy.ProviderOptions
	temp, topP   *float64
	topK         *int64
	freqPenalty  *float64
	presPenalty  *float64
}

func (c *coordinator) callTuning(ctx context.Context, sessionID string) (callTuning, config.ProviderConfig, error) {
	model, resolveModel := c.sessionModel(ctx, sessionID)
	maxTokens := model.CatwalkCfg.DefaultMaxTokens
	if model.ModelCfg.MaxTokens != 0 {
		maxTokens = model.ModelCfg.MaxTokens
	}
	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return callTuning{}, config.ProviderConfig{}, errModelProviderNotConfigured
	}
	options, temp, topP, topK, freqPenalty, presPenalty := mergeCallOptions(model, providerCfg)
	return callTuning{
		model: model, resolveModel: resolveModel, maxTokens: maxTokens, options: options,
		temp: temp, topP: topP, topK: topK, freqPenalty: freqPenalty, presPenalty: presPenalty,
	}, providerCfg, nil
}

// filterAttachments drops image attachments the model cannot accept.
func (t callTuning) filterAttachments(attachments []message.Attachment) []message.Attachment {
	if t.model.CatwalkCfg.SupportsImages || attachments == nil {
		return attachments
	}
	filtered := make([]message.Attachment, 0, len(attachments))
	for _, att := range attachments {
		if att.IsText() {
			filtered = append(filtered, att)
		}
	}
	return filtered
}

// apply stamps the tuning onto call.
func (t callTuning) apply(call SessionAgentCall) SessionAgentCall {
	call.ResolveModel = t.resolveModel
	call.MaxOutputTokens = t.maxTokens
	call.ProviderOptions = t.options
	call.Temperature = t.temp
	call.TopP = t.topP
	call.TopK = t.topK
	call.FrequencyPenalty = t.freqPenalty
	call.PresencePenalty = t.presPenalty
	return call
}

// run is the shared implementation behind Run and RunAccepted. When
// accept is non-nil it is threaded onto the SessionAgentCall as
// Accepted so sessionAgent.Run can consume the accept reservation under
// dispatchMu; when nil (the in-process/local path) no accept tracking
// applies.
func (c *coordinator) run(ctx context.Context, accept *AcceptedRun, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	if err := c.readiness().Wait(); err != nil {
		return nil, err
	}

	// Record the session's working directory on first run if it has none
	// yet, defaulting to the initiating client's cwd (falling back to the
	// workspace root). Once set, this is authoritative for tool cwd across
	// clients, so a session resumed elsewhere keeps running where it began.
	c.stampSessionWorkingDir(ctx, sessionID)

	// refresh models before each run
	if err := c.UpdateModels(ctx); err != nil {
		return nil, fmt.Errorf("failed to update models: %w", err)
	}

	// The turn's model: the workspace large model, unless this session
	// was spawned with a model reference (swarm `new` with `model`), in
	// which case the reference is resolved against the current config so
	// a re-pointed role takes effect immediately. The resolved selection
	// is fixed for the turn; the agent rebuilds only the provider client
	// at Run time via ResolveModel (see sessionModel).
	tuning, providerCfg, err := c.callTuning(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	attachments = tuning.filterAttachments(attachments)

	if err := c.refreshTokenIfExpired(ctx, providerCfg); err != nil {
		// NOTE(@andreynering): We don't return here because the event handling to ask the user to reauthenticate
		// depends on the flow below. If refresh fails, proceed with the token we have.
		slog.Error("Failed to refresh OAuth2 token. Proceeding with existing token.", "error", err)
	}

	// Coalesce per-attempt RunComplete payloads so only the final
	// outcome reaches subscribers. Without this, the first attempt's
	// failed RunComplete (unauthorized) would race ahead of the
	// retry's success, and `crush run` would exit on the stale error
	// before ever seeing the retry result. Each attempt's
	// SessionAgentCall.OnComplete hook overwrites latest; we publish
	// exactly once after retries resolve, via PublishMustDeliver, so
	// a momentarily-full subscriber buffer can't silently drop the
	// terminal event.
	var (
		latest    notify.RunComplete
		hasLatest bool
	)
	onComplete := func(rc notify.RunComplete) {
		latest = rc
		hasLatest = true
	}
	// Propagate the caller-supplied RunID (set via agent.WithRunID
	// at the HTTP boundary in backend.SendMessage) onto the
	// SessionAgentCall so the terminal RunComplete event echoes it
	// back. Both attempts in the retry chain reuse the same RunID;
	// the coalesce closure publishes the final outcome under that
	// same correlator.
	runID := RunIDFromContext(ctx)
	// A steer (set via agent.WithSteer at the HTTP boundary) only
	// matters for the first Run on this dispatch; goal-driven
	// continuations are ordinary turns.
	steer := SteerFromContext(ctx)
	// currentPrompt/Attachments/Accept change across goal continuations:
	// the first turn uses the caller's prompt and accept reservation;
	// each goal-driven continuation injects a fresh directive with no
	// attachments and no accept reservation (the reservation is
	// one-shot, consumed by the first Run).
	currentPrompt := prompt
	currentAttachments := attachments
	currentAccept := accept
	// Consume SwarmParts (if any) on this coordinator entry: only the
	// first user message on this dispatch carries them. Subsequent
	// goal-driven continuations run as ordinary text turns.
	currentSwarmParts := SwarmPartsFromContext(ctx)
	// Record any reply the incoming swarm message demands before the
	// turn starts, so the end-of-turn check below sees it even if the
	// agent never calls the swarm tool.
	c.registerReplyObligations(sessionID, currentSwarmParts)

	runOnce := func() (*fantasy.AgentResult, error) {
		run := func() (*fantasy.AgentResult, error) {
			call := tuning.apply(SessionAgentCall{
				SessionID:   sessionID,
				RunID:       runID,
				Steer:       steer,
				Prompt:      currentPrompt,
				SwarmParts:  currentSwarmParts,
				Attachments: currentAttachments,
				OnComplete:  onComplete,
				Accepted:    currentAccept,
			})
			return c.currentAgent.Run(ctx, call)
		}
		beforeLoaded := c.skillTracker.LoadedNames()
		var result *fantasy.AgentResult
		err := c.runWithUnauthorizedRetry(ctx, providerCfg, func() error {
			var e error
			result, e = run()
			return e
		})
		logTurnSkillUsage(sessionID, currentPrompt, c.activeSkills, c.skillTracker, beforeLoaded)

		if c.isUnauthorized(err) {
			if rerr := c.retryAfterUnauthorized(ctx, providerCfg); rerr == nil {
				result, err = run()
			}
		}
		return result, err
	}

	// Goal continuation loop. After a turn ends normally the active
	// /goal (if any) is evaluated; while the goal is unmet and within
	// its turn budget, AdvanceGoal returns a continuation directive and
	// the loop runs another turn. Sessions without a goal evaluate to a
	// cheap no-op and the loop runs exactly once. Running here (rather
	// than inside sessionAgent.Run) keeps the session continuously busy
	// and avoids touching Run's concurrency machinery.
	var (
		result      *fantasy.AgentResult
		originalErr error
	)
	for {
		result, originalErr = runOnce()
		if originalErr != nil || ctx.Err() != nil {
			// A turn that died with an error still owes its spawner an
			// answer; tell them what went wrong rather than leaving them
			// waiting on a session that will never reply.
			c.failReplyObligations(sessionID, originalErr)
			break
		}
		// A nil result means no turn actually executed here: either
		// the call was queued because the session was already busy,
		// or it was canceled on entry (see sessionAgent.Run's queued
		// and cancel-on-entry paths, which both return (nil, nil)).
		// In the queued case, the active run owns the goal
		// continuation loop for this session; advancing here would
		// spin, re-evaluating the goal and re-enqueuing on every
		// iteration while the real turn is still running. In the
		// canceled case there's nothing to continue from either way,
		// so breaking is correct in both.
		if result == nil {
			break
		}
		// Reply obligations take precedence over the goal: a worker that
		// owes its spawner an answer is nudged to send it before the
		// goal evaluator gets a say, so the parent is never left
		// waiting on a session that has already gone idle.
		if contPrompt, ok := c.advanceReplyObligations(ctx, sessionID, result); ok {
			currentPrompt = contPrompt
			currentAttachments = nil
			currentAccept = nil
			currentSwarmParts = nil
			steer = false
			continue
		}
		cont, contPrompt := c.currentAgent.AdvanceGoal(ctx, sessionID)
		if !cont {
			break
		}
		currentPrompt = contPrompt
		currentAttachments = nil
		currentAccept = nil
		currentSwarmParts = nil
		steer = false
	}

	if hasLatest && c.runComplete != nil {
		c.runComplete.PublishMustDeliver(ctx, pubsub.UpdatedEvent, latest)
		// Signal to the dispatcher (backend.runAgent) that the
		// authoritative terminal RunComplete for this run was already
		// emitted, so it does not publish a duplicate fallback for the
		// error it is about to receive.
		MarkRunCompletePublished(ctx)
	}
	return result, originalErr
}

func getProviderOptions(model Model, providerCfg config.ProviderConfig) fantasy.ProviderOptions {
	options := fantasy.ProviderOptions{}

	cfgOpts := []byte("{}")
	providerCfgOpts := []byte("{}")
	catwalkOpts := []byte("{}")

	if model.ModelCfg.ProviderOptions != nil {
		data, err := json.Marshal(model.ModelCfg.ProviderOptions)
		if err == nil {
			cfgOpts = data
		}
	}

	if providerCfg.ProviderOptions != nil {
		data, err := json.Marshal(providerCfg.ProviderOptions)
		if err == nil {
			providerCfgOpts = data
		}
	}

	if model.CatwalkCfg.Options.ProviderOptions != nil {
		data, err := json.Marshal(model.CatwalkCfg.Options.ProviderOptions)
		if err == nil {
			catwalkOpts = data
		}
	}

	readers := []io.Reader{
		bytes.NewReader(catwalkOpts),
		bytes.NewReader(providerCfgOpts),
		bytes.NewReader(cfgOpts),
	}

	got, err := jsons.Merge(readers)
	if err != nil {
		slog.Error("Could not merge call config", "err", err)
		return options
	}

	mergedOptions := make(map[string]any)

	err = json.Unmarshal([]byte(got), &mergedOptions)
	if err != nil {
		slog.Error("Could not create config for call", "err", err)
		return options
	}

	shouldSetEffort := model.CatwalkCfg.CanReason &&
		slices.Contains(model.CatwalkCfg.ReasoningLevels, model.ModelCfg.ReasoningEffort)

	switch providerCfg.Type {
	case openai.Name, azure.Name:
		applyOpenAIProviderOptions(options, mergedOptions, model, shouldSetEffort)
	case anthropic.Name, bedrock.Name:
		applyAnthropicProviderOptions(options, mergedOptions, model, providerCfg, shouldSetEffort)
	case openrouter.Name:
		applyOpenRouterProviderOptions(options, mergedOptions, model, shouldSetEffort)
	case vercel.Name:
		applyVercelProviderOptions(options, mergedOptions, model, shouldSetEffort)
	case google.Name:
		applyGoogleProviderOptions(options, mergedOptions, model)
	case openaicompat.Name, hyper.Name:
		applyOpenAICompatProviderOptions(options, mergedOptions, model, providerCfg, shouldSetEffort)
	}

	return options
}

func applyOpenAIProviderOptions(options fantasy.ProviderOptions, mergedOptions map[string]any, model Model, shouldSetEffort bool) {
	_, hasReasoningEffort := mergedOptions["reasoning_effort"]
	if !hasReasoningEffort && shouldSetEffort {
		mergedOptions["reasoning_effort"] = model.ModelCfg.ReasoningEffort
	}
	modelIDForResponses := model.CatwalkCfg.ID
	if !openai.IsResponsesModel(modelIDForResponses) {
		modelIDForResponses = strings.TrimPrefix(modelIDForResponses, "openai.")
	}
	if openai.IsResponsesModel(modelIDForResponses) {
		if openai.IsResponsesReasoningModel(modelIDForResponses) {
			mergedOptions["reasoning_summary"] = "auto"
			mergedOptions["include"] = []openai.IncludeType{openai.IncludeReasoningEncryptedContent}
		}
		parsed, err := openai.ParseResponsesOptions(mergedOptions)
		if err == nil {
			options[openai.Name] = parsed
		}
	} else {
		parsed, err := openai.ParseOptions(mergedOptions)
		if err == nil {
			options[openai.Name] = parsed
		}
	}
}

func applyAnthropicProviderOptions(options fantasy.ProviderOptions, mergedOptions map[string]any, model Model, providerCfg config.ProviderConfig, shouldSetEffort bool) {
	var (
		_, hasEffort = mergedOptions["effort"]
		_, hasThink  = mergedOptions["thinking"]
		extraBody    = make(map[string]any)
	)

	switch providerCfg.ID {
	case string(catwalk.InferenceProviderAlibabaSingapore):
		switch {
		case !hasEffort && shouldSetEffort:
			extraBody["reasoning_effort"] = model.ModelCfg.ReasoningEffort
		case !hasThink && model.CatwalkCfg.CanReason:
			if model.ModelCfg.Think {
				extraBody["thinking"] = map[string]any{"type": "enabled"}
			} else {
				extraBody["thinking"] = map[string]any{"type": "disabled"}
			}
		}
		mergedOptions["extra_body"] = extraBody

	default:
		switch {
		case !hasEffort && shouldSetEffort:
			mergedOptions["effort"] = model.ModelCfg.ReasoningEffort
		case !hasThink && model.ModelCfg.Think:
			mergedOptions["thinking"] = map[string]any{"budget_tokens": 2000}
		}
	}

	parsed, err := anthropic.ParseOptions(mergedOptions)
	if err == nil {
		options[anthropic.Name] = parsed
	}
}

func applyOpenRouterProviderOptions(options fantasy.ProviderOptions, mergedOptions map[string]any, model Model, shouldSetEffort bool) {
	_, hasReasoning := mergedOptions["reasoning"]
	if !hasReasoning && shouldSetEffort {
		mergedOptions["reasoning"] = map[string]any{
			"enabled": true,
			"effort":  model.ModelCfg.ReasoningEffort,
		}
	}
	parsed, err := openrouter.ParseOptions(mergedOptions)
	if err == nil {
		options[openrouter.Name] = parsed
	}
}

func applyVercelProviderOptions(options fantasy.ProviderOptions, mergedOptions map[string]any, model Model, shouldSetEffort bool) {
	_, hasReasoning := mergedOptions["reasoning"]
	if !hasReasoning && shouldSetEffort {
		mergedOptions["reasoning"] = map[string]any{
			"enabled": true,
			"effort":  model.ModelCfg.ReasoningEffort,
		}
	}
	parsed, err := vercel.ParseOptions(mergedOptions)
	if err == nil {
		options[vercel.Name] = parsed
	}
}

func applyGoogleProviderOptions(options fantasy.ProviderOptions, mergedOptions map[string]any, model Model) {
	_, hasReasoning := mergedOptions["thinking_config"]
	if !hasReasoning {
		if strings.HasPrefix(model.CatwalkCfg.ID, "gemini-2") {
			mergedOptions["thinking_config"] = map[string]any{
				"thinking_budget":  2000,
				"include_thoughts": true,
			}
		} else {
			mergedOptions["thinking_config"] = map[string]any{
				"thinking_level":   model.ModelCfg.ReasoningEffort,
				"include_thoughts": true,
			}
		}
	}
	parsed, err := google.ParseOptions(mergedOptions)
	if err == nil {
		options[google.Name] = parsed
	}
}

func applyOpenAICompatProviderOptions(options fantasy.ProviderOptions, mergedOptions map[string]any, model Model, providerCfg config.ProviderConfig, shouldSetEffort bool) {
	extraBody := make(map[string]any)

	_, hasReasoningEffort := mergedOptions["reasoning_effort"]
	if !hasReasoningEffort && shouldSetEffort {
		switch providerCfg.ID {
		case string(catwalk.InferenceProviderIoNet):
			extraBody["reasoning"] = map[string]string{"effort": model.ModelCfg.ReasoningEffort}
		default:
			mergedOptions["reasoning_effort"] = model.ModelCfg.ReasoningEffort
		}
	}

	// "reasoning effort" is a standard OpenAI field, but "thinking" is not.
	// Setting it in the right way for each provider.
	// TODO: Abstract this in Fantasy somehow?
	// TODO: Allow custom providers to specify how to set this?
	switch providerCfg.ID {
	case hyper.Name:
		extraBody["thinking"] = model.ModelCfg.Think
	case string(catwalk.InferenceProviderIoNet):
		if _, ok := extraBody["reasoning"]; !ok && model.CatwalkCfg.CanReason {
			if model.ModelCfg.Think {
				extraBody["reasoning"] = map[string]string{"effort": "medium"}
			} else {
				extraBody["reasoning"] = map[string]string{"effort": "none"}
			}
		}
	case string(catwalk.InferenceProviderZAI), string(catwalk.InferenceProviderDeepSeek):
		if model.ModelCfg.Think || model.ModelCfg.ReasoningEffort != "" {
			extraBody["thinking"] = map[string]any{
				"type": "enabled",
			}
		} else {
			extraBody["thinking"] = map[string]any{
				"type": "disabled",
			}
		}
	case string(catwalk.InferenceProviderAlibabaSingapore):
		if model.CatwalkCfg.CanReason {
			extraBody["enable_thinking"] = model.ModelCfg.Think
		}
	}

	mergedOptions["extra_body"] = extraBody

	parsed, err := openaicompat.ParseOptions(mergedOptions)
	if err == nil {
		options[openaicompat.Name] = parsed
	}
}

func mergeCallOptions(model Model, cfg config.ProviderConfig) (fantasy.ProviderOptions, *float64, *float64, *int64, *float64, *float64) {
	modelOptions := getProviderOptions(model, cfg)
	temp := cmp.Or(model.ModelCfg.Temperature, model.CatwalkCfg.Options.Temperature)
	topP := cmp.Or(model.ModelCfg.TopP, model.CatwalkCfg.Options.TopP)
	topK := cmp.Or(model.ModelCfg.TopK, model.CatwalkCfg.Options.TopK)
	freqPenalty := cmp.Or(model.ModelCfg.FrequencyPenalty, model.CatwalkCfg.Options.FrequencyPenalty)
	presPenalty := cmp.Or(model.ModelCfg.PresencePenalty, model.CatwalkCfg.Options.PresencePenalty)
	return modelOptions, temp, topP, topK, freqPenalty, presPenalty
}

func (c *coordinator) buildAgent(ctx context.Context, prompt *prompt.Prompt, agent config.Agent, isSubAgent bool) (SessionAgent, error) {
	large, small, err := c.buildAgentModels(ctx, isSubAgent)
	if err != nil {
		return nil, err
	}

	largeProviderCfg, _ := c.cfg.Config().Providers.Get(large.ModelCfg.Provider)
	// Only the top-level coder agent's queue is worth persisting:
	// sub-agent sessions are hidden children of a tool call that cannot
	// outlive the process.
	var queueJournal QueueJournal
	if !isSubAgent && c.journal != nil {
		queueJournal = c.journal
	}
	result := NewSessionAgent(SessionAgentOptions{
		LargeModel:           large,
		SmallModel:           small,
		SystemPromptPrefix:   largeProviderCfg.SystemPromptPrefix,
		SystemPrompt:         "",
		IsSubAgent:           isSubAgent,
		DisableAutoSummarize: c.cfg.Config().Options.DisableAutoSummarize,
		IsYolo:               c.permissions.SkipRequests(),
		Sessions:             c.sessions,
		Messages:             c.messages,
		Checkpoints:          c.checkpoints,
		Milestones:           c.milestones,
		Tools:                nil,
		Notify:               c.notify,
		RunComplete:          c.runComplete,
		WorkingDir:           c.workingDir,
		QueueJournal:         queueJournal,
		OnQueueDrop:          c.dropReplyObligations,
		OnQueueDispatch:      c.deliverReplyObligations,
	})

	// The readiness goroutines below run asynchronously and outlive the
	// caller of buildAgent. In the daemon, buildAgent is reached via an
	// InitAgent HTTP handler whose request context is cancelled the moment
	// the handler returns. Because readyWg is an errgroup, a cancellation
	// here would be cached and returned by every future run, permanently
	// wedging the coordinator (messages send but never produce output).
	// Detach from the request lifetime so only real build failures poison
	// readiness.
	readyCtx := context.WithoutCancel(ctx)

	ready := c.readiness()
	ready.Go(func() error {
		systemPrompt, err := prompt.Build(readyCtx, large.Model.Provider(), large.Model.Model(), c.cfg)
		if err != nil {
			return err
		}
		result.SetSystemPrompt(systemPrompt)
		return nil
	})

	ready.Go(func() error {
		// Wait for MCP servers to finish registering their tools before
		// building the initial tool list. This ensures the tool set includes
		// all MCP tools, not just fast-to-init ones — slow stdio servers
		// (e.g. Python via uv) otherwise register too late to appear.
		if err := mcp.WaitForInit(readyCtx); err != nil {
			return err
		}
		tools, err := c.buildTools(readyCtx, agent, isSubAgent)
		if err != nil {
			return err
		}
		result.SetTools(tools)
		return nil
	})

	return result, nil
}

func (c *coordinator) buildTools(ctx context.Context, agent config.Agent, isSubAgent bool) ([]fantasy.AgentTool, error) {
	var allTools []fantasy.AgentTool
	if slices.Contains(agent.AllowedTools, AgentToolName) {
		agentTool, err := c.agentTool(ctx)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, agentTool)
	}

	if slices.Contains(agent.AllowedTools, tools.AgenticFetchToolName) {
		agenticFetchTool, err := c.agenticFetchTool(ctx, nil)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, agenticFetchTool)
	}

	if slices.Contains(agent.AllowedTools, ReviewToolName) {
		reviewTool, err := c.reviewTool(ctx)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, reviewTool)
	}

	// Get the model name for the agent
	modelName := ""
	if modelCfg, ok := c.cfg.Config().Models[agent.Model]; ok {
		if model := c.cfg.Config().GetModel(modelCfg.Provider, modelCfg.Model); model != nil {
			modelName = model.Name
		}
	}

	logFile := filepath.Join(c.cfg.Config().Options.DataDirectory, "logs", "crush.log")

	// Snapshot the swarm wiring once: the same backend handle powers
	// both the swarm tool and cross-workspace history search.
	c.swarmMu.Lock()
	swarmBackend := c.swarmBackend
	swarmWorkspaceID := c.swarmWorkspaceID
	swarmConfigFn := c.swarmConfig
	c.swarmMu.Unlock()

	// Cross-workspace search is offered to main agents only (never
	// task/reviewer sub-agents) and only when the backend is wired.
	var historySearcher tools.HistorySearcher
	if !isSubAgent && swarmBackend != nil {
		if hs, ok := swarmBackend.(tools.HistorySearcher); ok {
			historySearcher = hs
		}
	}

	// Build hook runner if PreToolUse hooks are configured.
	var hookRunner *hooks.Runner
	if preToolHooks := c.cfg.Config().Hooks[hooks.EventPreToolUse]; len(preToolHooks) > 0 {
		hookRunner = hooks.NewRunner(preToolHooks, c.cfg.WorkingDir(), c.cfg.WorkingDir())
	}

	allTools = append(
		allTools,
		tools.NewBashTool(c.permissions, c.workingDir, c.cfg.Config().Options.Attribution, modelName),
		tools.NewCrushInfoTool(c.cfg, c.lspManager, c.allSkills, c.activeSkills, c.skillTracker),
		tools.NewReloadConfigTool(c.cfg, c.permissions),
		tools.NewCrushLogsTool(logFile),
		tools.NewJobOutputTool(),
		tools.NewJobKillTool(),
		tools.NewDownloadTool(c.permissions, c.workingDir, nil),
		tools.NewEditTool(c.lspManager, c.permissions, c.history, c.filetracker, c.workingDir),
		tools.NewMultiEditTool(c.lspManager, c.permissions, c.history, c.filetracker, c.workingDir),
		tools.NewFetchTool(c.permissions, c.workingDir, nil),
		tools.NewGlobTool(c.workingDir),
		tools.NewGrepTool(c.workingDir, c.cfg.Config().Tools.Grep),
		tools.NewLsTool(c.permissions, c.workingDir, c.cfg.Config().Tools.Ls),
		tools.NewSourcegraphTool(nil),
		tools.NewContext7Tool(nil),
		tools.NewSearchHistoryTool(c.messages, c.sessions, c.embeddings, historySearcher, swarmWorkspaceID),
		tools.NewListSessionsTool(c.sessions),
		tools.NewTodosTool(c.sessions),
	)

	viewTool := tools.NewViewTool(c.lspManager, c.permissions, c.filetracker, c.skillTracker, c.workingDir, c.cfg.Config().Options.SkillsPaths...)
	allTools = append(
		allTools,
		viewTool,
		tools.NewMultiViewTool(viewTool),
		tools.NewWriteTool(c.lspManager, c.permissions, c.history, c.filetracker, c.workingDir),
	)

	// Swarm tool: only offered to main agents (not task/reviewer
	// sub-agents), and only when the backend has plumbed the
	// cross-workspace swarm dispatcher and the global config gate is
	// on. Sub-agents get workflow-scoped sessions that must not be
	// addressable, and the config gate makes swarm entirely absent
	// when disabled so its guidance never bleeds into the coder
	// prompt.
	if !isSubAgent && swarmBackend != nil {
		if swarmConfigFn == nil {
			swarmConfigFn = swarm.Default
		}
		allTools = append(allTools, tools.NewSwarmTool(
			swarmBackend, c.sessions, swarmConfigFn, swarmWorkspaceID, c.swarmReplies,
		))
		allTools = append(allTools, tools.NewWorkspaceLookupTool(swarmBackend))
		allTools = append(allTools, tools.NewRenameSessionTool(swarmBackend))
	}

	// Editor bridge tools. The bridge is resolved per-turn from the
	// request context (the originating client's editor), so these are
	// gated on per-turn availability rather than registered statically:
	// the agent drops them for turns whose initiating client has no
	// attached editor, while still exposing them to editor clients on
	// the same shared coordinator.
	allTools = append(
		allTools,
		tools.WithContextGate(tools.NewEditorContextTool(), tools.EditorAttached),
		tools.WithContextGate(tools.NewShowLocationsTool(), tools.EditorAttached),
	)

	// Question tool: last-resort escalation to the user for genuinely
	// blocking, ambiguous, high-stakes decisions (see question.md).
	// Only offered to main agents — sub-agents (task/reviewer) get
	// workflow-scoped sessions and must not interrupt the user — and
	// only when an interactive client can actually answer (mirrors the
	// permission service's skip-requests signal; see
	// tools.QuestionCapable). NewQuestionTool re-checks the same
	// condition at call time as a defense-in-depth hard-fail.
	if !isSubAgent {
		allTools = append(
			allTools,
			tools.WithContextGate(tools.NewQuestionTool(c.permissions, c.questions), tools.QuestionCapable(c.permissions)),
		)
	}

	// Add LSP tools if user has configured LSPs or auto_lsp is enabled (nil or true).
	if len(c.cfg.Config().LSP) > 0 || c.cfg.Config().Options.AutoLSP == nil || *c.cfg.Config().Options.AutoLSP {
		allTools = append(
			allTools,
			tools.NewDiagnosticsTool(c.lspManager),
			tools.NewReferencesTool(c.lspManager),
			tools.NewDefinitionTool(c.lspManager),
			tools.NewDocumentSymbolsTool(c.lspManager, c.workingDir),
			tools.NewRenameTool(c.lspManager, c.permissions, c.workingDir),
			tools.NewReplaceSymbolTool(c.lspManager, c.permissions, c.history, c.filetracker, c.workingDir),
			tools.NewLSPRestartTool(c.lspManager),
		)
	}

	if len(c.cfg.Config().MCP) > 0 {
		allTools = append(
			allTools,
			tools.NewListMCPResourcesTool(c.cfg, c.permissions),
			tools.NewReadMCPResourceTool(c.cfg, c.permissions),
		)
	}

	var filteredTools []fantasy.AgentTool
	for _, tool := range allTools {
		if slices.Contains(agent.AllowedTools, tool.Info().Name) {
			filteredTools = append(filteredTools, tool)
		}
	}

	for _, tool := range tools.GetMCPTools(c.permissions, c.cfg, c.workingDir) {
		if agent.AllowedMCP == nil {
			// No MCP restrictions
			filteredTools = append(filteredTools, tool)
			continue
		}
		if len(agent.AllowedMCP) == 0 {
			// No MCPs allowed
			slog.Debug("No MCPs allowed", "tool", tool.Name(), "agent", agent.Name)
			break
		}

		for mcp, tools := range agent.AllowedMCP {
			if mcp != tool.MCP() {
				continue
			}
			if len(tools) == 0 || slices.Contains(tools, tool.MCPToolName()) {
				filteredTools = append(filteredTools, tool)
				break
			}
			slog.Debug("MCP not allowed", "tool", tool.Name(), "agent", agent.Name)
		}
	}
	slices.SortFunc(filteredTools, func(a, b fantasy.AgentTool) int {
		return strings.Compare(a.Info().Name, b.Info().Name)
	})

	// Wrap tools with hook interception for the top-level agent only.
	// Sub-agents (the `agent` task tool, `agentic_fetch`, etc.) run
	// without hook interception to avoid firing the user's hook N times
	// per delegated turn. The top-level invocation of the sub-agent tool
	// itself is still wrapped from the coder's side.
	filteredTools = wrapToolsWithHooks(filteredTools, hookRunner, c.cfg, isSubAgent)

	return filteredTools, nil
}

// resolveModelRef adapts the config resolver for tools: it reads the
// live config on every call so a reload that adds a role or provider is
// honored without rebuilding tools.
func (c *coordinator) resolveModelRef(ref string) (config.SelectedModel, error) {
	return c.cfg.Config().ResolveModelRef(ref)
}

// optionalModelRef resolves an optional tool `model` parameter. Empty
// means "no override" (nil, nil).
func (c *coordinator) optionalModelRef(ref string) (*config.SelectedModel, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, nil
	}
	sel, err := c.resolveModelRef(ref)
	if err != nil {
		return nil, err
	}
	return &sel, nil
}

// delegationModel returns the selection a sub-agent runs on: the explicit
// per-call override when given, else (when useWorker) the configured
// `worker` role when it resolves, else nil (the agent's own model).
func (c *coordinator) delegationModel(override *config.SelectedModel, useWorker bool) *config.SelectedModel {
	if override != nil {
		return override
	}
	if !useWorker {
		return nil
	}
	if sel, ok := c.cfg.Config().WorkerModel(); ok {
		return &sel
	}
	return nil
}

// overrideCacheKey identifies a built override [Model]. It covers every
// input that changes how the provider or language model is constructed:
// provider, model id, the sub-agent client variant, and whether the
// selection requests anthropic interleaved-thinking (which buildProvider
// bakes into a request header from either Think or ProviderOptions).
// Per-call tuning (effort, temperature) is overlaid on the cached value.
func (c *coordinator) overrideCacheKey(sel config.SelectedModel, isSubAgent bool) string {
	return fmt.Sprintf("%s\x00%s\x00%t\x00%t", sel.Provider, sel.Model, c.isAnthropicThinking(sel), isSubAgent)
}

// buildModel constructs a runnable [Model] for an explicit selection made
// by a tool's `model` parameter or the `worker` role. The selection's
// provider must be enabled and must list the model. The catalog default
// max tokens are backfilled when unset, exactly as applyModelOverrides
// does for large/small at load time, so a role behaves like the same
// selection would in the large slot. The workspace large/small models do
// NOT go through here (see buildAgentModels).
func (c *coordinator) buildModel(ctx context.Context, sel config.SelectedModel, isSubAgent bool) (Model, error) {
	if sel.Provider == "" || sel.Model == "" {
		return Model{}, fmt.Errorf("model selection requires both provider and model")
	}
	cfg := c.cfg.Config()
	providerCfg, ok := cfg.Providers.Get(sel.Provider)
	if !ok || providerCfg.Disable {
		return Model{}, fmt.Errorf("%w: %q", errModelProviderNotConfigured, sel.Provider)
	}
	catwalkModel := cfg.GetModel(sel.Provider, sel.Model)
	if catwalkModel == nil {
		return Model{}, fmt.Errorf("provider %q has no model %q", sel.Provider, sel.Model)
	}

	key := c.overrideCacheKey(sel, isSubAgent)
	built, cached := c.overrideCache.Get(key)
	if !cached {
		provider, err := c.buildProvider(providerCfg, sel, isSubAgent)
		if err != nil {
			return Model{}, err
		}
		modelID := sel.Model
		if sel.Provider == openrouter.Name && isExactoSupported(modelID) {
			modelID += ":exacto"
		}
		lm, err := provider.LanguageModel(ctx, modelID)
		if err != nil {
			return Model{}, err
		}
		built = Model{
			Model:      lm,
			CatwalkCfg: *catwalkModel,
			FlatRate:   providerCfg.FlatRate,
		}
		c.overrideCache.Set(key, built)
	}

	built.ModelCfg = sel
	if built.ModelCfg.MaxTokens == 0 {
		built.ModelCfg.MaxTokens = catwalkModel.DefaultMaxTokens
	}
	return built, nil
}

// overrideResolver returns a SessionAgentCall.ResolveModel that rebuilds
// sel through the override cache on every call. The selection is fixed, so
// the model a turn runs on — and the tuning derived from it — cannot
// drift; only the provider client is refreshed after a credential refresh
// or unauthorized retry resets the cache. A build failure is an error for
// the turn: silently running the large model with the override's tuning
// would misattribute and mis-size the request.
func (c *coordinator) overrideResolver(sel config.SelectedModel, isSubAgent bool) func(context.Context) (*Model, error) {
	return func(ctx context.Context) (*Model, error) {
		m, err := c.buildModel(ctx, sel, isSubAgent)
		if err != nil {
			return nil, fmt.Errorf("resolve model %s/%s: %w", sel.Provider, sel.Model, err)
		}
		return &m, nil
	}
}

// sessionModel resolves the model a top-level turn on sessionID runs on.
// Sessions without a ModelRef (every session a person opens, and every
// swarm worker spawned without `model`) run the agent's large model with a
// nil resolver, exactly as before. A session with a ModelRef resolves it
// through config.ResolveModelRef and buildModel; if the reference no
// longer resolves (a role was removed, a provider disabled) the session
// falls back to large with a warning rather than becoming unusable, since
// nothing re-points an existing session's reference.
func (c *coordinator) sessionModel(ctx context.Context, sessionID string) (Model, func(context.Context) (*Model, error)) {
	large := c.currentAgent.Model()
	if c.sessions == nil || sessionID == "" {
		return large, nil
	}
	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil || sess.ModelRef == "" {
		return large, nil
	}
	sel, err := c.resolveModelRef(sess.ModelRef)
	if err == nil {
		var m Model
		m, err = c.buildModel(ctx, sel, false)
		if err == nil {
			return m, c.overrideResolver(sel, false)
		}
	}
	slog.Warn("Session model reference is unavailable; using large model",
		"session_id", sessionID, "model_ref", sess.ModelRef, "error", err)
	return large, nil
}

// TODO: when we support multiple agents we need to change this so that we pass in the agent specific model config
func (c *coordinator) buildAgentModels(ctx context.Context, isSubAgent bool) (Model, Model, error) {
	largeModelCfg, ok := c.cfg.Config().Models[config.SelectedModelTypeLarge]
	if !ok {
		return Model{}, Model{}, errLargeModelNotSelected
	}
	smallModelCfg, ok := c.cfg.Config().Models[config.SelectedModelTypeSmall]
	if !ok {
		return Model{}, Model{}, errSmallModelNotSelected
	}

	largeProviderCfg, ok := c.cfg.Config().Providers.Get(largeModelCfg.Provider)
	if !ok {
		return Model{}, Model{}, errLargeModelProviderNotConfigured
	}

	largeProvider, err := c.buildProvider(largeProviderCfg, largeModelCfg, isSubAgent)
	if err != nil {
		return Model{}, Model{}, err
	}

	smallProviderCfg, ok := c.cfg.Config().Providers.Get(smallModelCfg.Provider)
	if !ok {
		return Model{}, Model{}, errSmallModelProviderNotConfigured
	}

	smallProvider, err := c.buildProvider(smallProviderCfg, smallModelCfg, true)
	if err != nil {
		return Model{}, Model{}, err
	}

	var largeCatwalkModel *catwalk.Model
	var smallCatwalkModel *catwalk.Model

	for _, m := range largeProviderCfg.Models {
		if m.ID == largeModelCfg.Model {
			largeCatwalkModel = &m
		}
	}
	for _, m := range smallProviderCfg.Models {
		if m.ID == smallModelCfg.Model {
			smallCatwalkModel = &m
		}
	}

	if largeCatwalkModel == nil {
		return Model{}, Model{}, errLargeModelNotFound
	}

	if smallCatwalkModel == nil {
		return Model{}, Model{}, errSmallModelNotFound
	}

	largeModelID := largeModelCfg.Model
	smallModelID := smallModelCfg.Model

	if largeModelCfg.Provider == openrouter.Name && isExactoSupported(largeModelID) {
		largeModelID += ":exacto"
	}

	if smallModelCfg.Provider == openrouter.Name && isExactoSupported(smallModelID) {
		smallModelID += ":exacto"
	}

	largeModel, err := largeProvider.LanguageModel(ctx, largeModelID)
	if err != nil {
		return Model{}, Model{}, err
	}
	smallModel, err := smallProvider.LanguageModel(ctx, smallModelID)
	if err != nil {
		return Model{}, Model{}, err
	}

	large := Model{
		Model:      largeModel,
		CatwalkCfg: *largeCatwalkModel,
		ModelCfg:   largeModelCfg,
		FlatRate:   largeProviderCfg.FlatRate,
	}
	small := Model{
		Model:      smallModel,
		CatwalkCfg: *smallCatwalkModel,
		ModelCfg:   smallModelCfg,
		FlatRate:   smallProviderCfg.FlatRate,
	}
	return large, small, nil
}

func (c *coordinator) buildAnthropicProvider(baseURL, apiKey string, headers map[string]string, providerID string) (fantasy.Provider, error) {
	var opts []anthropic.Option

	switch {
	case strings.HasPrefix(apiKey, "Bearer "):
		// NOTE: Prevent the SDK from picking up the API key from env.
		os.Setenv("ANTHROPIC_API_KEY", "")
		headers["Authorization"] = apiKey
	case providerID == string(catwalk.InferenceProviderMiniMax) || providerID == string(catwalk.InferenceProviderMiniMaxChina):
		// NOTE: Prevent the SDK from picking up the API key from env.
		os.Setenv("ANTHROPIC_API_KEY", "")
		headers["Authorization"] = "Bearer " + apiKey
	case apiKey != "":
		// X-Api-Key header
		opts = append(opts, anthropic.WithAPIKey(apiKey))
	}

	if len(headers) > 0 {
		opts = append(opts, anthropic.WithHeaders(headers))
	}

	if baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}

	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, anthropic.WithHTTPClient(httpClient))
	}
	return anthropic.New(opts...)
}

func (c *coordinator) buildOpenaiProvider(baseURL, apiKey string, headers map[string]string, providerID string) (fantasy.Provider, error) {
	return openai.New(openaiProviderOptions(baseURL, apiKey, headers, providerID, c.cfg.Config().Options.Debug)...)
}

// openaiProviderOptions assembles the fantasy OpenAI provider options Crush
// uses for a "openai"-type provider. It is a free function (not a method) so
// tests can build the exact same provider the coordinator does. For Bedrock
// Mantle it installs the HTTP-200 error-envelope transport.
func openaiProviderOptions(baseURL, apiKey string, headers map[string]string, providerID string, debug bool) []openai.Option {
	isMantle := providerID == string(catwalk.InferenceProviderBedrockMantle)
	opts := []openai.Option{
		openai.WithAPIKey(apiKey),
		openai.WithUseResponsesAPI(),
		openai.WithResponsesAPIFunc(func(modelID string) bool {
			// Bedrock Mantle's OpenAI surface is Responses-only (its
			// gateway does not proxy /chat/completions), and its model ids
			// (e.g. us.openai.gpt-5.6-sol) are not recognized by
			// IsResponsesModel, so force the Responses API for it.
			if isMantle {
				return true
			}
			return openai.IsResponsesModel(modelID) ||
				openai.IsResponsesModel(strings.TrimPrefix(modelID, "openai."))
		}),
	}
	var httpClient *http.Client
	if debug {
		httpClient = log.NewHTTPClient()
	}
	// Bedrock Mantle serves the OpenAI surface but returns OpenAI-style error
	// envelopes with HTTP 200, which the SDK would otherwise parse as an empty
	// (successful) response. Its catalog type is "openai", so it is built here
	// (not in buildOpenaiCompatProvider); wrap the transport so those errors
	// surface with a real status.
	if isMantle {
		httpClient = withMantleErrorTransport(httpClient)
	}
	if httpClient != nil {
		opts = append(opts, openai.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, openai.WithHeaders(headers))
	}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return opts
}

func (c *coordinator) buildOpenrouterProvider(_, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []openrouter.Option{
		openrouter.WithAPIKey(apiKey),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, openrouter.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, openrouter.WithHeaders(headers))
	}
	return openrouter.New(opts...)
}

func (c *coordinator) buildVercelProvider(_, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []vercel.Option{
		vercel.WithAPIKey(apiKey),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, vercel.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, vercel.WithHeaders(headers))
	}
	return vercel.New(opts...)
}

func (c *coordinator) buildOpenaiCompatProvider(baseURL, apiKey string, headers map[string]string, extraBody map[string]any, providerID string, isSubAgent bool) (fantasy.Provider, error) {
	opts := []openaicompat.Option{
		openaicompat.WithBaseURL(baseURL),
		openaicompat.WithAPIKey(apiKey),
	}

	// Set HTTP client based on provider and debug mode.
	var httpClient *http.Client
	switch providerID {
	case string(catwalk.InferenceProviderCopilot):
		opts = append(
			opts,
			openaicompat.WithUseResponsesAPI(),
			openaicompat.WithResponsesAPIFunc(func(modelID string) bool {
				return copilotResponsesModels[modelID]
			}),
		)
		httpClient = copilot.NewClient(isSubAgent, c.cfg.Config().Options.Debug)
	}
	if httpClient == nil && c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	// Bedrock Mantle returns OpenAI-style error envelopes with HTTP 200,
	// which the SDK would otherwise parse as an empty (successful) response.
	// Wrap the transport so those errors are surfaced with a real status.
	// The default mantle provider has catalog type "openai" and is built by
	// buildOpenaiProvider (which also forces the Responses API); this branch
	// only fires if a user overrides mantle's type to "openai-compat", and
	// installs the transport as a defensive measure — it does not force the
	// Responses API here, so that override is not a fully supported path.
	if providerID == string(catwalk.InferenceProviderBedrockMantle) {
		httpClient = withMantleErrorTransport(httpClient)
	}
	if httpClient != nil {
		opts = append(opts, openaicompat.WithHTTPClient(httpClient))
	}

	if len(headers) > 0 {
		opts = append(opts, openaicompat.WithHeaders(headers))
	}

	for extraKey, extraValue := range extraBody {
		opts = append(opts, openaicompat.WithSDKOptions(openaisdk.WithJSONSet(extraKey, extraValue)))
	}

	return openaicompat.New(opts...)
}

func (c *coordinator) buildAzureProvider(baseURL, apiKey string, headers map[string]string, options map[string]string) (fantasy.Provider, error) {
	opts := []azure.Option{
		azure.WithBaseURL(baseURL),
		azure.WithAPIKey(apiKey),
		azure.WithUseResponsesAPI(),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, azure.WithHTTPClient(httpClient))
	}
	if options == nil {
		options = make(map[string]string)
	}
	if apiVersion, ok := options["apiVersion"]; ok {
		opts = append(opts, azure.WithAPIVersion(apiVersion))
	}
	if len(headers) > 0 {
		opts = append(opts, azure.WithHeaders(headers))
	}

	return azure.New(opts...)
}

func (c *coordinator) buildBedrockProvider(baseURL, apiKey string, headers map[string]string, providerID string) (fantasy.Provider, error) {
	var opts []bedrock.Option
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, bedrock.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, bedrock.WithHeaders(headers))
	}

	// Allow overriding the Bedrock endpoint via config base URL or the
	// AWS_ENDPOINT_URL_BEDROCK environment variable.
	if baseURL == "" {
		baseURL = os.Getenv("AWS_ENDPOINT_URL_BEDROCK")
	}
	if baseURL != "" {
		opts = append(opts, bedrock.WithBaseURL(baseURL))
	}

	switch {
	case apiKey != "":
		opts = append(opts, bedrock.WithAPIKey(apiKey))
	case os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "":
		opts = append(opts, bedrock.WithAPIKey(os.Getenv("AWS_BEARER_TOKEN_BEDROCK")))
	default:
		// Skip, let the SDK do authentication.
	}

	_ = providerID

	return bedrock.New(opts...)
}

func (c *coordinator) buildGoogleProvider(baseURL, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []google.Option{
		google.WithBaseURL(baseURL),
		google.WithGeminiAPIKey(apiKey),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, google.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, google.WithHeaders(headers))
	}
	return google.New(opts...)
}

func (c *coordinator) buildGoogleVertexProvider(headers map[string]string, options map[string]string) (fantasy.Provider, error) {
	opts := []google.Option{}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, google.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, google.WithHeaders(headers))
	}

	project := options["project"]
	location := options["location"]

	opts = append(opts, google.WithVertex(project, location))

	return google.New(opts...)
}

func (c *coordinator) isAnthropicThinking(model config.SelectedModel) bool {
	if model.Think {
		return true
	}
	opts, err := anthropic.ParseOptions(model.ProviderOptions)
	return err == nil && opts.Thinking != nil
}

func (c *coordinator) buildProvider(providerCfg config.ProviderConfig, model config.SelectedModel, isSubAgent bool) (fantasy.Provider, error) {
	headers := maps.Clone(providerCfg.ExtraHeaders)
	if headers == nil {
		headers = make(map[string]string)
	}

	// handle special headers for anthropic
	if providerCfg.Type == anthropic.Name && c.isAnthropicThinking(model) {
		if v, ok := headers["anthropic-beta"]; ok {
			headers["anthropic-beta"] = v + ",interleaved-thinking-2025-05-14"
		} else {
			headers["anthropic-beta"] = "interleaved-thinking-2025-05-14"
		}
	}

	apiKey, _ := c.cfg.Resolve(providerCfg.APIKey)
	baseURL, _ := c.cfg.Resolve(providerCfg.BaseURL)

	switch providerCfg.ID {
	case string(catwalk.InferenceProviderOpenCodeGo), string(catwalk.InferenceProviderOpenCodeZen):
		if opencodeMessagesModels[model.Model] {
			baseURL = strings.TrimSuffix(baseURL, "/v1")
			return c.buildAnthropicProvider(baseURL, apiKey, headers, providerCfg.ID)
		}
	}

	switch providerCfg.Type {
	case openai.Name:
		return c.buildOpenaiProvider(baseURL, apiKey, headers, providerCfg.ID)
	case anthropic.Name:
		return c.buildAnthropicProvider(baseURL, apiKey, headers, providerCfg.ID)
	case openrouter.Name:
		return c.buildOpenrouterProvider(baseURL, apiKey, headers)
	case vercel.Name:
		return c.buildVercelProvider(baseURL, apiKey, headers)
	case azure.Name:
		return c.buildAzureProvider(baseURL, apiKey, headers, providerCfg.ExtraParams)
	case bedrock.Name:
		return c.buildBedrockProvider(baseURL, apiKey, headers, providerCfg.ID)
	case google.Name:
		return c.buildGoogleProvider(baseURL, apiKey, headers)
	case "google-vertex":
		return c.buildGoogleVertexProvider(headers, providerCfg.ExtraParams)
	case openaicompat.Name, hyper.Name:
		switch providerCfg.ID {
		case hyper.Name:
			baseURL = hyper.BaseURL() + "/v1"
		case string(catwalk.InferenceProviderZAI):
			if providerCfg.ExtraBody == nil {
				providerCfg.ExtraBody = map[string]any{}
			}
			providerCfg.ExtraBody["tool_stream"] = struct{}{}
		}
		return c.buildOpenaiCompatProvider(baseURL, apiKey, headers, providerCfg.ExtraBody, providerCfg.ID, isSubAgent)
	default:
		return nil, fmt.Errorf("provider type not supported: %q", providerCfg.Type)
	}
}

func isExactoSupported(modelID string) bool {
	supportedModels := []string{
		"moonshotai/kimi-k2-0905",
		"deepseek/deepseek-v3.1-terminus",
		"z-ai/glm-4.6",
		"openai/gpt-oss-120b",
		"qwen/qwen3-coder",
	}
	return slices.Contains(supportedModels, modelID)
}

// BeginAccepted reserves an accept slot for sessionID on the active
// agent and returns the ownership handle. It is the fire-and-forget
// dispatch path's only way to mark a run as accepted-but-not-yet-active
// so a cancel arriving before the run registers in activeRequests is not
// lost.
func (c *coordinator) BeginAccepted(sessionID string) *AcceptedRun {
	return c.currentAgent.BeginAccepted(sessionID)
}

func (c *coordinator) Cancel(sessionID string) {
	c.currentAgent.Cancel(sessionID)
}

func (c *coordinator) CancelAll() {
	c.currentAgent.CancelAll()
}

func (c *coordinator) SoftInterrupt(sessionID string) {
	c.currentAgent.SoftInterrupt(sessionID)
}

func (c *coordinator) ClearQueue(sessionID string) {
	c.currentAgent.ClearQueue(sessionID)
}

func (c *coordinator) IsBusy() bool {
	return c.currentAgent.IsBusy()
}

func (c *coordinator) IsSessionBusy(sessionID string) bool {
	return c.currentAgent.IsSessionBusy(sessionID)
}

func (c *coordinator) IsSessionBusyOrAccepted(sessionID string) bool {
	return c.currentAgent.IsSessionBusyOrAccepted(sessionID)
}

func (c *coordinator) Model() Model {
	return c.currentAgent.Model()
}

func (c *coordinator) UpdateModels(ctx context.Context) error {
	// Drop memoized per-call override models so they are rebuilt against
	// the freshly loaded config, exactly like large/small.
	c.overrideCache.Reset(map[string]Model{})

	// build the models again so we make sure we get the latest config
	large, small, err := c.buildAgentModels(ctx, false)
	if err != nil {
		return err
	}
	c.currentAgent.SetModels(large, small)

	agentCfg, ok := c.cfg.Config().Agents[config.AgentCoder]
	if !ok {
		return errCoderAgentNotConfigured
	}

	tools, err := c.buildTools(ctx, agentCfg, false)
	if err != nil {
		return err
	}
	c.currentAgent.SetTools(tools)
	return nil
}

// readiness returns the current readiness group, creating it on first
// use.
func (c *coordinator) readiness() *errgroup.Group {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	if c.readyWg == nil {
		c.readyWg = &errgroup.Group{}
	}
	return c.readyWg
}

// Refresh re-applies the workspace configuration to the live coder
// agent without replacing it: models, the system prompt (so edits to
// CRUSH.md and other context files are picked up), and the tool set,
// under a fresh readiness group so a previously failed build no longer
// poisons runs. This is what a connecting client's /agent/init does now
// that it no longer rebuilds the coordinator (a rebuild would orphan
// runs already dispatched on the old one). When the agent is busy the
// refresh is deferred until it goes idle, exactly like
// UpdateModelsWhenIdle, and deferred=true is returned.
func (c *coordinator) Refresh(ctx context.Context) (deferred bool, err error) {
	if c.currentAgent.IsBusy() {
		go func() {
			waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if waitErr := c.currentAgent.WaitForIdle(waitCtx); waitErr != nil {
				slog.Warn("Gave up waiting for agent idle before refreshing", "error", waitErr)
				return
			}
			if _, rerr := c.Refresh(context.Background()); rerr != nil {
				slog.Error("Failed to apply deferred agent refresh", "error", rerr)
			}
		}()
		return true, nil
	}

	agentCfg, ok := c.cfg.Config().Agents[config.AgentCoder]
	if !ok {
		return false, errCoderAgentNotConfigured
	}
	c.overrideCache.Reset(map[string]Model{})
	large, small, err := c.buildAgentModels(ctx, false)
	if err != nil {
		return false, err
	}
	coderPromptTpl, err := coderPrompt(prompt.WithWorkingDir(cmp.Or(c.effectiveWorkingDir, c.cfg.WorkingDir())))
	if err != nil {
		return false, err
	}
	c.currentAgent.SetModels(large, small)

	// Install a fresh readiness group before scheduling the rebuild so
	// a run dispatched from here on waits on THIS build. Goroutines of
	// a still-running previous build finish on their own group; their
	// late SetSystemPrompt/SetTools are overwritten by this one.
	readyCtx := context.WithoutCancel(ctx)
	ready := &errgroup.Group{}
	c.readyMu.Lock()
	c.readyWg = ready
	c.readyMu.Unlock()
	agent := c.currentAgent
	// current reports whether this build is still the newest: a slower
	// build from an earlier Refresh must not overwrite the prompt or
	// tools a later one installed.
	current := func() bool {
		c.readyMu.Lock()
		defer c.readyMu.Unlock()
		return c.readyWg == ready
	}
	ready.Go(func() error {
		systemPrompt, err := coderPromptTpl.Build(readyCtx, large.Model.Provider(), large.Model.Model(), c.cfg)
		if err != nil {
			return err
		}
		if current() {
			agent.SetSystemPrompt(systemPrompt)
		}
		return nil
	})
	ready.Go(func() error {
		if err := mcp.WaitForInit(readyCtx); err != nil {
			return err
		}
		tools, err := c.buildTools(readyCtx, agentCfg, false)
		if err != nil {
			return err
		}
		if current() {
			agent.SetTools(tools)
		}
		return nil
	})
	return false, nil
}

// SetGoal implements Coordinator.
func (c *coordinator) SetGoal(sessionID, condition string) {
	c.currentAgent.SetGoal(sessionID, condition)
}

// ClearGoal implements Coordinator.
func (c *coordinator) ClearGoal(sessionID string) {
	c.currentAgent.ClearGoal(sessionID)
}

// GoalStatus implements Coordinator.
func (c *coordinator) GoalStatus(sessionID string) (condition string, turns, maxTurns int, active bool) {
	return c.currentAgent.GoalStatus(sessionID)
}

// UpdateModelsWhenIdle applies the latest model/tool configuration. If the
// agent is busy, the apply is deferred: a goroutine blocks (event-driven, no
// polling) until the agent finishes its current turn, then applies. The call
// returns immediately so the RPC handler does not hang. It reports whether
// the apply was deferred so callers can tell the user it was queued.
func (c *coordinator) UpdateModelsWhenIdle(ctx context.Context) (deferred bool, err error) {
	if !c.currentAgent.IsBusy() {
		return false, c.UpdateModels(ctx)
	}
	go func() {
		// Detach from the request context, which is canceled when the RPC
		// returns; bound the wait so a stuck session can't leak the
		// goroutine forever.
		waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if waitErr := c.currentAgent.WaitForIdle(waitCtx); waitErr != nil {
			slog.Warn("Gave up waiting for agent idle before applying model change", "error", waitErr)
			return
		}
		if updErr := c.UpdateModels(context.Background()); updErr != nil {
			slog.Error("Failed to apply queued model change", "error", updErr)
		}
	}()
	return true, nil
}

func (c *coordinator) QueuedPrompts(sessionID string) int {
	return c.currentAgent.QueuedPrompts(sessionID)
}

func (c *coordinator) QueuedPromptsList(sessionID string) []string {
	return c.currentAgent.QueuedPromptsList(sessionID)
}

func (c *coordinator) Summarize(ctx context.Context, sessionID string) error {
	providerCfg, ok := c.cfg.Config().Providers.Get(c.currentAgent.Model().ModelCfg.Provider)
	if !ok {
		return errModelProviderNotConfigured
	}

	if err := c.refreshTokenIfExpired(ctx, providerCfg); err != nil {
		slog.Error("Failed to refresh OAuth2 token before summarize. Proceeding with existing token.", "error", err)
	}

	summarize := func() error {
		return c.currentAgent.Summarize(ctx, sessionID, nil, getProviderOptions(c.currentAgent.Model(), providerCfg))
	}

	return c.runWithUnauthorizedRetry(ctx, providerCfg, summarize)
}

// RegenerateTitle regenerates the session title on demand from the
// conversation so far.
func (c *coordinator) RegenerateTitle(ctx context.Context, sessionID string) error {
	return c.currentAgent.RegenerateTitle(ctx, sessionID)
}

// refreshTokenIfExpired proactively refreshes the OAuth token if it has expired.
func (c *coordinator) refreshTokenIfExpired(ctx context.Context, providerCfg config.ProviderConfig) error {
	if providerCfg.OAuthToken == nil || !providerCfg.OAuthToken.IsExpired() {
		return nil
	}
	slog.Debug("Token needs to be refreshed", "provider", providerCfg.ID)
	return c.refreshOAuth2Token(ctx, providerCfg)
}

// runWithUnauthorizedRetry executes fn. If fn returns a 401 error, it
// attempts to refresh credentials and re-runs fn once. Returns the
// final error: from the retry if a retry was attempted, otherwise from
// the original run. Callers that need to notify the user on persistent
// failure should check isUnauthorized on the returned error.
func (c *coordinator) runWithUnauthorizedRetry(ctx context.Context, providerCfg config.ProviderConfig, fn func() error) error {
	err := fn()
	if err != nil && c.isUnauthorized(err) {
		if retryErr := c.retryAfterUnauthorized(ctx, providerCfg); retryErr == nil {
			return fn()
		}
	}
	return err
}

// retryAfterUnauthorized attempts to refresh credentials after receiving a 401
// and returns nil if retry should be attempted.
func (c *coordinator) retryAfterUnauthorized(ctx context.Context, providerCfg config.ProviderConfig) error {
	switch {
	case providerCfg.OAuthToken != nil:
		slog.Debug("Received 401. Refreshing token and retrying", "provider", providerCfg.ID)
		return c.refreshOAuth2Token(ctx, providerCfg)
	case strings.Contains(providerCfg.APIKeyTemplate, "$"):
		slog.Debug("Received 401. Refreshing API Key template and retrying", "provider", providerCfg.ID)
		return c.refreshAPIKeyTemplate(ctx, providerCfg)
	default:
		return nil
	}
}

func (c *coordinator) isUnauthorized(err error) bool {
	var providerErr *fantasy.ProviderError
	return errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusUnauthorized
}

func (c *coordinator) refreshOAuth2Token(ctx context.Context, providerCfg config.ProviderConfig) error {
	if err := c.cfg.RefreshOAuthToken(ctx, config.ScopeGlobal, providerCfg.ID); err != nil {
		slog.Error("Failed to refresh OAuth token after 401 error", "provider", providerCfg.ID, "error", err)
		return err
	}
	if err := c.UpdateModels(ctx); err != nil {
		return err
	}
	return nil
}

func (c *coordinator) refreshAPIKeyTemplate(ctx context.Context, providerCfg config.ProviderConfig) error {
	newAPIKey, err := c.cfg.Resolve(providerCfg.APIKeyTemplate)
	if err != nil {
		slog.Error("Failed to re-resolve API key after 401 error", "provider", providerCfg.ID, "error", err)
		return err
	}

	providerCfg.APIKey = newAPIKey
	c.cfg.Config().Providers.Set(providerCfg.ID, providerCfg)

	if err := c.UpdateModels(ctx); err != nil {
		return err
	}
	return nil
}

// subAgentParams holds the parameters for running a sub-agent.
type subAgentParams struct {
	Agent          SessionAgent
	SessionID      string
	AgentMessageID string
	ToolCallID     string
	Prompt         string
	SessionTitle   string
	// SessionSetup is an optional callback invoked after session creation
	// but before agent execution, for custom session configuration.
	SessionSetup func(sessionID string)
	// Model, when non-nil, is the per-call model override for this
	// sub-agent run (the tool's `model` parameter).
	Model *config.SelectedModel
	// UseWorkerDefault makes a nil Model fall back to the configured
	// `worker` role before the agent's own large model. Set by the agent
	// and review tools (delegated work); other runSubAgent callers such as
	// agentic_fetch keep the model they were built with.
	UseWorkerDefault bool
}

// runSubAgent runs a sub-agent and handles session management and cost accumulation.
// It creates a sub-session, runs the agent with the given prompt, and propagates
// the cost to the parent session.
func (c *coordinator) runSubAgent(ctx context.Context, params subAgentParams) (fantasy.ToolResponse, error) {
	// Resolve the model BEFORE creating the child session so a bad
	// selection never leaves an empty orphan session behind. Resolution
	// failures are tool errors (not hard errors) so the calling model can
	// correct the reference and retry.
	model := params.Agent.Model()
	var resolveModel func(context.Context) (*Model, error)
	if sel := c.delegationModel(params.Model, params.UseWorkerDefault); sel != nil {
		built, err := c.buildModel(ctx, *sel, true)
		if err != nil {
			return fantasy.NewTextErrorResponse(fmt.Sprintf(
				"cannot run sub-agent on model %s/%s: %s", sel.Provider, sel.Model, err,
			)), nil
		}
		model = built
		resolveModel = c.overrideResolver(*sel, true)
	}

	// Create sub-session
	agentToolSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
	session, err := c.sessions.CreateTaskSession(ctx, agentToolSessionID, params.SessionID, params.SessionTitle)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("create session: %w", err)
	}

	// Call session setup function if provided
	if params.SessionSetup != nil {
		params.SessionSetup(session.ID)
	}

	maxTokens := model.CatwalkCfg.DefaultMaxTokens
	if model.ModelCfg.MaxTokens != 0 {
		maxTokens = model.ModelCfg.MaxTokens
	}

	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return fantasy.ToolResponse{}, errModelProviderNotConfigured
	}

	// Run the agent
	run := func() (*fantasy.AgentResult, error) {
		return params.Agent.Run(ctx, SessionAgentCall{
			SessionID:        session.ID,
			ResolveModel:     resolveModel,
			Prompt:           params.Prompt,
			MaxOutputTokens:  maxTokens,
			ProviderOptions:  getProviderOptions(model, providerCfg),
			Temperature:      model.ModelCfg.Temperature,
			TopP:             model.ModelCfg.TopP,
			TopK:             model.ModelCfg.TopK,
			FrequencyPenalty: model.ModelCfg.FrequencyPenalty,
			PresencePenalty:  model.ModelCfg.PresencePenalty,
			NonInteractive:   true,
		})
	}
	var result *fantasy.AgentResult
	err = c.runWithUnauthorizedRetry(ctx, providerCfg, func() error {
		var runErr error
		result, runErr = run()
		return runErr
	})
	// Notify only if still unauthorized after retry.
	if err != nil && c.isUnauthorized(err) && c.notify != nil && model.ModelCfg.Provider == hyper.Name {
		c.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			Type:       notify.TypeReAuthenticate,
			ProviderID: model.ModelCfg.Provider,
		})
	}
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to generate response: %s", err)), nil
	}

	// Update parent session cost
	if err := c.updateParentSessionCost(ctx, session.ID, params.SessionID); err != nil {
		return fantasy.ToolResponse{}, err
	}

	return fantasy.NewTextResponse(result.Response.Content.Text()), nil
}

// updateParentSessionCost accumulates the cost from a child session to its parent session.
func (c *coordinator) updateParentSessionCost(ctx context.Context, childSessionID, parentSessionID string) error {
	// Serialize the read-modify-write: concurrent sub-agents (e.g. the
	// review tool's parallel reviewers) all accumulate onto the same
	// parent session, and an unlocked += would drop costs.
	c.parentCostMu.Lock()
	defer c.parentCostMu.Unlock()

	childSession, err := c.sessions.Get(ctx, childSessionID)
	if err != nil {
		return fmt.Errorf("get child session: %w", err)
	}

	parentSession, err := c.sessions.Get(ctx, parentSessionID)
	if err != nil {
		return fmt.Errorf("get parent session: %w", err)
	}

	parentSession.Cost += childSession.Cost

	if _, err := c.sessions.Save(ctx, parentSession); err != nil {
		return fmt.Errorf("save parent session: %w", err)
	}

	return nil
}

// discoverSkills is a thin fallback wrapper used only when no
// skills.Manager has been threaded through to the coordinator. All
// production call sites (backend.CreateWorkspace, setupLocalWorkspace)
// run discovery in advance and pass the results via the manager;
// reaching this path means a caller bypassed both. It deliberately does
// NOT publish to the package-level broker — there are no subscribers in
// that case, so doing so would be misleading without delivering the
// snapshot anywhere useful.
func discoverSkills(cfg *config.ConfigStore) (allSkills, activeSkills []*skills.Skill) {
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
	allSkills, activeSkills, states := skills.DiscoverFromConfig(skills.DiscoveryConfig{
		SkillsPaths:    paths,
		DisabledSkills: disabled,
		Resolver:       resolver,
	})
	logDiscoveryStats(states, paths, allSkills, activeSkills, disabled)
	return allSkills, activeSkills
}

// logTurnSkillUsage emits a per-turn diagnostic line showing which skills
// (if any) were loaded during this turn and which looked relevant based on
// a cheap keyword match against the user prompt. The goal is to surface
// "should-have-loaded but didn't" situations for later analysis.
//
// Logged at Info level under component=skills; heavy fields are elided when
// there is nothing interesting to report.
func logTurnSkillUsage(
	sessionID string,
	prompt string,
	activeSkills []*skills.Skill,
	tracker *skills.Tracker,
	before []string,
) {
	if tracker == nil || len(activeSkills) == 0 {
		return
	}

	after := tracker.LoadedNames()

	beforeSet := make(map[string]struct{}, len(before))
	for _, n := range before {
		beforeSet[n] = struct{}{}
	}
	var loadedThisTurn []string
	for _, n := range after {
		if _, ok := beforeSet[n]; !ok {
			loadedThisTurn = append(loadedThisTurn, n)
		}
	}

	slog.Info(
		"Skill turn summary",
		"component", "skills",
		"session_id", sessionID,
		"prompt_len", len(prompt),
		"active_total", len(activeSkills),
		"loaded_total", len(after),
		"loaded_this_turn", loadedThisTurn,
	)
}

// logDiscoveryStats emits a single structured log line summarising skill
// discovery for the current session. It is intentionally low-volume: one
// line per session start. Builtin vs user counts are derived from the
// SkillState.Path — builtin states use the "builtin/" embed prefix.
func logDiscoveryStats(
	states []*skills.SkillState,
	userPaths []string,
	allSkills, activeSkills []*skills.Skill,
	disabled []string,
) {
	var builtinOK, builtinErr, userOK, userErr int
	for _, s := range states {
		isBuiltin := strings.HasPrefix(s.Path, "builtin/")
		switch {
		case isBuiltin && s.State == skills.StateNormal:
			builtinOK++
		case isBuiltin && s.State == skills.StateError:
			builtinErr++
		case !isBuiltin && s.State == skills.StateNormal:
			userOK++
		case !isBuiltin && s.State == skills.StateError:
			userErr++
		}
	}

	activeNames := make([]string, 0, len(activeSkills))
	for _, s := range activeSkills {
		activeNames = append(activeNames, s.Name)
	}

	xml := skills.ToPromptXML(activeSkills)

	slog.Info(
		"Skill discovery complete",
		"component", "skills",
		"builtin_ok", builtinOK,
		"builtin_errors", builtinErr,
		"user_ok", userOK,
		"user_errors", userErr,
		"user_paths", len(userPaths),
		"deduped_total", len(allSkills),
		"active", len(activeSkills),
		"disabled", len(disabled),
		"prompt_bytes", len(xml),
		"prompt_tok_est", skills.ApproxTokenCount(xml),
		"active_names", activeNames,
	)
}
