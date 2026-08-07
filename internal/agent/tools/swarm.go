package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// SwarmToolName is the tool identifier exposed to the model.
const SwarmToolName = "swarm"

//go:embed swarm.md
var swarmToolDescription string

// SwarmBackend is the minimal contract the swarm tool needs from the
// backend. It is deliberately narrow so tests can supply a stub
// without pulling in the full [backend.Backend]; production wiring
// passes a shim that just delegates to the running backend.
type SwarmBackend interface {
	// LookupAddress resolves an address string ("color-animal" /
	// "color-animal-hash" / raw session id) to a single session
	// across all running workspaces. Returns the workspace id, the
	// session id, and whether the session is a non-addressable
	// sub-agent.
	LookupAddress(ctx context.Context, addr string) (SwarmLookupResult, error)
	// Send delivers a message part to a target session. delivery
	// reports whether the target was idle ("sent") or busy
	// ("queued").
	Send(ctx context.Context, senderSessionID string, target SwarmLookupResult, part message.SwarmMessage) (delivery string, err error)
	// CreateSessionInWorkspace creates a new session in an existing
	// workspace and returns it (with color/animal assigned). Fails
	// if the workspace is not currently running.
	CreateSessionInWorkspace(ctx context.Context, workspaceID, title string) (session.Session, error)
	// ArchiveSessionInWorkspace archives a session that was created
	// via CreateSessionInWorkspace but that we then failed to send
	// the initial message to. Best-effort compensating cleanup so
	// callers don't leak orphaned empty sessions on retry.
	ArchiveSessionInWorkspace(ctx context.Context, workspaceID, sessionID string) error
}

// SwarmLookupResult mirrors backend.SwarmLookupResult so the tool
// package does not import backend directly (cycle avoidance).
type SwarmLookupResult struct {
	WorkspaceID string
	SessionID   string
	Color       string
	Animal      string
	Sub         bool
}

// SwarmParams is the JSON schema exposed to the model.
type SwarmParams struct {
	// Address is the target: "color-animal", "color-animal-shorthash",
	// a raw session id, or the literal "new" to create a new session
	// in WorkspaceID.
	Address string `json:"address" description:"Target session address: color-animal, color-animal-shorthash, raw session id, or 'new' (with workspace_id)"`
	// Prompt is the message body. It is prefixed with "message from
	// <sender>:" before delivery.
	Prompt string `json:"prompt" description:"The message body to send. Prefixed automatically with the sender's color-animal identity."`
	// Mode is "queue" (default) or "btw". "btw" folds into the
	// target's current turn without waiting for it to end.
	Mode string `json:"mode,omitempty" description:"Delivery mode: 'queue' (default, next-turn) or 'btw' (fold into current turn like /btw)"`
	// WorkspaceID is optional when Address == "new"; defaults to
	// the sender's own workspace.
	WorkspaceID string `json:"workspace_id,omitempty" description:"With address='new': the workspace id to create the session in. Optional; defaults to the sender's own workspace."`
	// Title is optional when Address == "new"; defaults to a short
	// synthesized title derived from the prompt.
	Title string `json:"title,omitempty" description:"Optional title for the new session when address='new'"`
}

// NewSwarmTool builds the fantasy tool wrapper. sessions is the
// current workspace's session service, used to look up the sender's
// identity (so the tool can stamp "message from <color-animal>:" onto
// the outgoing message and refuse self-addressed sends).
func NewSwarmTool(be SwarmBackend, sessions session.Service, swarmCfg func() swarm.Config, senderWorkspaceID string) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		SwarmToolName,
		swarmToolDescription,
		func(ctx context.Context, params SwarmParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return runSwarm(ctx, be, sessions, swarmCfg, senderWorkspaceID, params)
		},
	)
}

func runSwarm(
	ctx context.Context,
	be SwarmBackend,
	sessions session.Service,
	swarmCfg func() swarm.Config,
	senderWorkspaceID string,
	params SwarmParams,
) (fantasy.ToolResponse, error) {
	if strings.TrimSpace(params.Prompt) == "" {
		return fantasy.NewTextErrorResponse("swarm: prompt is required"), nil
	}
	address := strings.TrimSpace(params.Address)
	if address == "" {
		return fantasy.NewTextErrorResponse("swarm: address is required"), nil
	}

	// Resolve the sender's identity so we can attach it to the
	// outgoing message. Falls back to computing from cfg when the
	// session row is missing color/animal (legacy or race with
	// backfill).
	senderID := GetSessionFromContext(ctx)
	if senderID == "" {
		return fantasy.NewTextErrorResponse("swarm: sender session id missing from context"), nil
	}
	senderSess, err := sessions.Get(ctx, senderID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: failed to load sender session: %s", err)), nil
	}
	senderIdent := swarm.Identity{Color: senderSess.Color, Animal: senderSess.Animal}
	if senderIdent.Color == "" || senderIdent.Animal == "" {
		cfg := swarmCfg()
		senderIdent = swarm.Assign(senderSess.ID, cfg)
	}
	senderAddress := swarm.FormatAddress(senderIdent, senderSess.ID)

	// Fast-fail self-address before doing any cross-workspace lookup.
	// Compare against every canonical form the sender could plausibly
	// have typed.
	if isSelfAddress(address, senderID, senderIdent) {
		return fantasy.NewTextErrorResponse("swarm: cannot address your own session"), nil
	}

	mode := strings.ToLower(strings.TrimSpace(params.Mode))
	switch mode {
	case "", "queue":
		mode = "queue"
	case "btw":
		// ok
	default:
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: unknown mode %q (want 'queue' or 'btw')", params.Mode)), nil
	}

	// "new" path: create a fresh session in the given workspace and
	// treat the prompt as its initial user message.
	if strings.EqualFold(address, "new") {
		workspaceID := params.WorkspaceID
		if workspaceID == "" {
			// Default to the sender's own workspace when the model
			// doesn't supply one explicitly. Workspace ids are
			// backend-internal handles that aren't easily
			// discoverable from a session, so requiring an explicit
			// id every time is a bad UX; same-workspace is the
			// overwhelmingly common case.
			workspaceID = senderWorkspaceID
		}
		if workspaceID == "" {
			return fantasy.NewTextErrorResponse("swarm: address='new' requires workspace_id (sender workspace id unavailable)"), nil
		}
		title := strings.TrimSpace(params.Title)
		if title == "" {
			title = firstLine(params.Prompt, 60)
		}
		newSess, err := be.CreateSessionInWorkspace(ctx, workspaceID, title)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return fantasy.ToolResponse{}, err
			}
			return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: failed to create session: %s", err)), nil
		}
		target := SwarmLookupResult{
			WorkspaceID: workspaceID,
			SessionID:   newSess.ID,
			Color:       newSess.Color,
			Animal:      newSess.Animal,
		}
		part := buildSwarmPart(senderSess.ID, senderWorkspaceID, senderIdent, params.Prompt, mode == "btw")
		delivery, sendErr := be.Send(ctx, senderID, target, part)
		if sendErr != nil {
			if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) {
				return fantasy.ToolResponse{}, sendErr
			}
			// Compensating cleanup: archive the empty session we
			// just created so retries don't leak ghosts. Failures
			// here are best-effort; the outer error is what the
			// LLM sees.
			_ = be.ArchiveSessionInWorkspace(context.Background(), workspaceID, newSess.ID)
			return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: failed to send initial message to new session (session archived): %s", sendErr)), nil
		}
		return fantasy.NewTextResponse(fmt.Sprintf(
			"Created and %s to %s (workspace=%s, session=%s).",
			delivery,
			swarm.FormatAddress(swarm.Identity{Color: newSess.Color, Animal: newSess.Animal}, newSess.ID),
			workspaceID, newSess.ID,
		)), nil
	}

	// Standard resolve path.
	target, err := be.LookupAddress(ctx, address)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: %s", err)), nil
	}
	if target.SessionID == senderID {
		return fantasy.NewTextErrorResponse("swarm: cannot address your own session"), nil
	}
	if target.Sub {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"swarm: %s is a sub-agent session and cannot receive swarm messages", address,
		)), nil
	}

	part := buildSwarmPart(senderSess.ID, senderWorkspaceID, senderIdent, params.Prompt, mode == "btw")
	delivery, sendErr := be.Send(ctx, senderID, target, part)
	if sendErr != nil {
		if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) {
			return fantasy.ToolResponse{}, sendErr
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: %s", sendErr)), nil
	}
	return fantasy.NewTextResponse(fmt.Sprintf(
		"%s: %s (from %s to %s)",
		delivery, params.Prompt, senderAddress,
		swarm.FormatAddress(swarm.Identity{Color: target.Color, Animal: target.Animal}, target.SessionID),
	)), nil
}

// buildSwarmPart constructs the proto.SwarmMessage that will be stored
// on the receiving session's transcript. The Text field is the exact
// prefixed body the LLM will read; Body preserves the original prompt
// for programmatic consumers.
func buildSwarmPart(senderSessionID, senderWorkspaceID string, sender swarm.Identity, prompt string, btw bool) message.SwarmMessage {
	prefix := fmt.Sprintf("message from %s: ", sender.String())
	if btw {
		prefix = "[btw] " + prefix
	}
	return message.SwarmMessage{
		Text:              prefix + prompt,
		Body:              prompt,
		SenderSessionID:   senderSessionID,
		SenderColor:       sender.Color,
		SenderAnimal:      sender.Animal,
		SenderWorkspaceID: senderWorkspaceID,
		BTW:               btw,
	}
}

// isSelfAddress reports whether the given address plausibly refers
// to the sender's own session. Compares against the raw session id
// and the sender's color-animal[-shorthash] canonical forms so a
// self-address short-circuits before any cross-workspace lookup.
func isSelfAddress(address, senderID string, senderIdent swarm.Identity) bool {
	lower := strings.ToLower(strings.TrimSpace(address))
	if lower == strings.ToLower(senderID) {
		return true
	}
	if senderIdent.Color == "" || senderIdent.Animal == "" {
		return false
	}
	if lower == senderIdent.String() {
		return true
	}
	if lower == swarm.FormatAddress(senderIdent, senderID) {
		return true
	}
	return false
}

// firstLine returns up to maxRunes runes of the first non-empty line
// of s, used to synthesize a default title for `swarm new` sessions.
func firstLine(s string, maxRunes int) string {
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > maxRunes {
			r = r[:maxRunes]
		}
		return string(r)
	}
	return "Swarm session"
}
