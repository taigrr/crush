// Package agent is the core orchestration layer for Crush AI agents.
//
// It provides session-based AI agent functionality for managing
// conversations, tool execution, and message handling. It coordinates
// interactions between language models, messages, sessions, and tools while
// handling features like automatic summarization, queuing, and token
// management.
package agent

import (
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/checkpoint"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/journal"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/milestone"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/version"
	"github.com/taigrr/fantasy"
)

const (
	DefaultSessionName = "Untitled Session"

	// retitleAfterUserMessages is the user-message count at which the session
	// title is regenerated from accumulated conversation context.
	retitleAfterUserMessages = 10

	// Constants for auto-summarization thresholds
	largeContextWindowThreshold = 200_000
	largeContextWindowBuffer    = 20_000
	smallContextWindowRatio     = 0.2
)

var userAgent = fmt.Sprintf("Charm-Crush/%s (https://charm.land/crush)", version.Version)

// Used to remove <think> tags from generated titles.
var (
	thinkTagRegex       = regexp.MustCompile(`(?s)<think>.*?</think>`)
	orphanThinkTagRegex = regexp.MustCompile(`</?think>`)
)

type SessionAgentCall struct {
	SessionID string
	// RunID, when non-empty, is the caller-supplied correlator that
	// gets echoed back on the notify.RunComplete event emitted for
	// this turn. It is preserved when the call is enqueued behind a
	// busy session so the queued turn's terminal event is still
	// recognisable to the original caller. Callers that need a
	// reliable completion contract (e.g. `crush run` against a
	// session that may be busy) MUST set it; SessionID alone is
	// ambiguous when concurrent turns share the same session.
	RunID  string
	Prompt string
	// Steer marks a mid-turn steering message. It only has an effect when
	// the session is busy and the call is enqueued: in addition to the
	// usual fold-at-next-step behavior of a call without a RunID, the
	// agent raises the session's soft interrupt so tools that opt in
	// (see tools.SoftInterrupt) wrap up the current step early and the
	// steer is delivered sooner. It never cancels a tool or a turn. A
	// steer should carry an empty RunID so it folds rather than waiting
	// for its own turn; callers that set a RunID get the queued-turn
	// behavior plus the early wrap-up.
	Steer bool
	// ResolveModel, when non-nil, returns the model this turn runs on
	// instead of the agent's large model. The coordinator sets it for
	// sub-agent runs from the tool's per-call `model` parameter or the
	// configured `worker` role. It is a resolver rather than a built Model
	// so the provider is rebuilt at Run time — after any OAuth/API-key
	// refresh (which resets the coordinator's override cache), on each
	// unauthorized retry, and when a queued turn is dispatched — instead
	// of reusing a client built with expired credentials. The SELECTION it
	// resolves is fixed at accept time so it always matches the tuning
	// (max tokens, provider options) on this call. Nil means the agent's
	// own model. A resolution error fails the turn rather than silently
	// substituting another model.
	ResolveModel func(context.Context) (*Model, error)
	// SwarmParts, when non-empty, replaces the default single
	// TextContent user-message part with these SwarmMessage parts
	// (see message.SwarmMessage). Used by Backend.SwarmSend so the
	// receiving session's transcript records structured sender
	// metadata (color, animal, workspace) instead of a plain text
	// prefix. Prompt is still set to the concatenated user-visible
	// text so downstream queue-drop / log paths that read only the
	// prompt keep working.
	SwarmParts       []message.SwarmMessage
	ProviderOptions  fantasy.ProviderOptions
	Attachments      []message.Attachment
	MaxOutputTokens  int64
	Temperature      *float64
	TopP             *float64
	TopK             *int64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	NonInteractive   bool
	// OnComplete, when non-nil, replaces the default RunComplete
	// publish path: the inner Run hands the terminal payload to this
	// callback instead of emitting it on the RunComplete broker. The
	// coordinator uses this hook to coalesce the unauthorized →
	// re-auth → retry chain into a single user-visible terminal
	// event, so non-interactive clients (e.g. `crush run`) don't
	// exit on a stale failed-attempt RunComplete before the
	// successful retry. It is intentionally stripped when queueing
	// a busy-session call (see Run): the originating
	// coordinator.Run has long returned by the time the queued
	// recursion drains, so falling back to the default broker
	// publish keeps the event visible to subscribers.
	OnComplete func(notify.RunComplete)
	// Accepted, when non-nil, is the accept reservation taken by
	// BeginAccepted before the call was dispatched onto a goroutine
	// (the client/server fire-and-forget path). Run consumes it under
	// dispatchMu[SessionID] once the accepted -> (cancel-on-entry |
	// queued | active) transition has been chosen. When nil
	// (in-process / local callers like AppWorkspace), behavior is
	// unchanged and no accept tracking applies.
	Accepted *AcceptedRun
	// acceptSeq carries the accept sequence of the handle that produced
	// this call after it has been enqueued and its Accepted handle
	// stripped. The queue-drain paths compare it against a session's
	// cancel mark so a follow-up queued before a cancel is dropped while
	// one queued after the cancel survives. 0 means untracked (an
	// in-process enqueue with no accept reservation), which the drain
	// paths treat as covered by any present mark, preserving the
	// pre-sequence behavior.
	acceptSeq uint64
	// aside marks a system-originated notice (a finished background job)
	// parked in pendingAsides rather than a user prompt, so a failed fold
	// can put it back where it came from.
	aside bool
}

type SessionAgent interface {
	Run(context.Context, SessionAgentCall) (*fantasy.AgentResult, error)
	BeginAccepted(sessionID string) *AcceptedRun
	SetModels(large Model, small Model)
	SetTools(tools []fantasy.AgentTool)
	SetSystemPrompt(systemPrompt string)
	Cancel(sessionID string)
	CancelAll()
	// SoftInterrupt asks the tools running in the session's current step
	// to wrap up early without cancelling them (see tools.SoftInterrupt).
	// The model then sees their (complete) results and continues the
	// turn. It is a no-op for an idle session and idempotent within a
	// step; the next step re-arms it.
	SoftInterrupt(sessionID string)
	IsSessionBusy(sessionID string) bool
	IsSessionBusyOrAccepted(sessionID string) bool
	IsBusy() bool
	WaitForIdle(ctx context.Context) error
	QueuedPrompts(sessionID string) int
	QueuedPromptsList(sessionID string) []string
	ClearQueue(sessionID string)
	// Summarize compacts the session's history. model, when non-nil, is
	// the model the turn ran on (a per-call override); nil runs the
	// agent's large model.
	Summarize(context.Context, string, *Model, fantasy.ProviderOptions) error
	RegenerateTitle(ctx context.Context, sessionID string) error
	Model() Model

	// Goal control for the autonomous /goal feature.
	SetGoal(sessionID, condition string)
	ClearGoal(sessionID string)
	GoalStatus(sessionID string) (condition string, turns, maxTurns int, active bool)
	AdvanceGoal(ctx context.Context, sessionID string) (cont bool, prompt string)
}

type Model struct {
	Model      fantasy.LanguageModel
	CatwalkCfg catwalk.Model
	ModelCfg   config.SelectedModel
	FlatRate   bool
}

type sessionAgent struct {
	largeModel         *csync.Value[Model]
	smallModel         *csync.Value[Model]
	systemPromptPrefix *csync.Value[string]
	systemPrompt       *csync.Value[string]
	tools              *csync.Slice[fantasy.AgentTool]

	isSubAgent           bool
	sessions             session.Service
	messages             message.Service
	checkpoints          checkpoint.Service
	milestones           milestone.Service
	disableAutoSummarize bool
	isYolo               bool
	notify               pubsub.Publisher[notify.Notification]
	runComplete          pubsub.Publisher[notify.RunComplete]
	// workingDir resolves the worktree-aware directory tools run in for a
	// turn. Used only to inform the model of its cwd; nil omits the note.
	workingDir tools.WorkingDirFunc

	messageQueue   *csync.Map[string, []SessionAgentCall]
	activeRequests *csync.Map[string, context.CancelFunc]
	goals          *csync.Map[string, *goalState] // active /goal state per session
	// softInterrupts holds, per busy session, the channel handed to the
	// current step's tools via tools.WithSoftInterrupt. It is re-armed
	// (replaced with a fresh open channel) at every PrepareStep under the
	// session's dispatch mutex, atomically with the queue drain, and
	// closed by SoftInterrupt / a Steer enqueue under the same mutex.
	// That ordering guarantees a steer is either folded into the step
	// being prepared or interrupts the step that was just armed — never
	// lost between the two.
	softInterrupts *csync.Map[string, chan struct{}]
	// pendingAsides holds system-originated notices (a background job
	// finished) waiting to be folded into a session's conversation. They
	// are drained alongside the message queue at every PrepareStep but,
	// unlike queued prompts, never start a turn on their own: a notice
	// for an idle session waits for the next user-initiated turn. They
	// are not user prompts, so they are not counted by QueuedPrompts.
	pendingAsides *csync.Map[string, []SessionAgentCall]

	// queueJournal, when non-nil, receives a snapshot of a session's
	// queue after every mutation so the queue survives a server swap.
	// journalMu serializes snapshot+write pairs: each write reads the
	// live queue under the lock, so however concurrent mutations
	// interleave, the last write always reflects the newest state.
	queueJournal    QueueJournal
	journalMu       sync.Mutex
	onQueueDrop     func(SessionAgentCall)
	onQueueDispatch func(SessionAgentCall)
	// dispatchPaused, once set, stops a finished turn from handing off
	// to the next queued prompt. The queue stays in memory (and in the
	// journal) for the next server to run. Set by a draining server.
	dispatchPaused atomic.Bool

	// dispatchMu holds a per-session mutex that serializes the
	// accepted -> (cancel-on-entry | queued | active) transition in
	// Run against a concurrent Cancel. The lock is held only during
	// the brief handoff (no DB or LLM I/O under the lock).
	dispatchMu *csync.Map[string, *sync.Mutex]
	// acceptedRuns counts dispatched-but-not-yet-active runs per
	// session. A counter > 0 means a dispatched prompt is in flight
	// and has not yet completed the dispatch handoff in Run. Only
	// BeginAccepted increments it; only AcceptedRun.Close decrements
	// it.
	acceptedRuns *csync.Map[string, int]
	// cancelMark records, per session, a high-water accept sequence: an
	// accepted handle is canceled by it iff the handle's sequence is at
	// or below the mark. Cancel raises the mark to the latest sequence
	// assigned at cancel time, so a single Cancel covers every prompt
	// accepted-but-not-yet-active then, while a prompt accepted later
	// (higher sequence) is never poisoned. Absent or 0 means no pending
	// cancel. It is only raised by Cancel when acceptedRuns > 0, so an
	// idle Escape never records a mark.
	cancelMark *csync.Map[string, uint64]
	// dispatchMuCreate guards lazy creation of per-session entries in
	// dispatchMu so two goroutines can't race to lock different mutex
	// instances for the same session.
	dispatchMuCreate sync.Mutex

	// idleMu guards idleCh. idleCh is closed-and-replaced every time an
	// active request is cleared, giving WaitForIdle an event-driven wakeup
	// (no polling) when the agent may have transitioned to idle.
	idleMu sync.Mutex
	idleCh chan struct{}
	// acceptedMu serializes increments/decrements of acceptedRuns and
	// the assignment of accept sequence numbers from acceptSeqGen. It
	// is separate from dispatchMu so AcceptedRun.Close (which may run
	// while Run holds dispatchMu for the same session) does not
	// deadlock by re-entering the dispatch lock.
	acceptedMu sync.Mutex
	// acceptSeqGen is the monotonic source of accept sequence numbers.
	// Each BeginAccepted increments it under acceptedMu and stamps the
	// returned handle, so sequences strictly increase in accept order
	// across the agent. Cancel uses its current value as the per-session
	// high-water mark.
	acceptSeqGen uint64
}

type SessionAgentOptions struct {
	LargeModel           Model
	SmallModel           Model
	SystemPromptPrefix   string
	SystemPrompt         string
	IsSubAgent           bool
	DisableAutoSummarize bool
	IsYolo               bool
	Sessions             session.Service
	Messages             message.Service
	Checkpoints          checkpoint.Service
	Milestones           milestone.Service
	Tools                []fantasy.AgentTool
	Notify               pubsub.Publisher[notify.Notification]
	RunComplete          pubsub.Publisher[notify.RunComplete]
	// WorkingDir resolves the directory tools run in for the current turn
	// (worktree-aware). Used to tell the model its cwd; when nil the
	// environment note is omitted.
	WorkingDir tools.WorkingDirFunc
	// QueueJournal, when non-nil, persists the per-session prompt queue.
	// Only the top-level coder agent should carry one; sub-agent
	// sessions are hidden children that do not survive a restart.
	QueueJournal QueueJournal
	// OnQueueDrop, when non-nil, is called for every queued call that is
	// discarded without running (cancelled, cleared, dead context). The
	// coordinator uses it to release swarm reply obligations registered
	// when the call was accepted, which would otherwise outlive the
	// message they belong to.
	OnQueueDrop func(SessionAgentCall)
	// OnQueueDispatch, when non-nil, is called for every queued call as
	// it leaves the queue to run — as its own turn, or folded into the
	// active one. The coordinator uses it to mark the swarm reply
	// obligations the call carries as delivered, so they become
	// enforceable only once the agent has actually seen the message.
	OnQueueDispatch func(SessionAgentCall)
}

// QueueJournal persists a session's queued prompts so a server that
// drains and exits for an update leaves them for its successor. It is
// satisfied by *journal.Store. SaveQueue is called with the session's
// full current queue after every mutation; an empty slice deletes.
type QueueJournal interface {
	SaveQueue(ctx context.Context, sessionID string, entries []journal.QueuedPrompt) error
}

// Drainable is implemented by coordinators (and session agents) that
// can participate in a graceful server drain. Like SwarmConfigurable it
// is a side-channel interface so Coordinator test mocks need not know
// about draining.
type Drainable interface {
	// PauseQueueDispatch stops finished turns from handing off to
	// queued prompts. Already-queued prompts stay in memory and in the
	// journal for the next server to run.
	PauseQueueDispatch()
	// DetachJournals stops writing queue and reply-obligation changes
	// through to the database. Called right before a drained server
	// tears its workspaces down so the teardown-time clears do not
	// erase the persisted state the next server should rehydrate.
	DetachJournals()
	// BusySessions lists the sessions with an active or accepted run.
	BusySessions() []string
	// DeferPrompt appends a prompt to a session's queue without
	// dispatching it. Used for swarm messages that arrive while
	// draining (journaled for the next server) and for the tail of a
	// replayed queue (run in order once the head's turn ends). runID,
	// when non-empty, makes the entry run as its own turn instead of
	// being folded into the active step.
	DeferPrompt(sessionID, runID, prompt string, attachments []message.Attachment, parts []message.SwarmMessage)
	// RequeueFront is DeferPrompt at the head of the queue. Used when a
	// replayed queue head could not be dispatched: it goes back in
	// front of the tail that was already re-queued behind it.
	RequeueFront(sessionID, runID, prompt string, attachments []message.Attachment, parts []message.SwarmMessage)
}

func NewSessionAgent(
	opts SessionAgentOptions,
) SessionAgent {
	return &sessionAgent{
		largeModel:           csync.NewValue(opts.LargeModel),
		smallModel:           csync.NewValue(opts.SmallModel),
		systemPromptPrefix:   csync.NewValue(opts.SystemPromptPrefix),
		systemPrompt:         csync.NewValue(opts.SystemPrompt),
		isSubAgent:           opts.IsSubAgent,
		sessions:             opts.Sessions,
		messages:             opts.Messages,
		checkpoints:          opts.Checkpoints,
		milestones:           opts.Milestones,
		disableAutoSummarize: opts.DisableAutoSummarize,
		tools:                csync.NewSliceFrom(opts.Tools),
		isYolo:               opts.IsYolo,
		notify:               opts.Notify,
		runComplete:          opts.RunComplete,
		workingDir:           opts.WorkingDir,
		queueJournal:         opts.QueueJournal,
		onQueueDrop:          opts.OnQueueDrop,
		onQueueDispatch:      opts.OnQueueDispatch,
		messageQueue:         csync.NewMap[string, []SessionAgentCall](),
		activeRequests:       csync.NewMap[string, context.CancelFunc](),
		dispatchMu:           csync.NewMap[string, *sync.Mutex](),
		acceptedRuns:         csync.NewMap[string, int](),
		cancelMark:           csync.NewMap[string, uint64](),
		goals:                csync.NewMap[string, *goalState](),
		softInterrupts:       csync.NewMap[string, chan struct{}](),
		pendingAsides:        csync.NewMap[string, []SessionAgentCall](),
		idleCh:               make(chan struct{}),
	}
}

// AcceptedRun owns exactly one accept reservation taken by
// BeginAccepted. It is the only carrier of accept-state across the
// backend.runAgent / Coordinator.Run / sessionAgent.Run layers: a
// counter > 0 means a dispatched prompt is in flight and has not yet
// completed the dispatch handoff in Run. Close is the only way to
// release the reservation and is idempotent.
type AcceptedRun struct {
	agent     *sessionAgent
	sessionID string
	// seq is the monotonic accept sequence stamped by BeginAccepted. A
	// cancel covers this handle iff seq is at or below the session's
	// cancel mark, so a handle accepted after a cancel (higher seq) is
	// never poisoned by it.
	seq  uint64
	done atomic.Bool
}

// Close decrements the accept counter for this reservation. It is safe
// to call multiple times; only the first call has effect.
func (a *sessionAgent) SetModels(large Model, small Model) {
	a.largeModel.Set(large)
	a.smallModel.Set(small)
}

func (a *sessionAgent) SetTools(tools []fantasy.AgentTool) {
	a.tools.SetSlice(tools)
}

func (a *sessionAgent) SetSystemPrompt(systemPrompt string) {
	a.systemPrompt.Set(systemPrompt)
}

func (a *sessionAgent) Model() Model {
	return a.largeModel.Get()
}

// turnModel returns the model a call runs on: its explicit override, or
// the agent's own large model. Resolved per call, never at construction,
// because the override is rebuilt through the coordinator's cache after a
// credential refresh.
func (a *sessionAgent) turnModel(ctx context.Context, call SessionAgentCall) (Model, error) {
	if call.ResolveModel != nil {
		m, err := call.ResolveModel(ctx)
		if err != nil {
			return Model{}, err
		}
		if m != nil && m.Model != nil {
			return *m, nil
		}
	}
	return a.largeModel.Get(), nil
}

// convertToToolResult converts a fantasy tool result to a message tool result.
