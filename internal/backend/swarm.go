package backend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/home"
	"github.com/taigrr/crush/internal/journal"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/sound"
	"github.com/taigrr/crush/internal/swarm"
)

// Swarm errors.
var (
	ErrSwarmAddressAmbiguous  = errors.New("swarm address matches multiple sessions")
	ErrSwarmAddressNotFound   = errors.New("swarm address does not match any session")
	ErrSwarmTargetIsSubagent  = errors.New("swarm target is a sub-agent session (not addressable)")
	ErrSwarmSelfAddressed     = errors.New("swarm target is the sender's own session")
	ErrSwarmWorkspaceNotFound = errors.New("swarm target workspace not found")
	// ErrSwarmWorkingDirOutside is returned by CreateSwarmSession when
	// the requested working_dir does not resolve to the target
	// workspace's project (neither a subdirectory nor a linked git
	// worktree of it).
	ErrSwarmWorkingDirOutside = errors.New("swarm working_dir must be inside the target workspace")
)

// SwarmLookupResult describes a resolved swarm address across all
// running workspaces. Callers hold the returned WorkspaceID/SessionID
// as an opaque pair for [Backend.SwarmSend].
type SwarmLookupResult struct {
	WorkspaceID string
	SessionID   string
	Color       string
	Animal      string
	// WorkspaceRoot is the target workspace's resolved project root. It
	// lets [Backend.SwarmSend] re-bring-up the workspace if it was
	// idle-torn-down between address resolution and delivery, so a
	// cross-workspace send never spuriously fails with "workspace not
	// found" due to that race.
	WorkspaceRoot string
	// Sub is true when the session is a title/summary/task-tool
	// sub-session; callers should refuse to send.
	Sub bool
}

// LookupSwarmAddress finds every session across every running
// workspace whose color/animal (and optional short-hash) matches the
// address. Raw UUID addresses match by session id.
//
// Per-workspace errors do not abort the search: a transient DB
// failure in one workspace never masks a resolvable match in
// another. If every workspace errors AND no matches were found, the
// first error is returned; otherwise the matches (or NotFound /
// Ambiguous) drive the result.
//
// Returns:
//   - one match: the resolved session.
//   - zero matches: ErrSwarmAddressNotFound (or the first per-workspace
//     error when every workspace errored).
//   - multiple matches: ErrSwarmAddressAmbiguous. Callers should
//     retry with the shorthash form (or a raw session id) to
//     disambiguate.
func (b *Backend) LookupSwarmAddress(ctx context.Context, addrStr string) (SwarmLookupResult, error) {
	addr, ok := swarm.ParseAddress(addrStr)
	if !ok {
		return SwarmLookupResult{}, fmt.Errorf("swarm: invalid address %q", addrStr)
	}

	// seen dedupes sessions by (workspace, session) so a workspace
	// that returns the same row twice (unlikely, but a safety net)
	// doesn't inflate the match count into a spurious Ambiguous.
	var matches []SwarmLookupResult
	seen := make(map[string]struct{})
	addMatch := func(m SwarmLookupResult) {
		key := m.WorkspaceID + "\x00" + m.SessionID
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		matches = append(matches, m)
	}
	var firstErr error
	for _, ws := range b.workspaces.Seq2() {
		app := ws.App
		if app == nil {
			continue
		}
		found, err := matchAddressInApp(ctx, app.Sessions, ws.ID, addr)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("swarm: workspace %s lookup: %w", ws.ID, err)
			}
			continue
		}
		for _, m := range found {
			m.WorkspaceRoot = ws.resolvedPath
			addMatch(m)
		}
	}
	switch len(matches) {
	case 0:
		if firstErr != nil {
			return SwarmLookupResult{}, firstErr
		}
		// No match among currently-attached workspaces. Rather than
		// keeping every swarm-touched workspace pinned in memory
		// forever to dodge this, fall back to the on-disk registry:
		// a workspace that idle-teardown released is still fully
		// intact on disk (its DB is untouched), just not currently
		// loaded. Probe each known-but-detached root for a match,
		// and only pay the cost of actually re-attaching (via
		// CreateWorkspace) the one root that has it, if any.
		if reattached, err := b.reattachForAddress(ctx, addr); err == nil {
			return reattached, nil
		}
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	case 1:
		return matches[0], nil
	}
	return SwarmLookupResult{}, ErrSwarmAddressAmbiguous
}

// reattachForAddress searches every registry root that is not
// currently an attached workspace for a session matching addr,
// without paying the cost of fully booting each candidate: it uses
// [db.PeekSessions] (read-only, unlocked, no migrations, no shared
// connection pool — see its docs) to check each detached root's
// on-disk sessions first. Only the root(s) that actually contain a
// match are considered; a full boot (via CreateWorkspace, exactly as
// CreateSwarmSessionAtPath does for a brand-new spawn) is paid for
// only once, for the single root that turns out to be the unique
// match — so a registry with many stale or unrelated entries costs
// one cheap peek each rather than N full workspace boots.
//
// Preserves the same ambiguity contract as the live-workspace loop in
// LookupSwarmAddress: if more than one detached root (or more than
// one session within the same detached root) matches addr, this
// returns ErrSwarmAddressAmbiguous rather than silently picking
// whichever root happened to be scanned first — a color/animal pair
// is drawn from a small hashed palette, so cross-project collisions
// among unrelated, currently-detached projects are expected, not
// exotic, and must not cause a swarm message to be misdelivered to
// the wrong session.
//
// Returns ErrSwarmAddressNotFound if no registry root has a match.
func (b *Backend) reattachForAddress(ctx context.Context, addr swarm.Address) (SwarmLookupResult, error) {
	if b.registry == nil {
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	}
	entries, err := b.registry.List()
	if err != nil {
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	}

	type candidate struct {
		root   string
		peeked db.PeekedSession
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.Root == "" || entry.DataDir == "" {
			continue
		}
		b.mu.Lock()
		_, attached := b.pathIndex[entry.Root]
		b.mu.Unlock()
		if attached {
			// Already searched via the live loop in LookupSwarmAddress.
			continue
		}
		peeked, err := peekDataDirForAddress(ctx, entry.DataDir, addr)
		if err != nil {
			slog.Warn("Failed to probe detached workspace for swarm address", "root", entry.Root, "error", err)
			continue
		}
		for _, s := range peeked {
			candidates = append(candidates, candidate{root: entry.Root, peeked: s})
		}
	}

	switch len(candidates) {
	case 0:
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	case 1:
		// fall through to reattach below
	default:
		return SwarmLookupResult{}, ErrSwarmAddressAmbiguous
	}

	root := candidates[0].root
	ws, _, err := b.CreateWorkspace(proto.Workspace{
		Path:     root,
		ClientID: uuid.New().String(),
	})
	if err != nil {
		return SwarmLookupResult{}, fmt.Errorf("swarm: failed to reattach workspace %s: %w", root, err)
	}
	// Re-verify against the live session service now that the
	// workspace is actually attached: the peek above is a snapshot
	// that could have raced a concurrent change (archive, delete) to
	// the on-disk data between the peek and this reattach.
	found, err := matchAddressInApp(ctx, ws.Sessions, ws.ID, addr)
	if err != nil || len(found) == 0 {
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	}
	found[0].WorkspaceRoot = ws.resolvedPath
	return found[0], nil
}

// peekDataDirForAddress returns the (non-archived, non-sub-session)
// sessions in the detached workspace backed by dataDir that match
// addr, without booting a full workspace or touching the shared,
// refcounted connection pool [db.Connect] uses. [db.PeekSessions]
// opens its own private read-only connection, runs no migrations,
// and takes no lock — critically, unlike a plain db.Connect call, it
// cannot end up silently sharing an unlocked connection with a
// concurrent, properly-locked CreateWorkspace attach against the same
// data directory (db.Connect's pool only decides whether to acquire
// the lock on a cache *miss*; a probe that wins the race to create
// the pool entry would otherwise poison every later locked caller
// for that path). It also filters archived and parent-having (sub-)
// sessions server-side already; the id-shape check here is only a
// defensive backstop for legacy rows with no parent pointer, mirrored
// from isSubSession.
func peekDataDirForAddress(ctx context.Context, dataDir string, addr swarm.Address) ([]db.PeekedSession, error) {
	peeked, err := db.PeekSessions(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	var matches []db.PeekedSession
	for _, s := range peeked {
		if strings.HasPrefix(s.ID, "title-") || strings.Contains(s.ID, "$$") {
			continue
		}
		if addr.SessionID != "" {
			// A precise session-id address may resurrect an archived
			// session (the send path unarchives it), so archived rows
			// are not filtered on this branch.
			if s.ID == addr.SessionID {
				matches = append(matches, s)
			}
			continue
		}
		// Color/animal resolution never resurrects an archived session
		// (a palette collision could otherwise revive the wrong one).
		if s.Archived {
			continue
		}
		if !addr.MatchesColorAnimal(s.Color, s.Animal, s.ID) {
			continue
		}
		matches = append(matches, s)
	}
	return matches, nil
}

// SwarmSendResult is what [Backend.SwarmSend] reports back to the
// caller so the sending tool can tell the LLM whether the message
// landed in an idle session (sent) or was buffered behind an active
// turn (queued).
type SwarmSendResult struct {
	WorkspaceID string
	SessionID   string
	// Delivery is "sent" if the target session was idle when the
	// message arrived, "queued" if it was already busy running a turn.
	// Best-effort — computed from a snapshot of IsSessionBusyOrAccepted
	// (active or just-dispatched) before dispatch.
	Delivery string
}

// SwarmSend delivers a swarm message to a target session in any
// workspace. Enforces the global swarm.enabled config gate.
//
// The caller supplies a SwarmMessage part but SwarmSend re-derives
// the sender identity fields (SenderSessionID, SenderColor,
// SenderAnimal, SenderWorkspaceID) from the trusted senderSessionID
// argument. This prevents a prompt-injected tool caller from
// spoofing the displayed sender identity.
func (b *Backend) SwarmSend(ctx context.Context, senderSessionID string, target SwarmLookupResult, part proto.SwarmMessage) (SwarmSendResult, error) {
	if target.SessionID == "" || target.WorkspaceID == "" {
		return SwarmSendResult{}, ErrSwarmAddressNotFound
	}
	if target.Sub {
		return SwarmSendResult{}, ErrSwarmTargetIsSubagent
	}
	if senderSessionID != "" && target.SessionID == senderSessionID {
		return SwarmSendResult{}, ErrSwarmSelfAddressed
	}
	ws, ok := b.workspaces.Get(target.WorkspaceID)
	if !ok {
		// The target workspace was idle-torn-down between address
		// resolution and now. If we know its root, bring it back up
		// (attaching the detached-but-intact workspace) so a
		// cross-workspace send doesn't spuriously fail on this race.
		if target.WorkspaceRoot != "" {
			reattached, _, err := b.CreateWorkspace(proto.Workspace{
				Path:     target.WorkspaceRoot,
				ClientID: uuid.New().String(),
			})
			if err == nil && reattached != nil {
				ws, target.WorkspaceID = reattached, reattached.ID
				ok = true
			}
		}
		if !ok {
			return SwarmSendResult{}, ErrSwarmWorkspaceNotFound
		}
	}
	// Locate the sender session so we can stamp the outgoing part
	// with a trusted identity rather than the caller-supplied values.
	// Not fatal if the sender's session lives in a different
	// workspace than we can reach — fall back to the caller's fields
	// but log so this is auditable.
	trustedSender := lookupSenderIdentity(ctx, b, senderSessionID)
	if trustedSender.SessionID != "" {
		part.SenderSessionID = trustedSender.SessionID
		part.SenderColor = trustedSender.Color
		part.SenderAnimal = trustedSender.Animal
		part.SenderWorkspaceID = trustedSender.WorkspaceID
	}

	// Confirm the target session still exists in that workspace,
	// and cache its title for the outgoing notification below. An
	// archived target is resurrected (unarchived) so a swarm message
	// can revive a dormant conversation.
	targetSess, err := resurrectTargetSession(ctx, ws.Sessions, target.SessionID)
	if err != nil {
		return SwarmSendResult{}, err
	}

	delivery := "sent"
	if ws.AgentCoordinator != nil && ws.AgentCoordinator.IsSessionBusyOrAccepted(target.SessionID) {
		delivery = "queued"
	}

	msg := proto.AgentMessage{
		SessionID: target.SessionID,
		Prompt:    part.Text,
		// SwarmParts, when non-empty, replaces the default
		// TextContent user message with structured swarm parts.
		SwarmParts: []proto.SwarmMessage{part},
		// A btw aside is a steer: fold into the target's active turn
		// and wake its long-running tools so it lands sooner.
		Steer: part.BTW,
	}
	if b.Draining() {
		// The server is draining for an update and will not start a
		// new turn. Rather than fail the sender's tool call, journal
		// the message onto the target's persisted queue so the next
		// server delivers it when it rehydrates the workspace.
		if err := b.deferSwarmSend(ctx, ws, msg); err != nil {
			return SwarmSendResult{}, err
		}
		return SwarmSendResult{
			WorkspaceID: target.WorkspaceID,
			SessionID:   target.SessionID,
			Delivery:    "deferred",
		}, nil
	}
	if err := b.SendMessage(target.WorkspaceID, msg); err != nil {
		if !errors.Is(err, ErrDraining) {
			return SwarmSendResult{}, err
		}
		// A drain landed between the check above and the send: defer
		// rather than fail the sender's tool call.
		if derr := b.deferSwarmSend(ctx, ws, msg); derr != nil {
			return SwarmSendResult{}, derr
		}
		return SwarmSendResult{
			WorkspaceID: target.WorkspaceID,
			SessionID:   target.SessionID,
			Delivery:    "deferred",
		}, nil
	}
	publishSwarmReceived(ws, target.SessionID, targetSess.Title, part)

	// Swarm squelch on the sender's workspace: a swarm message fired via
	// a tool call. Best-effort; defers to a Swarm hook when configured.
	if senderWS, ok := b.workspaces.Get(part.SenderWorkspaceID); ok {
		b.playSound(senderWS, sound.Swarm)
	}
	// Queued bump on the target when the message landed behind an active
	// turn rather than starting immediately.
	if delivery == "queued" {
		b.playSound(ws, sound.Queued)
	}

	return SwarmSendResult{
		WorkspaceID: target.WorkspaceID,
		SessionID:   target.SessionID,
		Delivery:    delivery,
	}, nil
}

// deferSwarmSend appends msg to the target session's journaled queue
// without dispatching it. Used while draining: the entry is replayed by
// the next server's rehydrateQueue. Best-effort against a concurrent
// journal write by the target's own (finishing) turn: the queue is tiny
// and the window is the drain's final moments.
func (b *Backend) deferSwarmSend(ctx context.Context, ws *Workspace, msg proto.AgentMessage) error {
	if ws.App == nil || ws.Journal == nil {
		return ErrDraining
	}
	attachments := proto.AttachmentsToMessage(msg.Attachments)
	parts := make([]message.SwarmMessage, 0, len(msg.SwarmParts))
	for _, p := range msg.SwarmParts {
		parts = append(parts, message.SwarmMessage{
			Text:              p.Text,
			Body:              p.Body,
			SenderSessionID:   p.SenderSessionID,
			SenderColor:       p.SenderColor,
			SenderAnimal:      p.SenderAnimal,
			SenderWorkspaceID: p.SenderWorkspaceID,
			BTW:               p.BTW,
			RequireReply:      p.RequireReply,
		})
	}
	// Prefer the coordinator's own queue: the append is serialized on
	// the target session's dispatch mutex and journaled by the normal
	// write-through, so it cannot race the target's finishing turn. The
	// entry MUST carry a run id: drainQueueForStep folds id-less queued
	// calls into a still-streaming turn on this (old) server, which is
	// the opposite of deferring. Dispatch of id-bearing calls is paused
	// while draining, so with an id it stays put for the next server.
	if d, ok := ws.AgentCoordinator.(agent.Drainable); ok {
		d.DeferPrompt(msg.SessionID, newRunID(), msg.Prompt, attachments, parts)
		slog.Info("Swarm message deferred until after server update", "session_id", msg.SessionID)
		return nil
	}
	// Coordinators without drain support: write the journal directly.
	queues, err := ws.Journal.LoadQueue(ctx)
	if err != nil {
		return fmt.Errorf("defer swarm send: %w", err)
	}
	entries := append(queues[msg.SessionID], journal.QueuedPrompt{
		SessionID:   msg.SessionID,
		RunID:       newRunID(),
		Prompt:      msg.Prompt,
		Attachments: attachments,
		SwarmParts:  parts,
	})
	if err := ws.Journal.SaveQueue(ctx, msg.SessionID, entries); err != nil {
		return fmt.Errorf("defer swarm send: %w", err)
	}
	slog.Info("Swarm message deferred until after server update", "session_id", msg.SessionID, "queued", len(entries))
	return nil
}

// resurrectTargetSession fetches a swarm target by id, unarchiving it
// first if it was archived so a swarm message can revive a dormant
// conversation. sql.ErrNoRows maps to ErrSwarmAddressNotFound; genuine
// DB failures are distinguished (so the tool doesn't report a real
// error as NotFound). Returns the live (unarchived) session.
func resurrectTargetSession(ctx context.Context, sessions session.Service, sessionID string) (session.Session, error) {
	s, err := sessions.Get(ctx, sessionID)
	switch {
	case err == nil:
		if s.ArchivedAt != 0 {
			if err := sessions.Unarchive(ctx, sessionID); err != nil {
				return session.Session{}, fmt.Errorf("swarm: unarchive target: %w", err)
			}
			s.ArchivedAt = 0
		}
		return s, nil
	case errors.Is(err, sql.ErrNoRows):
		return session.Session{}, ErrSwarmAddressNotFound
	default:
		return session.Session{}, fmt.Errorf("swarm: target lookup: %w", err)
	}
}

// publishSwarmReceived emits a best-effort incoming-swarm notification
// on the target workspace's broker so unfocused clients can surface a
// "message from <sender>" toast without loading the session. The label
// uses the shorthash-qualified sender form so ambiguous color-animal
// pairs still identify the sender uniquely. No-op if the workspace has
// no notifier.
func publishSwarmReceived(ws *Workspace, targetSessionID, targetTitle string, part proto.SwarmMessage) {
	notifier := ws.AgentNotifications()
	if notifier == nil {
		return
	}
	senderAddr := swarm.FormatAddress(
		swarm.Identity{Color: part.SenderColor, Animal: part.SenderAnimal},
		part.SenderSessionID,
	)
	notifier.Publish(pubsub.CreatedEvent, notify.Notification{
		SessionID:    targetSessionID,
		SessionTitle: targetTitle,
		Type:         notify.TypeSwarmReceived,
		Message:      "message from " + senderAddr + ": " + part.Body,
	})
}

// senderIdentity is the trusted view of the sender's session that
// [SwarmSend] stamps onto outgoing parts. Zero value means "unknown".
type senderIdentity struct {
	WorkspaceID string
	SessionID   string
	Color       string
	Animal      string
}

// lookupSenderIdentity walks the workspace map searching for the
// session that owns senderSessionID, returning its persisted
// color/animal. Empty result means the sender wasn't found — the
// caller falls back to the tool-supplied fields but should treat
// them as untrusted.
func lookupSenderIdentity(ctx context.Context, b *Backend, senderSessionID string) senderIdentity {
	if senderSessionID == "" {
		return senderIdentity{}
	}
	for _, ws := range b.workspaces.Seq2() {
		if ws.App == nil {
			continue
		}
		s, err := ws.Sessions.Get(ctx, senderSessionID)
		if err != nil {
			continue
		}
		return senderIdentity{
			WorkspaceID: ws.ID,
			SessionID:   s.ID,
			Color:       s.Color,
			Animal:      s.Animal,
		}
	}
	return senderIdentity{}
}

// SwarmSpawnOptions describes the session [Backend.CreateSwarmSession]
// creates on behalf of a `swarm new` call.
type SwarmSpawnOptions struct {
	Title string
	// ModelRef, when non-empty, is the model reference the worker runs
	// on (see [session.Session.ModelRef]); it is validated against the
	// target workspace's config. Empty runs the workspace's large model.
	ModelRef string
	// SpawnedBySessionID and SpawnedByWorkspaceID record the spawner as
	// lineage on the new session. When the spawner session can be
	// located in a running workspace its identity is re-derived from
	// the session row (mirroring SwarmSend's trusted-sender rule) so a
	// caller cannot record a forged lineage.
	SpawnedBySessionID   string
	SpawnedByWorkspaceID string
	// WorkingDir pins the directory the new session's tools run in. It
	// must resolve (Abs + EvalSymlinks + project root) to the target
	// workspace; otherwise creation fails with ErrSwarmWorkingDirOutside.
	WorkingDir string
}

// CreateSwarmSession spins up a new session in an existing workspace
// so the caller can send an initial-prompt swarm message to it.
// Fails if the workspace does not exist or swarm is disabled. On
// failure to assign identity, the freshly-created session is
// archived so callers who retry don't accumulate ghost sessions.
//
// Lineage and working dir are stamped in the same insert as creation
// so the Created event already carries them.
func (b *Backend) CreateSwarmSession(ctx context.Context, workspaceID string, opts SwarmSpawnOptions) (session.Session, error) {
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return session.Session{}, ErrSwarmWorkspaceNotFound
	}
	// Validate the model reference against the TARGET workspace's config
	// (that is where it will be resolved on every turn) before creating
	// anything, so a bad reference never leaves an orphan session. An
	// empty ref means the worker runs the workspace's large model, the
	// historical default.
	modelRef := strings.TrimSpace(opts.ModelRef)
	if modelRef != "" {
		if ws.Cfg == nil {
			return session.Session{}, fmt.Errorf("%w: workspace has no config", ErrInvalidSessionModel)
		}
		if _, err := ws.Cfg.Config().ResolveModelRef(modelRef); err != nil {
			return session.Session{}, fmt.Errorf("%w: %v", ErrInvalidSessionModel, err)
		}
	}
	workingDir, err := b.resolveSwarmWorkingDir(ws, opts.WorkingDir)
	if err != nil {
		return session.Session{}, err
	}
	spawner := b.trustedSpawner(ctx, opts)
	sess, err := ws.Sessions.CreateWithOptions(ctx, opts.Title, session.CreateOptions{
		ModelRef:             modelRef,
		SpawnedBySessionID:   spawner.SessionID,
		SpawnedByWorkspaceID: spawner.WorkspaceID,
		WorkingDir:           workingDir,
	})
	if err != nil {
		return session.Session{}, err
	}
	// EnsureSwarmIdentity is idempotent and covers the case where the
	// pubsub subscriber hasn't consumed the create event yet.
	filled, err := ws.EnsureSwarmIdentity(ctx, sess)
	if err != nil {
		if archiveErr := ws.Sessions.Archive(context.Background(), sess.ID); archiveErr != nil {
			slog.Warn("Failed to archive orphaned swarm session after identity failure",
				"session_id", sess.ID, "error", archiveErr)
		}
		return session.Session{}, err
	}
	return filled, nil
}

// trustedSpawner returns the lineage to record for a spawn. When the
// claimed spawner session is found in a running workspace, its real
// workspace id wins over the caller-supplied one (the same rule
// [Backend.SwarmSend] applies to sender identity). When it cannot be
// found — the spawner's workspace was torn down mid-call — the claimed
// values are kept so lineage is not silently dropped.
func (b *Backend) trustedSpawner(ctx context.Context, opts SwarmSpawnOptions) senderIdentity {
	claimed := senderIdentity{
		SessionID:   opts.SpawnedBySessionID,
		WorkspaceID: opts.SpawnedByWorkspaceID,
	}
	if claimed.SessionID == "" {
		return senderIdentity{}
	}
	if found := lookupSenderIdentity(ctx, b, claimed.SessionID); found.SessionID != "" {
		return found
	}
	return claimed
}

// resolveSwarmWorkingDir validates and canonicalizes the working dir a
// spawned session is pinned to. Empty means "unpinned" and is returned
// as-is. Otherwise the directory must exist and resolve (absolute,
// symlinks evaluated, collapsed to its git project root) to the same
// key the target workspace is registered under, so a sibling git
// worktree of the project is accepted while an unrelated directory is
// refused.
func (b *Backend) resolveSwarmWorkingDir(ws *Workspace, dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	abs, err := filepath.Abs(home.Expand(dir))
	if err != nil {
		return "", fmt.Errorf("swarm: working_dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("swarm: working_dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("swarm: working_dir %s is not a directory", dir)
	}
	key, err := resolveWorkspaceKey(abs)
	if err != nil {
		return "", fmt.Errorf("swarm: working_dir: %w", err)
	}
	if key != ws.resolvedPath {
		return "", fmt.Errorf("%w: %s resolves to project %s, not %s", ErrSwarmWorkingDirOutside, dir, key, ws.resolvedPath)
	}
	return abs, nil
}

// CreateSwarmSessionAtPath ensures a workspace is running for the
// given directory path — reusing the already-running workspace when
// one exists, otherwise bringing a new one up (creating it on disk or
// attaching a previously detached one) — and then creates a swarm
// session in it. This lets a session spawn a session in a workspace
// that is not currently attached/running.
//
// A synthetic client UUID is minted to satisfy CreateWorkspace's
// creation-hold contract; the hold is released by the grace timer
// since no SSE stream will ever attach on its behalf.
//
// Returns the resolved workspace id alongside the identity-filled
// session. The swarm-enabled gate is enforced via CreateSwarmSession.
//
// The session's working dir defaults to path when opts.WorkingDir is
// empty. This matters when the workspace is already running: sibling
// git worktrees collapse to one workspace whose effectiveWorkingDir is
// whichever client attached first, and a swarm-driven turn carries no
// client cwd, so without the pin the worker's tools would run in the
// wrong tree.
func (b *Backend) CreateSwarmSessionAtPath(ctx context.Context, path string, opts SwarmSpawnOptions) (string, session.Session, error) {
	if strings.TrimSpace(path) == "" {
		return "", session.Session{}, ErrPathRequired
	}
	if opts.WorkingDir == "" {
		opts.WorkingDir = path
	}

	// Fast path: a workspace is already running for this path.
	key, err := resolveWorkspaceKey(path)
	if err != nil {
		return "", session.Session{}, fmt.Errorf("swarm: failed to resolve workspace path: %w", err)
	}
	b.mu.Lock()
	existingID, ok := b.pathIndex[key]
	b.mu.Unlock()
	if ok {
		if _, found := b.workspaces.Get(existingID); found {
			sess, err := b.CreateSwarmSession(ctx, existingID, opts)
			if err != nil {
				return "", session.Session{}, err
			}
			return existingID, sess, nil
		}
	}

	// Bring a workspace up for this path. CreateWorkspace is
	// first-wins by resolved path, so a concurrent caller that just
	// created the same workspace is deduplicated to the same id.
	ws, _, err := b.CreateWorkspace(proto.Workspace{
		Path:     path,
		ClientID: uuid.New().String(),
	})
	if err != nil {
		return "", session.Session{}, fmt.Errorf("swarm: failed to bring up workspace: %w", err)
	}
	sess, err := b.CreateSwarmSession(ctx, ws.ID, opts)
	if err != nil {
		return "", session.Session{}, err
	}
	return ws.ID, sess, nil
}

// RenameWorkspaceSession updates the title of a session in the target
// workspace, which may differ from the caller's. It is used by the
// cross-workspace rename_session tool via [tools.SwarmBackend]. If the
// target workspace was idle-torn-down between address resolution and
// now, it is brought back up from target.WorkspaceRoot (mirroring
// [Backend.SwarmSend]) so a cross-workspace rename does not spuriously
// fail on that race.
func (b *Backend) RenameWorkspaceSession(ctx context.Context, target SwarmLookupResult, title string) error {
	if target.SessionID == "" || target.WorkspaceID == "" {
		return ErrSwarmAddressNotFound
	}
	if target.Sub {
		return ErrSwarmTargetIsSubagent
	}
	ws, ok := b.workspaces.Get(target.WorkspaceID)
	if !ok {
		if target.WorkspaceRoot != "" {
			reattached, _, err := b.CreateWorkspace(proto.Workspace{
				Path:     target.WorkspaceRoot,
				ClientID: uuid.New().String(),
			})
			if err == nil && reattached != nil {
				ws, ok = reattached, true
			}
		}
		if !ok {
			return ErrSwarmWorkspaceNotFound
		}
	}
	return ws.Sessions.Rename(ctx, target.SessionID, title)
}

// ArchiveWorkspaceSession archives a session in the given workspace.
// Used by [tools.SwarmBackend] callers to clean up ghost sessions
// created by [CreateSwarmSession] when the follow-up Send fails; the
// caller cannot use [Backend.ArchiveSession] directly because that
// would require importing backend from agent/tools.
func (b *Backend) ArchiveWorkspaceSession(ctx context.Context, workspaceID, sessionID string) error {
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return ErrSwarmWorkspaceNotFound
	}
	return ws.Sessions.Archive(ctx, sessionID)
}

// matchAddressInApp returns the non-archived sessions in a single
// workspace's session service that match addr, tagged with
// workspaceID. It is the one place the session-id and color/animal
// lookup rules live, shared by the live-workspace loop in
// LookupSwarmAddress and the post-attach re-verification in
// reattachForAddress so the two can never drift.
//
// A raw-session-id address yields at most one result (flagged Sub if
// it is a sub-session, so callers can report it). sql.ErrNoRows on
// that path means the session lives in another workspace, not a
// failure, so it returns (nil, nil). A color/animal address yields
// every non-sub session that matches (sub-sessions are skipped so a
// legacy collision can't disambiguate).
func matchAddressInApp(ctx context.Context, sessions session.Service, workspaceID string, addr swarm.Address) ([]SwarmLookupResult, error) {
	if addr.SessionID != "" {
		s, err := sessions.Get(ctx, addr.SessionID)
		switch {
		case err == nil:
			// An archived session is still a valid target when
			// addressed by its precise, unambiguous session id: the
			// send path unarchives it (resurrects it). Color/animal
			// resolution below deliberately does NOT resurrect, to
			// avoid reviving the wrong session on a palette collision.
			return []SwarmLookupResult{{
				WorkspaceID: workspaceID,
				SessionID:   s.ID,
				Color:       s.Color,
				Animal:      s.Animal,
				Sub:         isSubSession(s),
			}}, nil
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil
		default:
			return nil, err
		}
	}
	list, err := sessions.FindByColorAnimal(ctx, addr.Color, addr.Animal)
	if err != nil {
		return nil, err
	}
	var out []SwarmLookupResult
	for _, s := range list {
		if isSubSession(s) {
			continue
		}
		if !addr.MatchesColorAnimal(s.Color, s.Animal, s.ID) {
			continue
		}
		out = append(out, SwarmLookupResult{
			WorkspaceID: workspaceID,
			SessionID:   s.ID,
			Color:       s.Color,
			Animal:      s.Animal,
		})
	}
	return out, nil
}

// isSubSession recognizes the internal sub-agent session id formats
// so LookupSwarmAddress can flag them. Callers use this to refuse to
// route user-visible swarm traffic to title/summary/task children.
// A non-empty ParentSessionID is the primary signal; the id-shape
// checks below are defensive for any legacy row where the parent
// pointer was never persisted.
func isSubSession(s session.Session) bool {
	if s.ParentSessionID != "" {
		return true
	}
	if strings.HasPrefix(s.ID, "title-") {
		return true
	}
	// Agent-tool sessions use the "$$"-delimited compound id.
	if strings.Contains(s.ID, "$$") {
		return true
	}
	return false
}
