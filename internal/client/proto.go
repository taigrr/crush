package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// ListWorkspaces retrieves all workspaces from the server.
func (c *Client) ListWorkspaces(ctx context.Context) ([]proto.Workspace, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, "/workspaces", nil, nil)
	return decodeResponse[[]proto.Workspace](rsp, err, "list workspaces")
}

// ListWorkspaceOverviews lists all known workspaces (attached and
// registry-known) with their sessions for the cross-workspace picker.
func (c *Client) ListWorkspaceOverviews(ctx context.Context) ([]proto.WorkspaceOverview, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, "/workspace-overviews", nil, nil)
	return decodeResponse[[]proto.WorkspaceOverview](rsp, err, "list workspace overviews")
}

// CreateWorkspace creates a new workspace on the server.
func (c *Client) CreateWorkspace(ctx context.Context, ws proto.Workspace) (*proto.Workspace, error) {
	ws.ClientID = c.clientID
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, "/workspaces", nil, jsonBody(ws), jsonHeader())
	return decodeResponse[*proto.Workspace](rsp, err, "create workspace")
}

// GetWorkspace retrieves a workspace from the server.
func (c *Client) GetWorkspace(ctx context.Context, id string) (*proto.Workspace, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s", id), nil, nil)
	return decodeResponse[*proto.Workspace](rsp, err, "get workspace")
}

// DeleteWorkspace deletes a workspace on the server.
func (c *Client) DeleteWorkspace(ctx context.Context, id string) error {
	q := c.clientQuery()
	rsp, err := c.delete(ctx, fmt.Sprintf("/workspaces/%s", id), q, nil)
	return checkResponse(rsp, err, "delete workspace")
}

// SetCurrentSession reports the client's current-session selection
// for the named workspace. An empty sessionID clears the entry. The
// request carries the process-scoped client ID minted in [NewClient]
// as a query parameter so the server can route the update to the
// correct [clientState] entry.
func (c *Client) SetCurrentSession(ctx context.Context, workspaceID, sessionID string) error {
	q := c.clientQuery()
	rsp, err := c.post(
		ctx,
		fmt.Sprintf("/workspaces/%s/current-session", workspaceID),
		q,
		jsonBody(proto.CurrentSession{SessionID: sessionID}),
		jsonHeader(),
	)
	return checkResponse(rsp, err, "set current session")
}

// SubscribeEvents subscribes to server-sent events for a workspace.
func (c *Client) SubscribeEvents(ctx context.Context, id string) (<-chan any, error) {
	q := c.clientQuery()
	q.Set("wire", strconv.Itoa(proto.WireVersion))
	if c.nodeID != "" {
		// Across the network, merge streamed updates into frames; on the
		// local socket every token is cheap, so keep them immediate.
		q.Set("frame_ms", strconv.Itoa(int(remoteFrame/time.Millisecond)))
	}
	return c.subscribeQuery(ctx, fmt.Sprintf("/workspaces/%s/events", id), q, "subscribe to events")
}

// remoteFrame is the coalescing interval requested for remote streams.
const remoteFrame = 50 * time.Millisecond

// SubscribeGlobalEvents subscribes to the server's observe-only global
// attention stream (GET /v1/events): cross-workspace permission/question
// blocked+resolved and agent busy/idle transitions. Unlike
// SubscribeEvents it is not scoped to a workspace and does not attach the
// client to (or pin alive) any workspace.
func (c *Client) SubscribeGlobalEvents(ctx context.Context) (<-chan any, error) {
	return c.subscribe(ctx, "/events", "subscribe to global events")
}

// subscribe opens an SSE stream at path and pumps decoded events into the
// returned channel until ctx is done or the stream ends.
func (c *Client) subscribe(ctx context.Context, path, op string) (<-chan any, error) {
	return c.subscribeQuery(ctx, path, c.clientQuery(), op)
}

func (c *Client) subscribeQuery(ctx context.Context, path string, query url.Values, op string) (<-chan any, error) {
	//nolint:bodyclose
	rsp, err := c.get(ctx, path, query, http.Header{
		"Accept":        []string{"text/event-stream"},
		"Cache-Control": []string{"no-cache"},
		"Connection":    []string{"keep-alive"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to %s: %w", op, err)
	}
	if err := checkStatus(rsp, op); err != nil {
		rsp.Body.Close()
		return nil, err
	}

	events := make(chan any, 100)
	go func() {
		defer rsp.Body.Close()
		defer close(events)
		streamEvents(ctx, rsp.Body, events, readErrorRetryDelay)
	}()
	return events, nil
}

// readErrorRetryDelay is how long streamEvents waits after a non-EOF
// read error before retrying. Overridable in tests to avoid real
// multi-second sleeps.
var readErrorRetryDelay = 2 * time.Second

// maxConsecutiveReadErrors bounds how many times a non-EOF read error
// is tolerated in a row before giving up on this connection and
// closing events. A couple of retries ride out brief blips on an
// otherwise-live connection; retrying forever would mean a truly dead
// connection (e.g. a reset TCP socket) never surfaces as closed,
// silently starving callers of any further events with no way to tell
// "lost connection" apart from "still working". Closing lets the
// caller (ClientWorkspace) open a fresh connection instead of retrying
// reads on a socket that can never recover.
const maxConsecutiveReadErrors = 3

// streamEvents reads SSE frames from body, parsing and forwarding each
// to events until the stream ends (EOF), the context is cancelled, or
// too many consecutive non-EOF read errors occur. It is split out from
// SubscribeEvents so it can be unit-tested against a crafted reader.
func streamEvents(ctx context.Context, body io.Reader, events chan any, retryDelay time.Duration) {
	consecutiveErrors := 0

	scr := bufio.NewReader(body)
	dec := &wireDecoder{messages: map[string]proto.Message{}}
	for {
		line, readErr := scr.ReadBytes('\n')
		// ReadBytes returns a final, newline-less line together with
		// io.EOF, so parse what we got before deciding to break;
		// otherwise the last event in the stream would be dropped.
		if ev, ok := dec.parse(line); ok {
			if !sendEvent(ctx, events, ev) {
				return
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveErrors++
			slog.Error("Reading from events stream", "error", readErr, "consecutive_errors", consecutiveErrors)
			if consecutiveErrors >= maxConsecutiveReadErrors {
				slog.Error("Giving up on events stream after repeated read errors")
				break
			}
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return
			}
			continue
		}
		consecutiveErrors = 0
	}
}

// parseSSELine parses a single Server-Sent Events line into a typed pubsub
// event. It returns ok=false (and logs) for blank lines, malformed frames, or
// unknown event types.
func parseSSELine(line []byte) (any, bool) {
	return (&wireDecoder{messages: map[string]proto.Message{}}).parse(line)
}

// wireDecoder reassembles wire 2 message deltas into full messages, so
// everything above the client sees the same events as with wire 1.
type wireDecoder struct {
	messages map[string]proto.Message
}

func (d *wireDecoder) parse(line []byte) (any, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, false
	}

	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		slog.Warn("Invalid event format", "line", string(line))
		return nil, false
	}
	data = bytes.TrimSpace(data)

	var p pubsub.Payload
	if err := json.Unmarshal(data, &p); err != nil {
		slog.Error("Unmarshaling event envelope", "error", err)
		return nil, false
	}
	if p.Type == pubsub.PayloadTypeMessageDelta {
		return d.applyDelta(p.Payload)
	}
	ev, ok := decodeEvent(p)
	if me, isMsg := ev.(pubsub.Event[proto.Message]); ok && isMsg {
		if me.Type == pubsub.DeletedEvent || me.Payload.IsFinished() {
			delete(d.messages, me.Payload.ID)
		} else {
			d.messages[me.Payload.ID] = me.Payload
		}
	}
	return ev, ok
}

// applyDelta rebuilds the full message from the last copy plus d. A delta
// that does not fit (missed keyframe) is dropped; the server sends a full
// message again soon.
func (d *wireDecoder) applyDelta(raw json.RawMessage) (any, bool) {
	var delta proto.MessageDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		slog.Error("Unmarshaling message delta", "error", err)
		return nil, false
	}
	m, ok := d.messages[delta.ID]
	if !ok || !proto.ApplyDelta(&m, delta) {
		slog.Debug("Dropping message delta without a matching base", "message", delta.ID)
		return nil, false
	}
	d.messages[delta.ID] = m
	return pubsub.Event[proto.Message]{Type: pubsub.UpdatedEvent, Payload: m}, true
}

// decodeEvent decodes the inner payload of an envelope into the concrete
// pubsub.Event type for its discriminator. It returns ok=false for unknown
// types.
func decodeEvent(p pubsub.Payload) (any, bool) {
	switch p.Type {
	case pubsub.PayloadTypeLSPEvent:
		return unmarshalEvent[proto.LSPEvent](p.Payload)
	case pubsub.PayloadTypeMCPEvent:
		return unmarshalEvent[proto.MCPEvent](p.Payload)
	case pubsub.PayloadTypeSkillEvent:
		return unmarshalEvent[proto.SkillEvent](p.Payload)
	case pubsub.PayloadTypePermissionRequest:
		return unmarshalEvent[proto.PermissionRequest](p.Payload)
	case pubsub.PayloadTypePermissionNotification:
		return unmarshalEvent[proto.PermissionNotification](p.Payload)
	case pubsub.PayloadTypeQuestionRequest:
		return unmarshalEvent[proto.QuestionRequest](p.Payload)
	case pubsub.PayloadTypeQuestionNotification:
		return unmarshalEvent[proto.QuestionNotification](p.Payload)
	case pubsub.PayloadTypeMessage:
		return unmarshalEvent[proto.Message](p.Payload)
	case pubsub.PayloadTypeSession:
		return unmarshalEvent[proto.Session](p.Payload)
	case pubsub.PayloadTypeFile:
		return unmarshalEvent[proto.File](p.Payload)
	case pubsub.PayloadTypeAgentEvent:
		return unmarshalEvent[proto.AgentEvent](p.Payload)
	case pubsub.PayloadTypeSkillsEvent:
		return unmarshalEvent[proto.SkillsEvent](p.Payload)
	case pubsub.PayloadTypeConfigChanged:
		return unmarshalEvent[proto.ConfigChanged](p.Payload)
	case pubsub.PayloadTypeRunComplete:
		return unmarshalEvent[proto.RunComplete](p.Payload)
	case pubsub.PayloadTypeForkProgress:
		return unmarshalEvent[proto.ForkProgress](p.Payload)
	case pubsub.PayloadTypeAttentionEvent:
		return unmarshalEvent[proto.AttentionEvent](p.Payload)
	default:
		slog.Warn("Unknown event type", "type", p.Type)
		return nil, false
	}
}

// unmarshalEvent decodes a raw payload into a pubsub.Event[T]. Decode errors
// are ignored to preserve the original best-effort streaming behavior; the
// (possibly zero) event is still delivered.
func unmarshalEvent[T any](payload json.RawMessage) (any, bool) {
	var e pubsub.Event[T]
	_ = json.Unmarshal(payload, &e)
	return e, true
}

func sendEvent(ctx context.Context, evc chan any, ev any) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case evc <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// GetLSPDiagnostics retrieves LSP diagnostics for a specific LSP client.
func (c *Client) GetLSPDiagnostics(ctx context.Context, id string, lspName string) (map[protocol.DocumentURI][]protocol.Diagnostic, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/lsps/%s/diagnostics", id, lspName), nil, nil)
	return decodeResponse[map[protocol.DocumentURI][]protocol.Diagnostic](rsp, err, "get LSP diagnostics")
}

// GetLSPs retrieves the LSP client states for a workspace.
func (c *Client) GetLSPs(ctx context.Context, id string) (map[string]proto.LSPClientInfo, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/lsps", id), nil, nil)
	return decodeResponse[map[string]proto.LSPClientInfo](rsp, err, "get LSPs")
}

// MCPGetStates retrieves the MCP client states for a workspace.
func (c *Client) MCPGetStates(ctx context.Context, id string) (map[string]proto.MCPClientInfo, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/mcp/states", id), nil, nil)
	return decodeResponse[map[string]proto.MCPClientInfo](rsp, err, "get MCP states")
}

// SkillsGetStates retrieves the skill discovery states for a workspace.
func (c *Client) SkillsGetStates(ctx context.Context, id string) ([]proto.SkillState, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/skills/states", id), nil, nil)
	return decodeResponse[[]proto.SkillState](rsp, err, "get skill states")
}

// MCPRefreshPrompts refreshes prompts for a named MCP client.
func (c *Client) MCPRefreshPrompts(ctx context.Context, id, name string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/mcp/refresh-prompts", id), nil,
		jsonBody(struct {
			Name string `json:"name"`
		}{Name: name}),
		jsonHeader())
	return checkResponse(rsp, err, "refresh MCP prompts")
}

// MCPRefreshResources refreshes resources for a named MCP client.
func (c *Client) MCPRefreshResources(ctx context.Context, id, name string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/mcp/refresh-resources", id), nil,
		jsonBody(struct {
			Name string `json:"name"`
		}{Name: name}),
		jsonHeader())
	return checkResponse(rsp, err, "refresh MCP resources")
}

// GetAgentSessionQueuedPrompts retrieves the number of queued prompts for a
// session.
func (c *Client) GetAgentSessionQueuedPrompts(ctx context.Context, id string, sessionID string) (int, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/prompts/queued", id, sessionID), nil, nil)
	return decodeResponse[int](rsp, err, "get session agent queued prompts")
}

// ClearAgentSessionQueuedPrompts clears the queued prompts for a session.
func (c *Client) ClearAgentSessionQueuedPrompts(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/prompts/clear", id, sessionID), nil, nil, nil)
	return checkResponse(rsp, err, "clear session agent queued prompts")
}

// PromoteNextQueuedToBTW converts the session's next queued prompt into a
// /btw aside folded into the active turn. It reports whether one was
// promoted.
func (c *Client) PromoteNextQueuedToBTW(ctx context.Context, id, sessionID string) (bool, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/prompts/promote-btw", id, sessionID), nil, nil, nil)
	out, err := decodeResponse[proto.PromoteQueuedResponse](rsp, err, "promote queued prompt")
	return out.Promoted, err
}

// CancelToolCalls cancels the session's in-flight tool calls without
// canceling its turn or queue. It reports how many were canceled.
func (c *Client) CancelToolCalls(ctx context.Context, id, sessionID string) (int, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/tools/cancel", id, sessionID), nil, nil, nil)
	out, err := decodeResponse[proto.CancelToolCallsResponse](rsp, err, "cancel tool calls")
	return out.Canceled, err
}

// DropQueuedRun removes the still-queued prompt sent with runID without
// cancelling the session's active turn. It reports whether the prompt was
// still queued.
func (c *Client) DropQueuedRun(ctx context.Context, id, sessionID, runID string) (bool, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/runs/%s/drop", id, sessionID, url.PathEscape(runID)), nil, nil, nil)
	if err != nil {
		return false, fmt.Errorf("failed to drop queued run: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to drop queued run: status code %d", rsp.StatusCode)
	}
	var out proto.DropQueuedRunResponse
	if err := json.NewDecoder(rsp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("failed to decode drop response: %w", err)
	}
	return out.Dropped, nil
}

// EmbeddingsPending returns how many past messages would be embedded by
// a backfill under the active embedding model.
func (c *Client) EmbeddingsPending(ctx context.Context, id string) (int, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/embeddings/pending", id), nil, nil)
	return decodeResponse[int](rsp, err, "get pending embeddings")
}

// BackfillEmbeddings embeds past messages lacking a vector and returns
// the count embedded.
func (c *Client) BackfillEmbeddings(ctx context.Context, id string) (int, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/embeddings/backfill", id), nil, nil, nil)
	return decodeResponse[int](rsp, err, "backfill embeddings")
}

// EmbeddingStatus returns the embedding index state for a workspace.
func (c *Client) EmbeddingStatus(ctx context.Context, id string) (proto.EmbeddingStatus, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/embeddings/status", id), nil, nil)
	return decodeResponse[proto.EmbeddingStatus](rsp, err, "get embedding status")
}

// SearchHistory runs hybrid history search over a workspace and returns
// per-session hits.
func (c *Client) SearchHistory(ctx context.Context, id string, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/history/search", id), nil, jsonBody(params),
		jsonHeader())
	return decodeResponse[proto.SearchHistoryResult](rsp, err, "search history")
}

// PeekMessages returns a session's messages from the workspace rooted at
// root (attached or registry-detached), without switching the caller's
// own workspace. Used by the session sidebar's live preview for a
// session outside the currently-attached workspace.
func (c *Client) PeekMessages(ctx context.Context, root, sessionID string) ([]proto.Message, error) {
	rsp, err := c.post(ctx, "/peek-messages", nil, jsonBody(proto.PeekMessagesParams{Root: root, SessionID: sessionID}),
		jsonHeader())
	if err != nil {
		return nil, fmt.Errorf("failed to peek messages: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to peek messages: status code %d", rsp.StatusCode)
	}
	var msgs []proto.Message
	if err := json.NewDecoder(rsp.Body).Decode(&msgs); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to decode messages: %w", err)
	}
	return msgs, nil
}

// PeekSessionInfo returns a session's metadata and history files from the
// workspace rooted at root (attached or registry-detached), without
// switching the caller's own workspace. It is the sidebar-data companion
// to PeekMessages, backing the session sidebar's live preview of the right
// info-sidebar for a session outside the currently-attached workspace.
func (c *Client) PeekSessionInfo(ctx context.Context, root, sessionID string) (proto.PeekSessionInfoResult, error) {
	rsp, err := c.post(ctx, "/peek-session-info", nil, jsonBody(proto.PeekMessagesParams{Root: root, SessionID: sessionID}),
		jsonHeader())
	if err != nil {
		return proto.PeekSessionInfoResult{}, fmt.Errorf("failed to peek session info: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return proto.PeekSessionInfoResult{}, fmt.Errorf("failed to peek session info: status code %d", rsp.StatusCode)
	}
	var res proto.PeekSessionInfoResult
	if err := json.NewDecoder(rsp.Body).Decode(&res); err != nil && !errors.Is(err, io.EOF) {
		return proto.PeekSessionInfoResult{}, fmt.Errorf("failed to decode session info: %w", err)
	}
	return res, nil
}

// GetAgentSessionGoal retrieves the active autonomous goal for a session.
func (c *Client) GetAgentSessionGoal(ctx context.Context, id, sessionID string) (proto.GoalStatus, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/goal", id, sessionID), nil, nil)
	return decodeResponse[proto.GoalStatus](rsp, err, "get session goal")
}

// SetAgentSessionGoal sets (or clears, when condition is blank) the
// autonomous goal for a session.
func (c *Client) SetAgentSessionGoal(ctx context.Context, id, sessionID, condition string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/goal", id, sessionID), nil, jsonBody(proto.SetGoalRequest{
		Condition: condition,
	}), jsonHeader())
	return checkResponse(rsp, err, "set session goal")
}

// SetAgentSessionWorkingDir sets the working directory tools run in for a
// session.
func (c *Client) SetAgentSessionWorkingDir(ctx context.Context, id, sessionID, dir string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/cwd", id, sessionID), nil, jsonBody(proto.SetWorkingDirRequest{
		WorkingDir: dir,
	}), jsonHeader())
	return checkResponse(rsp, err, "set session working dir")
}

// ClearAgentSessionGoal clears the active autonomous goal for a session.
func (c *Client) ClearAgentSessionGoal(ctx context.Context, id, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/goal/clear", id, sessionID), nil, nil, nil)
	return checkResponse(rsp, err, "clear session goal")
}

// GetAgentInfo retrieves the agent status for a workspace.
func (c *Client) GetAgentInfo(ctx context.Context, id string) (*proto.AgentInfo, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent", id), nil, nil)
	return decodeResponse[*proto.AgentInfo](rsp, err, "get agent status")
}

// UpdateAgent triggers an agent model update on the server.
func (c *Client) UpdateAgent(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/update", id), nil, nil, nil)
	return checkResponse(rsp, err, "update agent")
}

// SendMessage sends a message to the agent for a workspace.
//
// When runID is non-empty it is echoed back on the resulting
// proto.RunComplete event, giving the caller a unique correlator
// for completion detection. Pass "" when the caller does not need
// to distinguish its own turn's terminal event from any concurrent
// turn on the same session (e.g. interactive TUI usage).
func (c *Client) SendMessage(ctx context.Context, id string, sessionID, runID, prompt string, attachments ...message.Attachment) error {
	return c.SendAgentMessage(ctx, id, proto.AgentMessage{
		SessionID:   sessionID,
		RunID:       runID,
		Prompt:      prompt,
		Attachments: proto.AttachmentsFromMessage(attachments),
	})
}

// SendAgentMessage sends a fully specified agent message. ClientID is
// always overwritten with this client's ID.
func (c *Client) SendAgentMessage(ctx context.Context, id string, msg proto.AgentMessage) error {
	msg.ClientID = c.clientID
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent", id), nil, jsonBody(msg), jsonHeader())
	if err != nil {
		return fmt.Errorf("failed to send message to agent: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK && rsp.StatusCode != http.StatusAccepted {
		if msg := decodeErrorMessage(rsp.Body); msg != "" {
			return fmt.Errorf("failed to send message to agent: status code %d: %s", rsp.StatusCode, msg)
		}
		return fmt.Errorf("failed to send message to agent: status code %d", rsp.StatusCode)
	}
	return nil
}

// RunShellCommand runs a shell command in the workspace without triggering the agent.
func (c *Client) RunShellCommand(ctx context.Context, id, sessionID, command string) (proto.ShellCommandResponse, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/shell", id, sessionID), nil, jsonBody(proto.ShellCommandRequest{
		Command:  command,
		ClientID: c.clientID,
	}), jsonHeader())
	return decodeResponse[proto.ShellCommandResponse](rsp, err, "run shell command")
}

// decodeErrorMessage attempts to decode the response body as a
// proto.Error and returns its message. It returns an empty string
// when the body is empty or cannot be decoded into a proto.Error
// with a non-empty message, letting callers fall back to a
// status-only error.
func decodeErrorMessage(body io.Reader) string {
	var e proto.Error
	if err := json.NewDecoder(body).Decode(&e); err != nil {
		return ""
	}
	return e.Message
}

// ResolveSwarmAddress resolves a swarm address (color-animal[-hash] or a
// session UUID) to the workspace and session that own it, across every
// workspace the server knows about.
func (c *Client) ResolveSwarmAddress(ctx context.Context, address string) (proto.SwarmTarget, error) {
	rsp, err := c.get(ctx, "/swarm/resolve", url.Values{"address": []string{address}}, nil)
	if err != nil {
		return proto.SwarmTarget{}, fmt.Errorf("failed to resolve swarm address: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		if msg := decodeErrorMessage(rsp.Body); msg != "" {
			return proto.SwarmTarget{}, fmt.Errorf("failed to resolve swarm address %q: %s", address, msg)
		}
		return proto.SwarmTarget{}, fmt.Errorf("failed to resolve swarm address %q: status code %d", address, rsp.StatusCode)
	}
	var target proto.SwarmTarget
	if err := json.NewDecoder(rsp.Body).Decode(&target); err != nil {
		return proto.SwarmTarget{}, fmt.Errorf("failed to decode swarm target: %w", err)
	}
	return target, nil
}

// GetPendingPrompts returns the permission requests and questions the
// session (or any of its sub-agent sessions) is blocked on.
func (c *Client) GetPendingPrompts(ctx context.Context, id, sessionID string) (proto.PendingPrompts, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/pending", id, sessionID), nil, nil)
	return decodeResponse[proto.PendingPrompts](rsp, err, "get pending prompts")
}

// GetAgentSessionInfo retrieves the agent session info for a workspace.
func (c *Client) GetAgentSessionInfo(ctx context.Context, id string, sessionID string) (*proto.AgentSession, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s", id, sessionID), nil, nil)
	return decodeResponse[*proto.AgentSession](rsp, err, "get session agent info")
}

// AgentSummarizeSession requests a session summarization.
func (c *Client) AgentSummarizeSession(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/summarize", id, sessionID), nil, nil, nil)
	return checkResponse(rsp, err, "summarize session")
}

// AgentGenerateTitle requests a session title regeneration.
func (c *Client) AgentGenerateTitle(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/title", id, sessionID), nil, nil, nil)
	return checkResponse(rsp, err, "regenerate session title")
}

// InitiateAgentProcessing triggers agent initialization on the server.
func (c *Client) InitiateAgentProcessing(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/init", id), nil, nil, nil)
	return checkResponse(rsp, err, "initiate session agent processing")
}

// ListMessages retrieves all messages for a session as proto types.
func (c *Client) ListMessages(ctx context.Context, id string, sessionID string) ([]proto.Message, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/messages", id, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get messages: status code %d", rsp.StatusCode)
	}
	var msgs []proto.Message
	if err := json.NewDecoder(rsp.Body).Decode(&msgs); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to decode messages: %w", err)
	}
	return msgs, nil
}

// GetSession retrieves a specific session as a proto type.
func (c *Client) GetSession(ctx context.Context, id string, sessionID string) (*proto.Session, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s", id, sessionID), nil, nil)
	return decodeResponse[*proto.Session](rsp, err, "get session")
}

// ListSessionHistoryFiles retrieves history files for a session as proto types.
func (c *Client) ListSessionHistoryFiles(ctx context.Context, id string, sessionID string) ([]proto.File, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/history", id, sessionID), nil, nil)
	return decodeResponse[[]proto.File](rsp, err, "get session history files")
}

// CreateSession creates a new session in a workspace as a proto type.
func (c *Client) CreateSession(ctx context.Context, id string, title string) (*proto.Session, error) {
	return c.CreateSessionWithModel(ctx, id, title, "")
}

// CreateSessionWithModel creates a session that runs modelRef (a role
// name, "provider/model", or a bare model id) instead of the workspace's
// large model. An empty modelRef uses the large model.
func (c *Client) CreateSessionWithModel(ctx context.Context, id, title, modelRef string) (*proto.Session, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions", id), nil, jsonBody(proto.Session{Title: title, ModelRef: modelRef}), jsonHeader())
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		if msg := decodeErrorMessage(rsp.Body); msg != "" {
			return nil, fmt.Errorf("failed to create session: status code %d: %s", rsp.StatusCode, msg)
		}
		return nil, fmt.Errorf("failed to create session: status code %d", rsp.StatusCode)
	}
	var sess proto.Session
	if err := json.NewDecoder(rsp.Body).Decode(&sess); err != nil {
		return nil, fmt.Errorf("failed to decode session: %w", err)
	}
	return &sess, nil
}

// ListSessions lists all sessions in a workspace as proto types.
func (c *Client) ListSessions(ctx context.Context, id string) ([]proto.Session, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions", id), nil, nil)
	return decodeResponse[[]proto.Session](rsp, err, "get sessions")
}

func (c *Client) ListSessionImportSources(ctx context.Context) ([]proto.SessionImportSources, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, "/session-import/sources", nil, nil)
	return decodeResponse[[]proto.SessionImportSources](rsp, err, "list session import sources")
}

func (c *Client) DiscoverSessionImports(ctx context.Context, source string) ([]proto.SessionImportCandidate, error) {
	query := url.Values{}
	query.Set("source", source)
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, "/session-import/candidates", query, nil)
	return decodeResponse[[]proto.SessionImportCandidate](rsp, err, "discover session imports")
}

func (c *Client) ImportSessions(ctx context.Context, id string, paths []string, from string) ([]proto.SessionImportResult, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/session-import", id), nil, jsonBody(proto.SessionImportRequest{Paths: paths, Source: from}), jsonHeader())
	return decodeResponse[[]proto.SessionImportResult](rsp, err, "import sessions")
}

// GrantPermission grants a permission on a workspace. The returned
// bool reports whether this call resolved the pending request (true)
// or found it already resolved by a previous caller (false). A false
// value is not an error — it just means another subscriber resolved
// the same request first.
func (c *Client) GrantPermission(ctx context.Context, id string, req proto.PermissionGrant) (bool, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/permissions/grant", id), nil, jsonBody(req), jsonHeader())
	if err != nil {
		return false, fmt.Errorf("failed to grant permission: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to grant permission: status code %d", rsp.StatusCode)
	}
	var resp proto.PermissionGrantResponse
	if err := json.NewDecoder(rsp.Body).Decode(&resp); err != nil {
		return false, fmt.Errorf("failed to decode grant permission response: %w", err)
	}
	return resp.Resolved, nil
}

// AnswerQuestion answers a pending question on a workspace. The
// returned bool reports whether this call resolved the pending
// request (true) or found it already resolved by a previous caller
// (false). A false value is not an error — it just means another
// subscriber resolved the same request first.
func (c *Client) AnswerQuestion(ctx context.Context, id string, req proto.QuestionAnswer) (bool, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/questions/answer", id), nil, jsonBody(req), jsonHeader())
	if err != nil {
		return false, fmt.Errorf("failed to answer question: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to answer question: status code %d", rsp.StatusCode)
	}
	var resp proto.QuestionAnswerResponse
	if err := json.NewDecoder(rsp.Body).Decode(&resp); err != nil {
		return false, fmt.Errorf("failed to decode answer question response: %w", err)
	}
	return resp.Resolved, nil
}

// SetPermissionsSkipRequests sets the skip-requests flag for a workspace.
func (c *Client) SetPermissionsSkipRequests(ctx context.Context, id string, skip bool) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/permissions/skip", id), nil, jsonBody(proto.PermissionSkipRequest{Skip: skip}), jsonHeader())
	return checkResponse(rsp, err, "set permissions skip requests")
}

// GetPermissionsSkipRequests retrieves the skip-requests flag for a workspace.
func (c *Client) GetPermissionsSkipRequests(ctx context.Context, id string) (bool, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/permissions/skip", id), nil, nil)
	if err != nil {
		return false, fmt.Errorf("failed to get permissions skip requests: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to get permissions skip requests: status code %d", rsp.StatusCode)
	}
	var skip proto.PermissionSkipRequest
	if err := json.NewDecoder(rsp.Body).Decode(&skip); err != nil {
		return false, fmt.Errorf("failed to decode permissions skip requests: %w", err)
	}
	return skip.Skip, nil
}

// SetPermissionsSysadminMode toggles ephemeral sysadmin mode for a workspace.
func (c *Client) SetPermissionsSysadminMode(ctx context.Context, id string, enabled bool) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/permissions/sysadmin", id), nil, jsonBody(proto.PermissionSysadminRequest{Sysadmin: enabled}), jsonHeader())
	return checkResponse(rsp, err, "set permissions sysadmin mode")
}

// GetPermissionsSysadminMode retrieves the sysadmin mode flag for a workspace.
func (c *Client) GetPermissionsSysadminMode(ctx context.Context, id string) (bool, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/permissions/sysadmin", id), nil, nil)
	if err != nil {
		return false, fmt.Errorf("failed to get permissions sysadmin mode: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to get permissions sysadmin mode: status code %d", rsp.StatusCode)
	}
	var req proto.PermissionSysadminRequest
	if err := json.NewDecoder(rsp.Body).Decode(&req); err != nil {
		return false, fmt.Errorf("failed to decode permissions sysadmin mode: %w", err)
	}
	return req.Sysadmin, nil
}

// ReloadConfig reloads the workspace config from disk on the server.
func (c *Client) ReloadConfig(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/config/reload", id), nil, nil, nil)
	return checkResponse(rsp, err, "reload config")
}

// GetConfig retrieves the workspace-specific configuration.
func (c *Client) GetConfig(ctx context.Context, id string) (*config.Config, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/config", id), nil, nil)
	return decodeResponse[*config.Config](rsp, err, "get config")
}

func jsonBody(v any) *bytes.Buffer {
	b := new(bytes.Buffer)
	m, _ := json.Marshal(v)
	b.Write(m)
	return b
}

// SaveSession updates a session in a workspace, returning a proto type.
func (c *Client) SaveSession(ctx context.Context, id string, sess proto.Session) (*proto.Session, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.put(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s", id, sess.ID), nil, jsonBody(sess), jsonHeader())
	return decodeResponse[*proto.Session](rsp, err, "save session")
}

// DeleteSession deletes a session from a workspace.
func (c *Client) DeleteSession(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.delete(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s", id, sessionID), nil, nil)
	return checkResponse(rsp, err, "delete session")
}

// detachedWorkspacePathID is the placeholder used in the {id} path
// segment when routing a session write to a DETACHED workspace, which has
// no server-side workspace id. The backend resolves such a request by the
// "root" query parameter instead. It is a non-UUID string, so it can never
// collide with a real (UUID) attached workspace id.
const detachedWorkspacePathID = "-"

// workspacePathID returns the {id} path segment for a session-write route,
// substituting the detached placeholder when the workspace id is empty (a
// detached workspace routed by root).
func workspacePathID(id string) string {
	if id == "" {
		return detachedWorkspacePathID
	}
	return id
}

// rootQuery builds the optional ?root= query used to route a session write
// to a detached workspace.
func rootQuery(root string) url.Values {
	if root == "" {
		return nil
	}
	q := url.Values{}
	q.Set("root", root)
	return q
}

// ArchiveSession archives a session in a workspace. When id is empty the
// request is routed to the detached workspace at root.
func (c *Client) ArchiveSession(ctx context.Context, id, root, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/archive", workspacePathID(id), sessionID), rootQuery(root), nil, nil)
	return checkResponse(rsp, err, "archive session")
}

// MarkSessionSeen marks a session as read in a workspace. When id is empty
// the request is routed to the detached workspace at root.
func (c *Client) MarkSessionSeen(ctx context.Context, id, root, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/seen", workspacePathID(id), sessionID), rootQuery(root), nil, nil)
	return checkResponse(rsp, err, "mark session seen")
}

// SetSessionFavorite pins or unpins a session in a workspace. When id is
// empty the request is routed to the detached workspace at root.
func (c *Client) SetSessionFavorite(ctx context.Context, id, root, sessionID string, favorite bool) error {
	q := rootQuery(root)
	if q == nil {
		q = url.Values{}
	}
	q.Set("favorite", strconv.FormatBool(favorite))
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/favorite", workspacePathID(id), sessionID), q, nil, nil)
	return checkResponse(rsp, err, "set session favorite")
}

// UnarchiveSession unarchives a session in a workspace. When id is empty
// the request is routed to the detached workspace at root.
func (c *Client) UnarchiveSession(ctx context.Context, id, root, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/unarchive", workspacePathID(id), sessionID), rootQuery(root), nil, nil)
	return checkResponse(rsp, err, "unarchive session")
}

// ListArchivedSessions retrieves archived sessions for a workspace.
func (c *Client) ListArchivedSessions(ctx context.Context, id string) ([]proto.Session, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/archived", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list archived sessions: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list archived sessions: status code %d", rsp.StatusCode)
	}
	var sessions []proto.Session
	if err := json.NewDecoder(rsp.Body).Decode(&sessions); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to decode archived sessions: %w", err)
	}
	return sessions, nil
}

// ListUserMessages retrieves user-role messages for a session as proto types.
func (c *Client) ListUserMessages(ctx context.Context, id string, sessionID string) ([]proto.Message, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/messages/user", id, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get user messages: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get user messages: status code %d", rsp.StatusCode)
	}
	var msgs []proto.Message
	if err := json.NewDecoder(rsp.Body).Decode(&msgs); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to decode user messages: %w", err)
	}
	return msgs, nil
}

// ListAllUserMessages retrieves all user-role messages across sessions as proto types.
func (c *Client) ListAllUserMessages(ctx context.Context, id string) ([]proto.Message, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/messages/user", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get all user messages: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get all user messages: status code %d", rsp.StatusCode)
	}
	var msgs []proto.Message
	if err := json.NewDecoder(rsp.Body).Decode(&msgs); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to decode all user messages: %w", err)
	}
	return msgs, nil
}

// CancelAgentSession cancels an ongoing agent operation for a session.
func (c *Client) CancelAgentSession(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/cancel", id, sessionID), nil, nil, nil)
	return checkResponse(rsp, err, "cancel agent session")
}

// CancelAgent cancels all ongoing agent operations for a workspace.
func (c *Client) CancelAgent(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/cancel", id), nil, nil, nil)
	return checkResponse(rsp, err, "cancel agent")
}

// GetAgentSessionQueuedPromptsList retrieves the list of queued prompt
// strings for a session.
func (c *Client) GetAgentSessionQueuedPromptsList(ctx context.Context, id string, sessionID string) ([]string, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/prompts/list", id, sessionID), nil, nil)
	return decodeResponse[[]string](rsp, err, "get queued prompts list")
}

// GetDefaultSmallModel retrieves the default small model for a provider.
func (c *Client) GetDefaultSmallModel(ctx context.Context, id string, providerID string) (*config.SelectedModel, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/default-small-model", id), url.Values{"provider_id": []string{providerID}}, nil)
	return decodeResponse[*config.SelectedModel](rsp, err, "get default small model")
}

// FileTrackerRecordRead records a file read for a session.
func (c *Client) FileTrackerRecordRead(ctx context.Context, id string, sessionID, path string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/filetracker/read", id), nil, jsonBody(struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
	}{SessionID: sessionID, Path: path}), jsonHeader())
	return checkResponse(rsp, err, "record file read")
}

// FileTrackerLastReadTime returns the last read time for a file in a
// session.
func (c *Client) FileTrackerLastReadTime(ctx context.Context, id string, sessionID, path string) (time.Time, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/filetracker/lastread", id), url.Values{
		"session_id": []string{sessionID},
		"path":       []string{path},
	}, nil)
	return decodeResponse[time.Time](rsp, err, "get last read time")
}

// FileTrackerListReadFiles returns the list of read files for a session.
func (c *Client) FileTrackerListReadFiles(ctx context.Context, id string, sessionID string) ([]string, error) {
	//nolint:bodyclose // body closed by checkResponse/decodeResponse
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/filetracker/files", id, sessionID), nil, nil)
	return decodeResponse[[]string](rsp, err, "get read files")
}

// LSPStart starts an LSP server for a path.
func (c *Client) LSPStart(ctx context.Context, id string, path string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/lsps/start", id), nil, jsonBody(struct {
		Path string `json:"path"`
	}{Path: path}), jsonHeader())
	return checkResponse(rsp, err, "start LSP")
}

// LSPStopAll stops all LSP servers for a workspace.
func (c *Client) LSPStopAll(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/lsps/stop", id), nil, nil, nil)
	return checkResponse(rsp, err, "stop LSPs")
}

// DecodeEventStream decodes an SSE event stream (wire 1 or 2) from body
// into typed events until it ends. Exposed for tests and tools.
func DecodeEventStream(ctx context.Context, body io.Reader) <-chan any {
	events := make(chan any, 100)
	go func() {
		defer close(events)
		streamEvents(ctx, body, events, readErrorRetryDelay)
	}()
	return events
}

// SessionBundle fetches a session with its messages, nested agent-tool
// sessions, and file history in one request.
func (c *Client) SessionBundle(ctx context.Context, params proto.SessionBundleParams) (proto.SessionBundle, error) {
	return clusterCall[proto.SessionBundle](ctx, c, http.MethodPost, "/session-bundle", params)
}

// SessionBatch applies an operation to many sessions in one request.
func (c *Client) SessionBatch(ctx context.Context, req proto.SessionBatchRequest) (proto.SessionBatchResult, error) {
	return clusterCall[proto.SessionBatchResult](ctx, c, http.MethodPost, "/sessions/batch", req)
}
