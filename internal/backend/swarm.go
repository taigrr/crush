package backend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// Swarm errors.
var (
	ErrSwarmDisabled          = errors.New("swarm is disabled")
	ErrSwarmAddressAmbiguous  = errors.New("swarm address matches multiple sessions")
	ErrSwarmAddressNotFound   = errors.New("swarm address does not match any session")
	ErrSwarmTargetIsSubagent  = errors.New("swarm target is a sub-agent session (not addressable)")
	ErrSwarmSelfAddressed     = errors.New("swarm target is the sender's own session")
	ErrSwarmWorkspaceNotFound = errors.New("swarm target workspace not found")
)

// swarmEnabled reports whether the given workspace has swarm turned
// on. The gate lives at the workspace layer (not the backend's
// server-wide config) because each workspace has its own
// ConfigStore, and users typically opt into swarm via a project's
// crush.json. Defensively handles nil Options / Swarm.
func workspaceSwarmEnabled(ws *Workspace) bool {
	if ws == nil || ws.App == nil {
		return false
	}
	cfg := ws.Cfg.Config()
	if cfg == nil {
		return false
	}
	return cfg.Options.SwarmEnabled()
}

// SwarmLookupResult describes a resolved swarm address across all
// running workspaces. Callers hold the returned WorkspaceID/SessionID
// as an opaque pair for [Backend.SwarmSend].
type SwarmLookupResult struct {
	WorkspaceID string
	SessionID   string
	Color       string
	Animal      string
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
		if addr.SessionID != "" {
			s, err := app.Sessions.Get(ctx, addr.SessionID)
			switch {
			case err == nil:
				if s.ArchivedAt == 0 {
					addMatch(SwarmLookupResult{
						WorkspaceID: ws.ID,
						SessionID:   s.ID,
						Color:       s.Color,
						Animal:      s.Animal,
						Sub:         isSubSession(s),
					})
				}
			case errors.Is(err, sql.ErrNoRows):
				// Session belongs to another workspace; not an error.
			default:
				if firstErr == nil {
					firstErr = fmt.Errorf("swarm: workspace %s lookup: %w", ws.ID, err)
				}
			}
			continue
		}
		// Color/animal path. Filter out sub-sessions here so a
		// legacy title/summary/task-tool row that happens to share a
		// color/animal with a real session never disambiguates by
		// collision.
		list, err := app.Sessions.FindByColorAnimal(ctx, addr.Color, addr.Animal)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("swarm: workspace %s lookup: %w", ws.ID, err)
			}
			continue
		}
		for _, s := range list {
			if isSubSession(s) {
				continue
			}
			if addr.ShortHash != "" && !strings.EqualFold(swarm.ShortHash(s.ID), addr.ShortHash) {
				continue
			}
			addMatch(SwarmLookupResult{
				WorkspaceID: ws.ID,
				SessionID:   s.ID,
				Color:       s.Color,
				Animal:      s.Animal,
			})
		}
	}
	switch len(matches) {
	case 0:
		if firstErr != nil {
			return SwarmLookupResult{}, firstErr
		}
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	case 1:
		return matches[0], nil
	}
	return SwarmLookupResult{}, ErrSwarmAddressAmbiguous
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
	// Best-effort — computed from a snapshot of IsSessionBusy before
	// dispatch.
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
		return SwarmSendResult{}, ErrSwarmWorkspaceNotFound
	}
	// Gate on the target workspace's config: each workspace owns
	// its own ConfigStore, and the swarm tool is registered based
	// on the sender workspace's opt-in. Refuse to deliver to a
	// workspace that hasn't opted in itself.
	if !workspaceSwarmEnabled(ws) {
		return SwarmSendResult{}, ErrSwarmDisabled
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
	// and cache its title for the outgoing notification below.
	// Distinguish "no such session" from other DB failures so the
	// tool doesn't report a real DB error as NotFound.
	targetSess, err := ws.App.Sessions.Get(ctx, target.SessionID)
	switch {
	case err == nil:
		if targetSess.ArchivedAt != 0 {
			return SwarmSendResult{}, ErrSwarmAddressNotFound
		}
	case errors.Is(err, sql.ErrNoRows):
		return SwarmSendResult{}, ErrSwarmAddressNotFound
	default:
		return SwarmSendResult{}, fmt.Errorf("swarm: target lookup: %w", err)
	}

	delivery := "sent"
	if ws.AgentCoordinator != nil && ws.AgentCoordinator.IsSessionBusy(target.SessionID) {
		delivery = "queued"
	}

	msg := proto.AgentMessage{
		SessionID: target.SessionID,
		Prompt:    part.Text,
		// SwarmParts, when non-empty, replaces the default
		// TextContent user message with structured swarm parts.
		SwarmParts: []proto.SwarmMessage{part},
	}
	if err := b.SendMessage(target.WorkspaceID, msg); err != nil {
		return SwarmSendResult{}, err
	}
	// Publish an incoming-swarm notification on the target
	// workspace's notification broker so unfocused clients can
	// surface a "message from <sender>" toast without loading the
	// session. Best-effort; a full subscriber is dropped. The label
	// uses the shorthash-qualified form so ambiguous color-animal
	// pairs still identify the sender uniquely.
	if notifier := ws.AgentNotifications(); notifier != nil {
		senderAddr := swarm.FormatAddress(
			swarm.Identity{Color: part.SenderColor, Animal: part.SenderAnimal},
			part.SenderSessionID,
		)
		notifier.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID:    target.SessionID,
			SessionTitle: targetSess.Title,
			Type:         notify.TypeSwarmReceived,
			Message:      "message from " + senderAddr + ": " + part.Body,
		})
	}
	return SwarmSendResult{
		WorkspaceID: target.WorkspaceID,
		SessionID:   target.SessionID,
		Delivery:    delivery,
	}, nil
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
		s, err := ws.App.Sessions.Get(ctx, senderSessionID)
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

// CreateSwarmSession spins up a new session in an existing workspace
// so the caller can send an initial-prompt swarm message to it.
// Fails if the workspace does not exist or swarm is disabled. On
// failure to assign identity, the freshly-created session is
// archived so callers who retry don't accumulate ghost sessions.
func (b *Backend) CreateSwarmSession(ctx context.Context, workspaceID, title string) (session.Session, error) {
	ws, ok := b.workspaces.Get(workspaceID)
	if !ok {
		return session.Session{}, ErrSwarmWorkspaceNotFound
	}
	if !workspaceSwarmEnabled(ws) {
		return session.Session{}, ErrSwarmDisabled
	}
	sess, err := ws.App.Sessions.Create(ctx, title)
	if err != nil {
		return session.Session{}, err
	}
	// EnsureSwarmIdentity is idempotent and covers the case where the
	// pubsub subscriber hasn't consumed the create event yet.
	filled, err := ws.App.EnsureSwarmIdentity(ctx, sess)
	if err != nil {
		if archiveErr := ws.App.Sessions.Archive(context.Background(), sess.ID); archiveErr != nil {
			slog.Warn("Failed to archive orphaned swarm session after identity failure",
				"session_id", sess.ID, "error", archiveErr)
		}
		return session.Session{}, err
	}
	return filled, nil
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
	return ws.App.Sessions.Archive(ctx, sessionID)
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
