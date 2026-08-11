package permission

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/pubsub"
)

// hookApprovalKey is the unexported context key used to mark a tool call as
// pre-approved by a PreToolUse hook. The value is the tool call ID so an
// approval can't be reused across calls that happen to share a context.
type hookApprovalKey struct{}

// WithHookApproval returns a context that marks the given tool call ID as
// pre-approved by a hook. When the permission service sees a matching
// request it short-circuits the normal prompt and grants immediately.
func WithHookApproval(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, hookApprovalKey{}, toolCallID)
}

// hookApproved reports whether the context carries a hook approval for the
// given tool call ID.
func hookApproved(ctx context.Context, toolCallID string) bool {
	if toolCallID == "" {
		return false
	}
	v, _ := ctx.Value(hookApprovalKey{}).(string)
	return v == toolCallID
}

type CreatePermissionRequest struct {
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

type PermissionNotification struct {
	SessionID  string `json:"session_id"`
	ToolCallID string `json:"tool_call_id"`
	Granted    bool   `json:"granted"`
	Denied     bool   `json:"denied"`
}

type PermissionRequest struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

type Service interface {
	pubsub.Subscriber[PermissionRequest]
	// GrantPersistent grants a permission request and remembers the grant
	// for the session. It returns true if this call actually resolved the
	// pending request; false if the request had already been resolved
	// (e.g., by another concurrent caller) or is unknown.
	GrantPersistent(permission PermissionRequest) bool
	// Grant grants a permission request. It returns true if this call
	// actually resolved the pending request; false if the request had
	// already been resolved or is unknown.
	Grant(permission PermissionRequest) bool
	// Deny denies a permission request. It returns true if this call
	// actually resolved the pending request; false if the request had
	// already been resolved or is unknown.
	Deny(permission PermissionRequest) bool
	// CancelAll resolves every still-pending request as denied,
	// publishing a resolution notification for each. Called on workspace
	// teardown so no agent goroutine is left blocked in Request and no
	// client is left showing a zombie prompt for a session that no
	// longer exists.
	CancelAll()
	Request(ctx context.Context, opts CreatePermissionRequest) (bool, error)
	AutoApproveSession(sessionID string)
	SetSkipRequests(skip bool)
	SkipRequests() bool
	// SetSysadminMode toggles ephemeral sysadmin mode. When enabled, the
	// bash tool's sysadmin command filter becomes a no-op so commands
	// like curl, ssh, sudo, etc. are allowed.
	SetSysadminMode(enabled bool)
	SysadminMode() bool
	// RepublishPending re-emits the request event for every still-pending
	// request in the given session. A client that just switched to (or
	// re-attached to) a workspace was not subscribed when the request was
	// first published; republishing lets its prompt surface on switch
	// (switch-to-grant) instead of staying invisibly blocked.
	RepublishPending(sessionID string)
	// SetAttachedProbe wires a callback Request uses to detect whether at
	// least one interactive client is currently attached to the workspace
	// that owns a given session. When set, a request that stays
	// continuously unattended (the probe reporting false the whole time)
	// for [unattendedPermissionTimeout] is auto-denied instead of blocking
	// indefinitely — see Request's doc comment. A client attaching at any
	// point resets the clock, so a request is only ever auto-denied while
	// genuinely unwatched. Passing nil restores the original unbounded
	// wait (the default when this is never called). The sessionID argument
	// lets an implementation scope the check, but callers are expected to
	// answer workspace-level "is anyone here", not "is this exact session
	// focused".
	SetAttachedProbe(fn func(sessionID string) bool)
	SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[PermissionNotification]
}

// PermissionKey is a composite key for session permission lookups.
type PermissionKey struct {
	SessionID string
	ToolName  string
	Action    string
	Path      string
}

type permissionService struct {
	*pubsub.Broker[PermissionRequest]

	notificationBroker    *pubsub.Broker[PermissionNotification]
	workingDir            string
	sessionPermissions    *csync.Map[PermissionKey, bool]
	pendingRequests       *csync.Map[string, pendingPermission]
	autoApproveSessions   map[string]bool
	autoApproveSessionsMu sync.RWMutex
	skip                  atomic.Bool
	sysadmin              atomic.Bool
	allowedTools          []string

	// perSessionMu serializes permission requests within a single
	// session WITHOUT blocking other sessions. A workspace runs many
	// sessions concurrently (e.g. swarm-dispatched background turns); a
	// single workspace-wide mutex held across the blocking wait in
	// Request would wedge every other session's prompt behind one
	// in-flight request (head-of-line blocking) — in client/server mode
	// this manifested as a silent stall when a background session hit a
	// prompt. Keyed by session ID.
	//
	// Bound: one entry per distinct session ID for the lifetime of the
	// process. It is never pruned — there is no session-teardown hook on
	// this service, and deleting a mutex on request resolution would
	// race a concurrent Request about to lock it. This matches the
	// existing per-session maps here (sessionPermissions,
	// autoApproveSessions), is bounded by the total number of sessions,
	// and each entry is a tiny *sync.Mutex, so the footprint is
	// negligible.
	perSessionMu *csync.Map[string, *sync.Mutex]
	// perSessionMuCreate serializes creation of per-session mutexes so
	// two concurrent Request calls for the same session cannot create
	// (and then lock) two different mutex instances — which would defeat
	// the serialization. It guards only the tiny create-if-absent step,
	// never held across a request wait.
	perSessionMuCreate sync.Mutex

	// attachedProbe, when non-nil, reports whether at least one
	// interactive client is currently attached to (and viewing) a given
	// session. Wired in by the backend layer (which owns client
	// tracking) via SetAttachedProbe; this package has no notion of
	// clients on its own. Guarded by attachedProbeMu since it can be set
	// concurrently with an in-flight Request.
	attachedProbeMu sync.RWMutex
	attachedProbe   func(sessionID string) bool
}

// pendingPermission is the in-flight state for a published request.
// The full request is stored (not just the channel) so a resolution
// notification is always built from the trusted, server-minted request
// and CancelAll can publish a correct notification for each pending
// entry at teardown.
type pendingPermission struct {
	req    PermissionRequest
	respCh chan bool
}

// sessionLock returns the mutex guarding requests for a session,
// creating it on first use. Creation is serialized by perSessionMuCreate
// so every caller for a given session observes the same mutex instance.
func (s *permissionService) sessionLock(sessionID string) *sync.Mutex {
	s.perSessionMuCreate.Lock()
	defer s.perSessionMuCreate.Unlock()
	if mu, ok := s.perSessionMu.Get(sessionID); ok {
		return mu
	}
	mu := &sync.Mutex{}
	s.perSessionMu.Set(sessionID, mu)
	return mu
}

// resolve atomically removes the pending request entry for the given
// permission and, if it was still pending, publishes exactly one
// PermissionNotification and forwards the outcome to the waiter on
// respCh. It returns true if this call resolved the request, false if
// it had already been resolved (e.g., by another concurrent caller) or
// the request ID is unknown.
//
// If onResolve is non-nil it runs after the pending entry has been
// taken but before the notification is published or the waiter is
// unblocked. This lets GrantPersistent record the session permission
// only when it actually wins the race, so a losing GrantPersistent
// that lost to a Deny does not leak an auto-approve entry.
//
// All three public resolution methods (Grant, GrantPersistent, Deny)
// route through this helper so multi-subscriber UIs can race safely:
// the first caller wins, the rest become no-ops.
func (s *permissionService) resolve(permission PermissionRequest, granted, denied bool, onResolve func()) bool {
	p, ok := s.pendingRequests.Take(permission.ID)
	if !ok {
		return false
	}

	if onResolve != nil {
		onResolve()
	}

	// Build the notification from the STORED request so routing
	// (SessionID/ToolCallID) is always the trusted server-minted value,
	// not caller-supplied fields.
	s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
		SessionID:  p.req.SessionID,
		ToolCallID: p.req.ToolCallID,
		Granted:    granted,
		Denied:     denied,
	})

	// respCh is buffered (cap 1) and only ever has at most one sender
	// per request because Take removes the entry under the map lock,
	// so this send never blocks.
	p.respCh <- granted
	return true
}

// CancelAll resolves every still-pending request as denied. Each entry
// is resolved through the same atomic Take path as Grant/Deny, so a
// concurrent real resolution races safely (first wins). Used on
// workspace teardown to unblock waiting agent goroutines and clear
// zombie prompts.
func (s *permissionService) CancelAll() {
	for id := range s.pendingRequests.Seq2() {
		s.resolve(PermissionRequest{ID: id}, false, true, nil)
	}
}

// RepublishPending re-emits the request event for every still-pending
// request in the given session so a newly-subscribed client surfaces it.
func (s *permissionService) RepublishPending(sessionID string) {
	for _, p := range s.pendingRequests.Seq2() {
		if p.req.SessionID == sessionID {
			s.Publish(pubsub.CreatedEvent, p.req)
		}
	}
}

func (s *permissionService) GrantPersistent(permission PermissionRequest) bool {
	// Record the persistent grant only if this call wins the
	// pending-request race. Otherwise a losing GrantPersistent that
	// lost to a Deny would still leave an auto-approve entry behind,
	// silently flipping later denied calls to allowed.
	return s.resolve(permission, true, false, func() {
		s.sessionPermissions.Set(PermissionKey{
			SessionID: permission.SessionID,
			ToolName:  permission.ToolName,
			Action:    permission.Action,
			Path:      permission.Path,
		}, true)
	})
}

func (s *permissionService) Grant(permission PermissionRequest) bool {
	return s.resolve(permission, true, false, nil)
}

func (s *permissionService) Deny(permission PermissionRequest) bool {
	return s.resolve(permission, false, true, nil)
}

func (s *permissionService) Request(ctx context.Context, opts CreatePermissionRequest) (bool, error) {
	if s.skip.Load() {
		return true, nil
	}

	// Check if the tool/action combination is in the allowlist
	commandKey := opts.ToolName + ":" + opts.Action
	if slices.Contains(s.allowedTools, commandKey) || slices.Contains(s.allowedTools, opts.ToolName) {
		return true, nil
	}

	// A PreToolUse hook that returned decision=allow stamps the context
	// with the tool call ID. Treat that as a pre-approval and skip the
	// prompt entirely. We still publish a granted notification so the UI
	// and audit subscribers see the outcome.
	if hookApproved(ctx, opts.ToolCallID) {
		s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
			SessionID:  opts.SessionID,
			ToolCallID: opts.ToolCallID,
			Granted:    true,
		})
		return true, nil
	}

	// Serialize requests within this session only. Different sessions
	// proceed concurrently so a background session's prompt never blocks
	// behind another session's in-flight request.
	mu := s.sessionLock(opts.SessionID)
	mu.Lock()
	defer mu.Unlock()

	// tell the UI that a permission was requested
	s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
		SessionID:  opts.SessionID,
		ToolCallID: opts.ToolCallID,
	})

	s.autoApproveSessionsMu.RLock()
	autoApprove := s.autoApproveSessions[opts.SessionID]
	s.autoApproveSessionsMu.RUnlock()

	if autoApprove {
		s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
			SessionID:  opts.SessionID,
			ToolCallID: opts.ToolCallID,
			Granted:    true,
		})
		return true, nil
	}

	fileInfo, err := os.Stat(opts.Path)
	dir := opts.Path
	if err == nil {
		if fileInfo.IsDir() {
			dir = opts.Path
		} else {
			dir = filepath.Dir(opts.Path)
		}
	}

	if dir == "." {
		dir = s.workingDir
	}
	permission := PermissionRequest{
		ID:          uuid.New().String(),
		Path:        dir,
		SessionID:   opts.SessionID,
		ToolCallID:  opts.ToolCallID,
		ToolName:    opts.ToolName,
		Description: opts.Description,
		Action:      opts.Action,
		Params:      opts.Params,
	}

	if _, ok := s.sessionPermissions.Get(PermissionKey{
		SessionID: permission.SessionID,
		ToolName:  permission.ToolName,
		Action:    permission.Action,
		Path:      permission.Path,
	}); ok {
		s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
			SessionID:  opts.SessionID,
			ToolCallID: opts.ToolCallID,
			Granted:    true,
		})
		return true, nil
	}

	respCh := make(chan bool, 1)
	s.pendingRequests.Set(permission.ID, pendingPermission{req: permission, respCh: respCh})
	defer s.pendingRequests.Del(permission.ID)

	// Publish the request
	s.Publish(pubsub.CreatedEvent, permission)

	// done stops the unattended-timeout watcher goroutine as soon as
	// this call returns via any path, so it never outlives a single
	// Request.
	done := make(chan struct{})
	defer close(done)
	unattended := s.unattendedDeadline(opts.SessionID, done)

	select {
	case <-ctx.Done():
		// The run was cancelled (or otherwise torn down) while the
		// prompt was still pending. Resolve the request as denied so
		// any open permission dialog on clients viewing this session
		// is dismissed; without this the dialog would hang open until
		// manually closed, making the cancel appear to do nothing.
		// resolve is a no-op if a concurrent Grant/Deny already won.
		s.resolve(permission, false, true, nil)
		return false, ctx.Err()
	case granted := <-respCh:
		return granted, nil
	case <-unattended:
		// A concurrent Grant/Deny may be resolving this request at the
		// same instant the timeout fires. resolve() is first-writer-wins
		// (it Takes the pending entry), so use its return value as the
		// authority: if our auto-deny actually won, report denied; if it
		// lost, the winner has Taken the entry and is guaranteed to send
		// its real outcome on the buffered respCh, so adopt that instead
		// of hardcoding a denial. A bare non-blocking peek before resolve
		// is insufficient — the winner's Take can happen before its respCh
		// send, leaving a window where the peek sees nothing yet the
		// request is already granted.
		if !s.resolve(permission, false, true, nil) {
			return <-respCh, nil
		}
		// Nobody has been attached to this session's workspace for the
		// entire unattended grace window: there is no one left to answer
		// this prompt, so waiting any longer would just hang the tool call
		// (and the whole turn) indefinitely — the exact failure mode that
		// made a cross-workspace swarm message delivered to a non-yolo,
		// unattended workspace look like it "timed out" forever rather than
		// actually completing. Auto-deny (we won the race above): fail
		// closed so a background session without a human watching it cannot
		// silently escalate its own permissions.
		slog.Warn("Auto-denying permission request: no interactive client is attached to answer it",
			"session_id", opts.SessionID, "tool_call_id", opts.ToolCallID, "tool_name", opts.ToolName,
			"timeout", unattendedPermissionTimeout)
		return false, nil
	}
}

// unattendedPermissionTimeout bounds how long Request blocks a session's
// tool call waiting for a permission grant when [Service.SetAttachedProbe]
// reports that no interactive client has been attached (and watching) the
// whole time. It only applies once a probe is wired in; callers that never
// call SetAttachedProbe keep the original unbounded wait.
var unattendedPermissionTimeout = 2 * time.Minute

// unattendedPermissionPollInterval is how often unattendedDeadline
// re-checks the attached-probe while a request is pending.
var unattendedPermissionPollInterval = 5 * time.Second

// unattendedDeadline returns a channel that closes once sessionID has gone
// continuously unattended (attachedProbe reporting false) for
// unattendedPermissionTimeout. It returns nil — which blocks forever in a
// select, i.e. imposes no timeout — when no probe is wired in, preserving
// the original behavior for embedders that never call SetAttachedProbe
// (e.g. single-process CLI runs, or tests). The watcher goroutine it starts
// exits as soon as either the deadline fires or done is closed by the
// caller, so it never outlives the Request call that started it.
func (s *permissionService) unattendedDeadline(sessionID string, done <-chan struct{}) <-chan struct{} {
	s.attachedProbeMu.RLock()
	probe := s.attachedProbe
	s.attachedProbeMu.RUnlock()
	if probe == nil {
		return nil
	}

	// Capture the timeout/poll interval as local copies rather than
	// reading the package vars from inside the goroutine below: the
	// goroutine can outlive this call by a few poll intervals (it only
	// learns to stop once it next wakes up after done closes), and tests
	// mutate these vars around each Request call to exercise the
	// timeout on a short fuse. Reading the shared var directly from the
	// goroutine would race against that mutation once this call
	// returns; local copies make the goroutine's behavior fixed at
	// start, matching what the caller intended when the request began.
	timeout := unattendedPermissionTimeout
	poll := unattendedPermissionPollInterval

	fired := make(chan struct{})
	go func() {
		ticker := time.NewTicker(poll)
		defer ticker.Stop()

		var unattendedSince time.Time
		if !probe(sessionID) {
			unattendedSince = time.Now()
		}
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if probe(sessionID) {
					unattendedSince = time.Time{}
					continue
				}
				if unattendedSince.IsZero() {
					unattendedSince = time.Now()
					continue
				}
				if time.Since(unattendedSince) >= timeout {
					close(fired)
					return
				}
			}
		}
	}()
	return fired
}

func (s *permissionService) AutoApproveSession(sessionID string) {
	s.autoApproveSessionsMu.Lock()
	s.autoApproveSessions[sessionID] = true
	s.autoApproveSessionsMu.Unlock()
}

func (s *permissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[PermissionNotification] {
	return s.notificationBroker.Subscribe(ctx)
}

func (s *permissionService) SetSkipRequests(skip bool) {
	s.skip.Store(skip)
}

func (s *permissionService) SkipRequests() bool {
	return s.skip.Load()
}

func (s *permissionService) SetSysadminMode(enabled bool) {
	s.sysadmin.Store(enabled)
}

func (s *permissionService) SysadminMode() bool {
	return s.sysadmin.Load()
}

func (s *permissionService) SetAttachedProbe(fn func(sessionID string) bool) {
	s.attachedProbeMu.Lock()
	s.attachedProbe = fn
	s.attachedProbeMu.Unlock()
}

func NewPermissionService(workingDir string, skip bool, allowedTools []string) Service {
	svc := &permissionService{
		Broker:              pubsub.NewBroker[PermissionRequest](),
		notificationBroker:  pubsub.NewBroker[PermissionNotification](),
		workingDir:          workingDir,
		sessionPermissions:  csync.NewMap[PermissionKey, bool](),
		autoApproveSessions: make(map[string]bool),
		allowedTools:        allowedTools,
		pendingRequests:     csync.NewMap[string, pendingPermission](),
		perSessionMu:        csync.NewMap[string, *sync.Mutex](),
	}
	svc.skip.Store(skip)
	return svc
}
