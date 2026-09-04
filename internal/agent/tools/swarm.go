package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"
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
	// reports whether the target was idle ("sent"), busy ("queued"),
	// or whether the server is draining for an update and journaled
	// the message for delivery by its replacement ("deferred").
	Send(ctx context.Context, senderSessionID string, target SwarmLookupResult, part message.SwarmMessage) (delivery string, err error)
	// CreateSessionInWorkspace creates a new session in an existing
	// workspace and returns it (with color/animal assigned). Fails
	// if the workspace is not currently running. opts carries the
	// title, optional model reference, swarm lineage, and optional
	// working dir; the backend validates the model reference and the
	// working dir against the target workspace.
	CreateSessionInWorkspace(ctx context.Context, workspaceID string, opts SwarmNewOptions) (session.Session, error)
	// ArchiveSessionInWorkspace archives a session that was created
	// via CreateSessionInWorkspace but that we then failed to send
	// the initial message to. Best-effort compensating cleanup so
	// callers don't leak orphaned empty sessions on retry.
	ArchiveSessionInWorkspace(ctx context.Context, workspaceID, sessionID string) error
	// CreateSessionInWorkspaceAtPath ensures a workspace is running
	// for the given directory path (creating it on disk or attaching
	// a detached one if needed), then creates a new session in it. It
	// returns the resolved workspace id alongside the session so the
	// caller can deliver the initial message. When opts.WorkingDir is
	// empty the session's working dir defaults to path.
	CreateSessionInWorkspaceAtPath(ctx context.Context, path string, opts SwarmNewOptions) (workspaceID string, sess session.Session, err error)
	// ResolveWorkspaceByPath resolves a directory path to the ID of
	// the running workspace rooted at that path. found is false (with
	// no error) when no running workspace matches.
	ResolveWorkspaceByPath(ctx context.Context, path string) (workspaceID string, found bool, err error)
	// RenameSession updates the title of the target session (resolved
	// via LookupAddress) in its own workspace, which may differ from
	// the caller's. Cross-workspace safe.
	RenameSession(ctx context.Context, target SwarmLookupResult, title string) error
}

// SwarmLookupResult mirrors backend.SwarmLookupResult so the tool
// package does not import backend directly (cycle avoidance).
type SwarmLookupResult struct {
	WorkspaceID   string
	SessionID     string
	Color         string
	Animal        string
	WorkspaceRoot string
	Sub           bool
}

// SwarmNewOptions describes the session a `swarm new` call creates. It
// mirrors backend.SwarmSpawnOptions so the tool package does not import
// backend directly.
type SwarmNewOptions struct {
	Title string
	// ModelRef, when non-empty, is the model reference the new session
	// runs on (a role name, provider/model, or bare id) instead of the
	// workspace large model. Validated by the backend against the
	// target workspace's config.
	ModelRef string
	// SpawnedBySessionID and SpawnedByWorkspaceID are the trusted
	// identity of the spawning session, recorded as lineage on the new
	// session. They come from the tool's own context, never from model
	// input.
	SpawnedBySessionID   string
	SpawnedByWorkspaceID string
	// WorkingDir, when set, is the absolute directory the new
	// session's tools run in. The backend validates it resolves to the
	// target workspace's project.
	WorkingDir string
}

// SwarmResponseMetadata is attached to every successful swarm tool
// result so UIs can link to the target session without parsing prose.
type SwarmResponseMetadata struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
	Color       string `json:"color,omitempty"`
	Animal      string `json:"animal,omitempty"`
	// Address is the shorthash-qualified color-animal form.
	Address string `json:"address,omitempty"`
	// WorkingDir is the directory a newly created session runs in;
	// empty for deliveries to existing sessions.
	WorkingDir string `json:"working_dir,omitempty"`
	// Delivery is "sent" (target was idle), "queued" (target busy), or
	// "deferred" (server draining for an update; delivered after it).
	Delivery string `json:"delivery"`
	BTW      bool   `json:"btw,omitempty"`
	// Created is true when this call spawned the target session.
	Created bool `json:"created,omitempty"`
	// ReplyRequired is true when the target must reply to the sender
	// before its turn can end.
	ReplyRequired bool `json:"reply_required,omitempty"`
	// FulfilledReply is true when this send satisfied a reply the
	// sender owed the target (the target had messaged the sender with
	// require_reply).
	FulfilledReply bool `json:"fulfilled_reply,omitempty"`
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
	// Path is optional when Address == "new"; an alternative to
	// WorkspaceID that targets a workspace by its directory path,
	// bringing it up if it isn't currently running. Takes precedence
	// over WorkspaceID when both are set.
	Path string `json:"path,omitempty" description:"With address='new': a directory path to spawn the session in, bringing up the workspace if it is not currently running. Alternative to workspace_id; takes precedence when both are set."`
	// Model is optional when Address == "new": the model the new session
	// runs on, as a configured role name (large, small, worker, or a
	// user-defined role), 'provider/model', or a bare model id, resolved in
	// the TARGET workspace's config on every turn. Omitted keeps today's
	// default: the new session runs its workspace's large model.
	Model string `json:"model,omitempty" description:"With address='new': the model for the new session, as a role name (large, small, worker, or a configured role), 'provider/model', or a bare model id, resolved in the target workspace's config. Omitted runs that workspace's large model. Rejected (tool error) for existing sessions, which keep their own model."`
	// WorkingDir is optional when Address == "new": the absolute
	// directory the new session's tools run in. Must resolve to the
	// target workspace's project (a subdirectory or a sibling git
	// worktree). Defaults to Path when Path is given.
	WorkingDir string `json:"working_dir,omitempty" description:"With address='new': absolute directory the new session's tools run in. Must be inside the target workspace's project (a subdirectory or a linked git worktree of it). Defaults to path when path is given."`
	// RequireReply makes the target owe this session a swarm reply
	// before its turn can end. The target is reminded up front and
	// nudged at end of turn; if it still does not reply, the system
	// replies on its behalf with its final message.
	RequireReply bool `json:"require_reply,omitempty" description:"When true, the target session must reply to you (via the swarm tool) before it can end its turn. Use this when you need to be told the outcome, e.g. when spawning a worker with address='new'. The target is nudged if it forgets; as a last resort its final message is forwarded to you automatically."`
}

// NewSwarmTool builds the fantasy tool wrapper. sessions is the
// current workspace's session service, used to look up the sender's
// identity (so the tool can stamp "message from <color-animal>:" onto
// the outgoing message and refuse self-addressed sends).
//
// replies is the coordinator's reply-obligation registry; a send to an
// existing session fulfills any reply the sender owed that session.
// Nil disables tracking.
func NewSwarmTool(be SwarmBackend, sessions session.Service, swarmCfg func() swarm.Config, senderWorkspaceID string, replies *swarm.ReplyTracker) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		SwarmToolName,
		swarmToolDescription,
		func(ctx context.Context, params SwarmParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return runSwarm(ctx, be, sessions, swarmCfg, senderWorkspaceID, replies, params)
		},
	)
}

func runSwarm(
	ctx context.Context,
	be SwarmBackend,
	sessions session.Service,
	swarmCfg func() swarm.Config,
	senderWorkspaceID string,
	replies *swarm.ReplyTracker,
	params SwarmParams,
) (fantasy.ToolResponse, error) {
	if strings.TrimSpace(params.Prompt) == "" {
		return fantasy.NewTextErrorResponse("swarm: prompt is required"), nil
	}
	address := strings.TrimSpace(params.Address)
	if address == "" {
		return fantasy.NewTextErrorResponse("swarm: address is required"), nil
	}

	senderID := GetSessionFromContext(ctx)
	if senderID == "" {
		return fantasy.NewTextErrorResponse("swarm: sender session id missing from context"), nil
	}
	sender, err := resolveSwarmSender(ctx, sessions, swarmCfg, senderWorkspaceID, senderID)
	if err != nil {
		if isContextErr(err) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: %s", err)), nil
	}

	// Fast-fail self-address before doing any cross-workspace lookup.
	// Compare against every canonical form the sender could plausibly
	// have typed.
	if isSelfAddress(address, senderID, sender.ident) {
		return fantasy.NewTextErrorResponse("swarm: cannot address your own session"), nil
	}

	btw, ok := parseSwarmMode(params.Mode)
	if !ok {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: unknown mode %q (want 'queue' or 'btw')", params.Mode)), nil
	}
	sender.replies = replies

	// "new" path: create a fresh session in the given workspace and
	// treat the prompt as its initial user message.
	if strings.EqualFold(address, "new") {
		return runSwarmNew(ctx, be, sender, params, btw)
	}
	if strings.TrimSpace(params.Model) != "" {
		return fantasy.NewTextErrorResponse("swarm: 'model' only applies with address='new'; an existing session keeps its own model"), nil
	}
	if strings.TrimSpace(params.WorkingDir) != "" {
		return fantasy.NewTextErrorResponse("swarm: 'working_dir' only applies with address='new'; an existing session keeps its own working directory"), nil
	}
	return runSwarmDeliver(ctx, be, sender, address, params.Prompt, btw, params.RequireReply)
}

// swarmSender bundles the resolved, trusted identity of the session
// invoking the swarm tool. It is threaded into the delivery helpers so
// they can stamp the outgoing part and format response text without
// re-deriving anything.
type swarmSender struct {
	id          string
	workspaceID string
	ident       swarm.Identity
	// address is the sender's own formatted "color-animal[-hash]"
	// address, used for the "from <sender>" response text and the
	// reply-required trailer that tells the target where to reply.
	address string
	// replies, when non-nil, is consulted so a send to an existing
	// session fulfills any reply the sender owed it.
	replies *swarm.ReplyTracker
}

// resolveSwarmSender loads the trusted identity of senderID from the
// session service. It falls back to computing the color/animal from
// cfg when the session row is missing them (legacy or race with
// backfill).
func resolveSwarmSender(ctx context.Context, sessions session.Service, swarmCfg func() swarm.Config, workspaceID, senderID string) (swarmSender, error) {
	senderSess, err := sessions.Get(ctx, senderID)
	if err != nil {
		if isContextErr(err) {
			return swarmSender{}, err
		}
		return swarmSender{}, fmt.Errorf("failed to load sender session: %w", err)
	}
	ident := swarm.Identity{Color: senderSess.Color, Animal: senderSess.Animal}
	if ident.Color == "" || ident.Animal == "" {
		ident = swarm.Assign(senderSess.ID, swarmCfg())
	}
	return swarmSender{
		id:          senderID,
		workspaceID: workspaceID,
		ident:       ident,
		address:     swarm.FormatAddress(ident, senderSess.ID),
	}, nil
}

// SwarmReplyOnBehalf delivers text from senderSessionID to
// targetSessionID as an ordinary swarm message, stamped with the
// sender's trusted identity. The coordinator uses it to honor a
// require_reply obligation the agent itself failed to satisfy, so the
// waiting session still hears back. It never sets require_reply on the
// outgoing message (that would ping-pong) and never fulfills tracked
// obligations itself; the caller owns that bookkeeping.
func SwarmReplyOnBehalf(
	ctx context.Context,
	be SwarmBackend,
	sessions session.Service,
	swarmCfg func() swarm.Config,
	senderWorkspaceID, senderSessionID, targetSessionID, text string,
) error {
	if be == nil || sessions == nil {
		return errors.New("swarm backend unavailable")
	}
	if swarmCfg == nil {
		swarmCfg = swarm.Default
	}
	sender, err := resolveSwarmSender(ctx, sessions, swarmCfg, senderWorkspaceID, senderSessionID)
	if err != nil {
		return err
	}
	target, err := be.LookupAddress(ctx, targetSessionID)
	if err != nil {
		return err
	}
	if target.SessionID == sender.id {
		return errors.New("cannot reply to own session")
	}
	_, err = be.Send(ctx, sender.id, target, buildSwarmPart(sender, text, false, false))
	return err
}

// parseSwarmMode normalizes the mode param, returning whether the
// message should be delivered as a "btw" aside. ok is false for an
// unrecognized mode.
func parseSwarmMode(mode string) (btw, ok bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "queue":
		return false, true
	case "btw":
		return true, true
	default:
		return false, false
	}
}

// isContextErr reports whether err is a context cancellation or
// deadline, which callers propagate as a hard error rather than a
// soft tool-error response.
func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// sendSwarmPart builds and delivers the swarm part to target. It
// classifies the outcome: hardErr (context cancellation) must be
// propagated to the runtime; softErr is a delivery failure the caller
// should format as a tool-error response (after any compensating
// cleanup). On success both errors are nil and delivery is "sent" or
// "queued".
func sendSwarmPart(ctx context.Context, be SwarmBackend, sender swarmSender, prompt string, btw, requireReply bool, target SwarmLookupResult) (delivery string, hardErr, softErr error) {
	part := buildSwarmPart(sender, prompt, btw, requireReply)
	delivery, err := be.Send(ctx, sender.id, target, part)
	if err != nil {
		if isContextErr(err) {
			return "", err, nil
		}
		return "", nil, err
	}
	return delivery, nil, nil
}

// runSwarmNew handles address=="new": create a session (by workspace
// id, defaulting to the sender's, or by directory path) and deliver
// the prompt as its first message. On a delivery failure it archives
// the just-created empty session so retries don't leak ghosts.
func runSwarmNew(ctx context.Context, be SwarmBackend, sender swarmSender, params SwarmParams, btw bool) (fantasy.ToolResponse, error) {
	title := strings.TrimSpace(params.Title)
	if title == "" {
		title = firstLine(params.Prompt, 60)
	}

	workingDir := strings.TrimSpace(params.WorkingDir)
	if workingDir != "" && !filepath.IsAbs(workingDir) {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: working_dir must be an absolute path, got %q", workingDir)), nil
	}
	opts := SwarmNewOptions{
		Title:                title,
		ModelRef:             strings.TrimSpace(params.Model),
		SpawnedBySessionID:   sender.id,
		SpawnedByWorkspaceID: sender.workspaceID,
		WorkingDir:           workingDir,
	}

	var (
		workspaceID string
		newSess     session.Session
		err         error
	)
	if path := strings.TrimSpace(params.Path); path != "" {
		// Path-based: bring up the workspace for this directory
		// (creating or attaching it) and create a session in it. The
		// backend pins the session's working dir to path unless an
		// explicit working_dir was given.
		workspaceID, newSess, err = be.CreateSessionInWorkspaceAtPath(ctx, path, opts)
	} else {
		workspaceID = params.WorkspaceID
		if workspaceID == "" {
			// Default to the sender's own workspace when the model
			// doesn't supply one explicitly. Workspace ids are
			// backend-internal handles that aren't easily discoverable
			// from a session, so requiring an explicit id every time is
			// a bad UX; same-workspace is the overwhelmingly common case.
			workspaceID = sender.workspaceID
		}
		if workspaceID == "" {
			return fantasy.NewTextErrorResponse("swarm: address='new' requires workspace_id or path (sender workspace id unavailable)"), nil
		}
		newSess, err = be.CreateSessionInWorkspace(ctx, workspaceID, opts)
	}
	if err != nil {
		if isContextErr(err) {
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
	delivery, hardErr, softErr := sendSwarmPart(ctx, be, sender, params.Prompt, btw, params.RequireReply, target)
	if hardErr != nil {
		return fantasy.ToolResponse{}, hardErr
	}
	if softErr != nil {
		// Compensating cleanup: archive the empty session we just
		// created so retries don't leak ghosts. Failures here are
		// best-effort; the outer error is what the LLM sees.
		_ = be.ArchiveSessionInWorkspace(context.Background(), workspaceID, newSess.ID)
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: failed to send initial message to new session (session archived): %s", softErr)), nil
	}
	modelNote := ""
	if newSess.ModelRef != "" {
		modelNote = " Runs on " + newSess.ModelRef + "."
	}
	dirNote := ""
	if newSess.WorkingDir != "" {
		dirNote = fmt.Sprintf(" Working dir %s.", newSess.WorkingDir)
	}
	targetAddress := swarm.FormatAddress(swarm.Identity{Color: newSess.Color, Animal: newSess.Animal}, newSess.ID)
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(fmt.Sprintf(
		"Created and %s to %s (workspace=%s, session=%s).%s%s%s",
		delivery, targetAddress, workspaceID, newSess.ID, modelNote, dirNote, replyRequiredNote(params.RequireReply),
	)), SwarmResponseMetadata{
		WorkspaceID:   workspaceID,
		SessionID:     newSess.ID,
		Color:         newSess.Color,
		Animal:        newSess.Animal,
		Address:       targetAddress,
		WorkingDir:    newSess.WorkingDir,
		Delivery:      delivery,
		BTW:           btw,
		Created:       true,
		ReplyRequired: params.RequireReply,
	}), nil
}

// replyRequiredNote is the response-text suffix confirming the target
// owes the sender a reply.
func replyRequiredNote(requireReply bool) string {
	if !requireReply {
		return ""
	}
	return " Reply required: the target must message you back before its turn can end."
}

// runSwarmDeliver resolves an existing address and delivers the
// prompt to it.
func runSwarmDeliver(ctx context.Context, be SwarmBackend, sender swarmSender, address, prompt string, btw, requireReply bool) (fantasy.ToolResponse, error) {
	target, err := be.LookupAddress(ctx, address)
	if err != nil {
		if isContextErr(err) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: %s", err)), nil
	}
	// Second self-address guard: the fast-fail check in runSwarm only
	// sees the raw address string, but a lookup can still resolve a
	// differently-spelled address back to the sender's own session.
	if target.SessionID == sender.id {
		return fantasy.NewTextErrorResponse("swarm: cannot address your own session"), nil
	}
	if target.Sub {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"swarm: %s is a sub-agent session and cannot receive swarm messages", address,
		)), nil
	}

	delivery, hardErr, softErr := sendSwarmPart(ctx, be, sender, prompt, btw, requireReply, target)
	if hardErr != nil {
		return fantasy.ToolResponse{}, hardErr
	}
	if softErr != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("swarm: %s", softErr)), nil
	}
	// Any message to a session we owed a reply to counts as that
	// reply, whatever mode it used.
	fulfilled := sender.replies.Fulfill(sender.id, target.SessionID)
	fulfilledNote := ""
	if fulfilled {
		fulfilledNote = " This satisfies the reply you owed them."
	}
	targetAddress := swarm.FormatAddress(swarm.Identity{Color: target.Color, Animal: target.Animal}, target.SessionID)
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(fmt.Sprintf(
		"%s: %s (from %s to %s)%s%s",
		delivery, prompt, sender.address, targetAddress, replyRequiredNote(requireReply), fulfilledNote,
	)), SwarmResponseMetadata{
		WorkspaceID:    target.WorkspaceID,
		SessionID:      target.SessionID,
		Color:          target.Color,
		Animal:         target.Animal,
		Address:        targetAddress,
		Delivery:       delivery,
		BTW:            btw,
		ReplyRequired:  requireReply,
		FulfilledReply: fulfilled,
	}), nil
}

// buildSwarmPart constructs the proto.SwarmMessage that will be stored
// on the receiving session's transcript. The Text field is the exact
// prefixed body the LLM will read; Body preserves the original prompt
// for programmatic consumers. When requireReply is set the text also
// carries a trailer telling the target where to reply, so the model
// knows about the obligation before it starts rather than only when
// nudged at end of turn.
func buildSwarmPart(sender swarmSender, prompt string, btw, requireReply bool) message.SwarmMessage {
	prefix := fmt.Sprintf("message from %s: ", sender.ident.String())
	if btw {
		prefix = "[btw] " + prefix
	}
	text := prefix + prompt
	if requireReply {
		text += "\n\n" + ReplyRequiredTrailer(sender.address)
	}
	return message.SwarmMessage{
		Text:              text,
		Body:              prompt,
		SenderSessionID:   sender.id,
		SenderColor:       sender.ident.Color,
		SenderAnimal:      sender.ident.Animal,
		SenderWorkspaceID: sender.workspaceID,
		BTW:               btw,
		RequireReply:      requireReply,
	}
}

// ReplyRequiredTrailer is the instruction appended to a require_reply
// message so the receiving agent knows, up front, that it must send
// its result to senderAddress before its turn can end.
func ReplyRequiredTrailer(senderAddress string) string {
	return fmt.Sprintf(
		"[reply required: when you have finished, send your result to %s with the swarm tool (address=%q). Your turn cannot end until you do.]",
		senderAddress, senderAddress,
	)
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
