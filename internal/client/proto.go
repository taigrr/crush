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
	rsp, err := c.get(ctx, "/workspaces", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list workspaces: status code %d", rsp.StatusCode)
	}
	var workspaces []proto.Workspace
	if err := json.NewDecoder(rsp.Body).Decode(&workspaces); err != nil {
		return nil, fmt.Errorf("failed to decode workspaces: %w", err)
	}
	return workspaces, nil
}

// ListWorkspaceOverviews lists all known workspaces (attached and
// registry-known) with their sessions for the cross-workspace picker.
func (c *Client) ListWorkspaceOverviews(ctx context.Context) ([]proto.WorkspaceOverview, error) {
	rsp, err := c.get(ctx, "/workspace-overviews", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace overviews: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list workspace overviews: status code %d", rsp.StatusCode)
	}
	var overviews []proto.WorkspaceOverview
	if err := json.NewDecoder(rsp.Body).Decode(&overviews); err != nil {
		return nil, fmt.Errorf("failed to decode workspace overviews: %w", err)
	}
	return overviews, nil
}

// CreateWorkspace creates a new workspace on the server.
func (c *Client) CreateWorkspace(ctx context.Context, ws proto.Workspace) (*proto.Workspace, error) {
	ws.ClientID = c.clientID
	rsp, err := c.post(ctx, "/workspaces", nil, jsonBody(ws), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to create workspace: status code %d", rsp.StatusCode)
	}
	var created proto.Workspace
	if err := json.NewDecoder(rsp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("failed to decode workspace: %w", err)
	}
	return &created, nil
}

// GetWorkspace retrieves a workspace from the server.
func (c *Client) GetWorkspace(ctx context.Context, id string) (*proto.Workspace, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get workspace: status code %d", rsp.StatusCode)
	}
	var ws proto.Workspace
	if err := json.NewDecoder(rsp.Body).Decode(&ws); err != nil {
		return nil, fmt.Errorf("failed to decode workspace: %w", err)
	}
	return &ws, nil
}

// DeleteWorkspace deletes a workspace on the server.
func (c *Client) DeleteWorkspace(ctx context.Context, id string) error {
	q := url.Values{"client_id": []string{c.clientID}}
	rsp, err := c.delete(ctx, fmt.Sprintf("/workspaces/%s", id), q, nil)
	if err != nil {
		return fmt.Errorf("failed to delete workspace: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete workspace: status code %d", rsp.StatusCode)
	}
	return nil
}

// SetCurrentSession reports the client's current-session selection
// for the named workspace. An empty sessionID clears the entry. The
// request carries the process-scoped client ID minted in [NewClient]
// as a query parameter so the server can route the update to the
// correct [clientState] entry.
func (c *Client) SetCurrentSession(ctx context.Context, workspaceID, sessionID string) error {
	q := url.Values{"client_id": []string{c.clientID}}
	rsp, err := c.post(
		ctx,
		fmt.Sprintf("/workspaces/%s/current-session", workspaceID),
		q,
		jsonBody(proto.CurrentSession{SessionID: sessionID}),
		http.Header{"Content-Type": []string{"application/json"}},
	)
	if err != nil {
		return fmt.Errorf("failed to set current session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set current session: status code %d", rsp.StatusCode)
	}
	return nil
}

// SubscribeEvents subscribes to server-sent events for a workspace.
func (c *Client) SubscribeEvents(ctx context.Context, id string) (<-chan any, error) {
	events := make(chan any, 100)
	q := url.Values{"client_id": []string{c.clientID}}
	//nolint:bodyclose
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/events", id), q, http.Header{
		"Accept":        []string{"text/event-stream"},
		"Cache-Control": []string{"no-cache"},
		"Connection":    []string{"keep-alive"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to events: %w", err)
	}

	if rsp.StatusCode != http.StatusOK {
		rsp.Body.Close()
		return nil, fmt.Errorf("failed to subscribe to events: status code %d", rsp.StatusCode)
	}

	go func() {
		defer rsp.Body.Close()
		defer close(events)
		streamEvents(ctx, rsp.Body, events, readErrorRetryDelay)
	}()

	return events, nil
}

// SubscribeGlobalEvents subscribes to the server's observe-only global
// attention stream (GET /v1/events): cross-workspace permission/question
// blocked+resolved and agent busy/idle transitions. Unlike
// SubscribeEvents it is not scoped to a workspace and does not attach the
// client to (or pin alive) any workspace.
func (c *Client) SubscribeGlobalEvents(ctx context.Context) (<-chan any, error) {
	events := make(chan any, 100)
	q := url.Values{"client_id": []string{c.clientID}}
	//nolint:bodyclose
	rsp, err := c.get(ctx, "/events", q, http.Header{
		"Accept":        []string{"text/event-stream"},
		"Cache-Control": []string{"no-cache"},
		"Connection":    []string{"keep-alive"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to global events: %w", err)
	}

	if rsp.StatusCode != http.StatusOK {
		rsp.Body.Close()
		return nil, fmt.Errorf("failed to subscribe to global events: status code %d", rsp.StatusCode)
	}

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
	for {
		line, readErr := scr.ReadBytes('\n')
		// ReadBytes returns a final, newline-less line together with
		// io.EOF, so parse what we got before deciding to break;
		// otherwise the last event in the stream would be dropped.
		if ev, ok := parseSSELine(line); ok {
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
	return decodeEvent(p)
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
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/lsps/%s/diagnostics", id, lspName), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get LSP diagnostics: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get LSP diagnostics: status code %d", rsp.StatusCode)
	}
	var diagnostics map[protocol.DocumentURI][]protocol.Diagnostic
	if err := json.NewDecoder(rsp.Body).Decode(&diagnostics); err != nil {
		return nil, fmt.Errorf("failed to decode LSP diagnostics: %w", err)
	}
	return diagnostics, nil
}

// GetLSPs retrieves the LSP client states for a workspace.
func (c *Client) GetLSPs(ctx context.Context, id string) (map[string]proto.LSPClientInfo, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/lsps", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get LSPs: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get LSPs: status code %d", rsp.StatusCode)
	}
	var lsps map[string]proto.LSPClientInfo
	if err := json.NewDecoder(rsp.Body).Decode(&lsps); err != nil {
		return nil, fmt.Errorf("failed to decode LSPs: %w", err)
	}
	return lsps, nil
}

// MCPGetStates retrieves the MCP client states for a workspace.
func (c *Client) MCPGetStates(ctx context.Context, id string) (map[string]proto.MCPClientInfo, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/mcp/states", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP states: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get MCP states: status code %d", rsp.StatusCode)
	}
	var states map[string]proto.MCPClientInfo
	if err := json.NewDecoder(rsp.Body).Decode(&states); err != nil {
		return nil, fmt.Errorf("failed to decode MCP states: %w", err)
	}
	return states, nil
}

// SkillsGetStates retrieves the skill discovery states for a workspace.
func (c *Client) SkillsGetStates(ctx context.Context, id string) ([]proto.SkillState, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/skills/states", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get skill states: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get skill states: status code %d", rsp.StatusCode)
	}
	var states []proto.SkillState
	if err := json.NewDecoder(rsp.Body).Decode(&states); err != nil {
		return nil, fmt.Errorf("failed to decode skill states: %w", err)
	}
	return states, nil
}

// MCPRefreshPrompts refreshes prompts for a named MCP client.
func (c *Client) MCPRefreshPrompts(ctx context.Context, id, name string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/mcp/refresh-prompts", id), nil,
		jsonBody(struct {
			Name string `json:"name"`
		}{Name: name}),
		http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to refresh MCP prompts: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to refresh MCP prompts: status code %d", rsp.StatusCode)
	}
	return nil
}

// MCPRefreshResources refreshes resources for a named MCP client.
func (c *Client) MCPRefreshResources(ctx context.Context, id, name string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/mcp/refresh-resources", id), nil,
		jsonBody(struct {
			Name string `json:"name"`
		}{Name: name}),
		http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to refresh MCP resources: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to refresh MCP resources: status code %d", rsp.StatusCode)
	}
	return nil
}

// GetAgentSessionQueuedPrompts retrieves the number of queued prompts for a
// session.
func (c *Client) GetAgentSessionQueuedPrompts(ctx context.Context, id string, sessionID string) (int, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/prompts/queued", id, sessionID), nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get session agent queued prompts: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to get session agent queued prompts: status code %d", rsp.StatusCode)
	}
	var count int
	if err := json.NewDecoder(rsp.Body).Decode(&count); err != nil {
		return 0, fmt.Errorf("failed to decode session agent queued prompts: %w", err)
	}
	return count, nil
}

// ClearAgentSessionQueuedPrompts clears the queued prompts for a session.
func (c *Client) ClearAgentSessionQueuedPrompts(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/prompts/clear", id, sessionID), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to clear session agent queued prompts: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to clear session agent queued prompts: status code %d", rsp.StatusCode)
	}
	return nil
}

// EmbeddingsPending returns how many past messages would be embedded by
// a backfill under the active embedding model.
func (c *Client) EmbeddingsPending(ctx context.Context, id string) (int, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/embeddings/pending", id), nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get pending embeddings: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to get pending embeddings: status code %d", rsp.StatusCode)
	}
	var count int
	if err := json.NewDecoder(rsp.Body).Decode(&count); err != nil {
		return 0, fmt.Errorf("failed to decode pending embeddings: %w", err)
	}
	return count, nil
}

// BackfillEmbeddings embeds past messages lacking a vector and returns
// the count embedded.
func (c *Client) BackfillEmbeddings(ctx context.Context, id string) (int, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/embeddings/backfill", id), nil, nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to backfill embeddings: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to backfill embeddings: status code %d", rsp.StatusCode)
	}
	var count int
	if err := json.NewDecoder(rsp.Body).Decode(&count); err != nil {
		return 0, fmt.Errorf("failed to decode backfill result: %w", err)
	}
	return count, nil
}

// EmbeddingStatus returns the embedding index state for a workspace.
func (c *Client) EmbeddingStatus(ctx context.Context, id string) (proto.EmbeddingStatus, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/embeddings/status", id), nil, nil)
	if err != nil {
		return proto.EmbeddingStatus{}, fmt.Errorf("failed to get embedding status: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return proto.EmbeddingStatus{}, fmt.Errorf("failed to get embedding status: status code %d", rsp.StatusCode)
	}
	var status proto.EmbeddingStatus
	if err := json.NewDecoder(rsp.Body).Decode(&status); err != nil {
		return proto.EmbeddingStatus{}, fmt.Errorf("failed to decode embedding status: %w", err)
	}
	return status, nil
}

// SearchHistory runs hybrid history search over a workspace and returns
// per-session hits.
func (c *Client) SearchHistory(ctx context.Context, id string, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/history/search", id), nil, jsonBody(params),
		http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return proto.SearchHistoryResult{}, fmt.Errorf("failed to search history: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return proto.SearchHistoryResult{}, fmt.Errorf("failed to search history: status code %d", rsp.StatusCode)
	}
	var res proto.SearchHistoryResult
	if err := json.NewDecoder(rsp.Body).Decode(&res); err != nil {
		return proto.SearchHistoryResult{}, fmt.Errorf("failed to decode search result: %w", err)
	}
	return res, nil
}

// PeekMessages returns a session's messages from the workspace rooted at
// root (attached or registry-detached), without switching the caller's
// own workspace. Used by the session sidebar's live preview for a
// session outside the currently-attached workspace.
func (c *Client) PeekMessages(ctx context.Context, root, sessionID string) ([]proto.Message, error) {
	rsp, err := c.post(ctx, "/peek-messages", nil, jsonBody(proto.PeekMessagesParams{Root: root, SessionID: sessionID}),
		http.Header{"Content-Type": []string{"application/json"}})
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
		http.Header{"Content-Type": []string{"application/json"}})
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
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/goal", id, sessionID), nil, nil)
	if err != nil {
		return proto.GoalStatus{}, fmt.Errorf("failed to get session goal: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return proto.GoalStatus{}, fmt.Errorf("failed to get session goal: status code %d", rsp.StatusCode)
	}
	var status proto.GoalStatus
	if err := json.NewDecoder(rsp.Body).Decode(&status); err != nil {
		return proto.GoalStatus{}, fmt.Errorf("failed to decode session goal: %w", err)
	}
	return status, nil
}

// SetAgentSessionGoal sets (or clears, when condition is blank) the
// autonomous goal for a session.
func (c *Client) SetAgentSessionGoal(ctx context.Context, id, sessionID, condition string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/goal", id, sessionID), nil, jsonBody(proto.SetGoalRequest{
		Condition: condition,
	}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to set session goal: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set session goal: status code %d", rsp.StatusCode)
	}
	return nil
}

// SetAgentSessionWorkingDir sets the working directory tools run in for a
// session.
func (c *Client) SetAgentSessionWorkingDir(ctx context.Context, id, sessionID, dir string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/cwd", id, sessionID), nil, jsonBody(proto.SetWorkingDirRequest{
		WorkingDir: dir,
	}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to set session working dir: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set session working dir: status code %d", rsp.StatusCode)
	}
	return nil
}

// ClearAgentSessionGoal clears the active autonomous goal for a session.
func (c *Client) ClearAgentSessionGoal(ctx context.Context, id, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/goal/clear", id, sessionID), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to clear session goal: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to clear session goal: status code %d", rsp.StatusCode)
	}
	return nil
}

// GetAgentInfo retrieves the agent status for a workspace.
func (c *Client) GetAgentInfo(ctx context.Context, id string) (*proto.AgentInfo, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent status: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get agent status: status code %d", rsp.StatusCode)
	}
	var info proto.AgentInfo
	if err := json.NewDecoder(rsp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode agent status: %w", err)
	}
	return &info, nil
}

// UpdateAgent triggers an agent model update on the server.
func (c *Client) UpdateAgent(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/update", id), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to update agent: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to update agent: status code %d", rsp.StatusCode)
	}
	return nil
}

// SendMessage sends a message to the agent for a workspace.
//
// When runID is non-empty it is echoed back on the resulting
// proto.RunComplete event, giving the caller a unique correlator
// for completion detection. Pass "" when the caller does not need
// to distinguish its own turn's terminal event from any concurrent
// turn on the same session (e.g. interactive TUI usage).
func (c *Client) SendMessage(ctx context.Context, id string, sessionID, runID, prompt string, attachments ...message.Attachment) error {
	return c.sendAgentMessage(ctx, id, proto.AgentMessage{
		SessionID:   sessionID,
		RunID:       runID,
		Prompt:      prompt,
		Attachments: proto.AttachmentsFromMessage(attachments),
	})
}

// SteerMessage sends a mid-turn steering message: it is queued behind the
// session's active turn with no RunID (so it folds into that turn at the
// next step rather than waiting for its own) and raises the session's
// soft interrupt so long-running tools wrap up early. On an idle session
// it behaves like SendMessage with an empty RunID.
func (c *Client) SteerMessage(ctx context.Context, id string, sessionID, prompt string) error {
	return c.sendAgentMessage(ctx, id, proto.AgentMessage{
		SessionID: sessionID,
		Prompt:    prompt,
		Steer:     true,
	})
}

func (c *Client) sendAgentMessage(ctx context.Context, id string, msg proto.AgentMessage) error {
	msg.ClientID = c.clientID
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent", id), nil, jsonBody(msg), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to send message to agent: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK && rsp.StatusCode != http.StatusAccepted {
		e := decodeError(rsp.Body)
		if rsp.StatusCode == http.StatusServiceUnavailable && e.Code == proto.ErrorCodeDraining {
			return ErrServerDraining
		}
		if e.Message != "" {
			return fmt.Errorf("failed to send message to agent: status code %d: %s", rsp.StatusCode, e.Message)
		}
		return fmt.Errorf("failed to send message to agent: status code %d", rsp.StatusCode)
	}
	return nil
}

// ErrServerDraining is returned by SendMessage when the server refused
// the prompt because it is draining for an update. The prompt was not
// accepted; callers should hold it and retry once a server is back.
var ErrServerDraining = errors.New("server is updating; prompt not accepted")

// RunShellCommand runs a shell command in the workspace without triggering the agent.
func (c *Client) RunShellCommand(ctx context.Context, id, sessionID, command string) (proto.ShellCommandResponse, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/shell", id, sessionID), nil, jsonBody(proto.ShellCommandRequest{
		Command:  command,
		ClientID: c.clientID,
	}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return proto.ShellCommandResponse{}, fmt.Errorf("failed to run shell command: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return proto.ShellCommandResponse{}, fmt.Errorf("failed to run shell command: status code %d", rsp.StatusCode)
	}
	var resp proto.ShellCommandResponse
	if err := json.NewDecoder(rsp.Body).Decode(&resp); err != nil {
		return proto.ShellCommandResponse{}, fmt.Errorf("failed to decode shell command response: %w", err)
	}
	return resp, nil
}

// decodeErrorMessage attempts to decode the response body as a
// proto.Error and returns its message. It returns an empty string
// when the body is empty or cannot be decoded into a proto.Error
// with a non-empty message, letting callers fall back to a
// status-only error.
func decodeErrorMessage(body io.Reader) string {
	return decodeError(body).Message
}

// decodeError decodes a proto.Error body, returning the zero value when
// the body is not one.
func decodeError(body io.Reader) proto.Error {
	var e proto.Error
	if err := json.NewDecoder(body).Decode(&e); err != nil {
		return proto.Error{}
	}
	return e
}

// GetAgentSessionInfo retrieves the agent session info for a workspace.
func (c *Client) GetAgentSessionInfo(ctx context.Context, id string, sessionID string) (*proto.AgentSession, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s", id, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get session agent info: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get session agent info: status code %d", rsp.StatusCode)
	}
	var info proto.AgentSession
	if err := json.NewDecoder(rsp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode session agent info: %w", err)
	}
	return &info, nil
}

// AgentSummarizeSession requests a session summarization.
func (c *Client) AgentSummarizeSession(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/summarize", id, sessionID), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to summarize session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to summarize session: status code %d", rsp.StatusCode)
	}
	return nil
}

// AgentGenerateTitle requests a session title regeneration.
func (c *Client) AgentGenerateTitle(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/title", id, sessionID), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to regenerate session title: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to regenerate session title: status code %d", rsp.StatusCode)
	}
	return nil
}

// InitiateAgentProcessing triggers agent initialization on the server.
func (c *Client) InitiateAgentProcessing(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/init", id), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to initiate session agent processing: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to initiate session agent processing: status code %d", rsp.StatusCode)
	}
	return nil
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
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s", id, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get session: status code %d", rsp.StatusCode)
	}
	var sess proto.Session
	if err := json.NewDecoder(rsp.Body).Decode(&sess); err != nil {
		return nil, fmt.Errorf("failed to decode session: %w", err)
	}
	return &sess, nil
}

// ListSessionHistoryFiles retrieves history files for a session as proto types.
func (c *Client) ListSessionHistoryFiles(ctx context.Context, id string, sessionID string) ([]proto.File, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/history", id, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get session history files: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get session history files: status code %d", rsp.StatusCode)
	}
	var files []proto.File
	if err := json.NewDecoder(rsp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("failed to decode session history files: %w", err)
	}
	return files, nil
}

// CreateSession creates a new session in a workspace as a proto type.
func (c *Client) CreateSession(ctx context.Context, id string, title string) (*proto.Session, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions", id), nil, jsonBody(proto.Session{Title: title}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
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
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get sessions: status code %d", rsp.StatusCode)
	}
	var sessions []proto.Session
	if err := json.NewDecoder(rsp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("failed to decode sessions: %w", err)
	}
	return sessions, nil
}

func (c *Client) ListSessionImportSources(ctx context.Context) ([]proto.SessionImportSources, error) {
	rsp, err := c.get(ctx, "/session-import/sources", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list session import sources: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list session import sources: status code %d", rsp.StatusCode)
	}
	var sources []proto.SessionImportSources
	if err := json.NewDecoder(rsp.Body).Decode(&sources); err != nil {
		return nil, fmt.Errorf("failed to decode session import sources: %w", err)
	}
	return sources, nil
}

func (c *Client) DiscoverSessionImports(ctx context.Context, source string) ([]proto.SessionImportCandidate, error) {
	query := url.Values{}
	query.Set("source", source)
	rsp, err := c.get(ctx, "/session-import/candidates", query, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to discover session imports: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to discover session imports: status code %d", rsp.StatusCode)
	}
	var candidates []proto.SessionImportCandidate
	if err := json.NewDecoder(rsp.Body).Decode(&candidates); err != nil {
		return nil, fmt.Errorf("failed to decode session imports: %w", err)
	}
	return candidates, nil
}

func (c *Client) ImportSessions(ctx context.Context, id string, paths []string, from string) ([]proto.SessionImportResult, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/session-import", id), nil, jsonBody(proto.SessionImportRequest{Paths: paths, Source: from}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return nil, fmt.Errorf("failed to import sessions: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to import sessions: status code %d", rsp.StatusCode)
	}
	var results []proto.SessionImportResult
	if err := json.NewDecoder(rsp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("failed to decode session import results: %w", err)
	}
	return results, nil
}

// GrantPermission grants a permission on a workspace. The returned
// bool reports whether this call resolved the pending request (true)
// or found it already resolved by a previous caller (false). A false
// value is not an error — it just means another subscriber resolved
// the same request first.
func (c *Client) GrantPermission(ctx context.Context, id string, req proto.PermissionGrant) (bool, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/permissions/grant", id), nil, jsonBody(req), http.Header{"Content-Type": []string{"application/json"}})
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
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/questions/answer", id), nil, jsonBody(req), http.Header{"Content-Type": []string{"application/json"}})
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
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/permissions/skip", id), nil, jsonBody(proto.PermissionSkipRequest{Skip: skip}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to set permissions skip requests: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set permissions skip requests: status code %d", rsp.StatusCode)
	}
	return nil
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
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/permissions/sysadmin", id), nil, jsonBody(proto.PermissionSysadminRequest{Sysadmin: enabled}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to set permissions sysadmin mode: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set permissions sysadmin mode: status code %d", rsp.StatusCode)
	}
	return nil
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
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to reload config: status code %d", rsp.StatusCode)
	}
	return nil
}

// GetConfig retrieves the workspace-specific configuration.
func (c *Client) GetConfig(ctx context.Context, id string) (*config.Config, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/config", id), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get config: status code %d", rsp.StatusCode)
	}
	var cfg config.Config
	if err := json.NewDecoder(rsp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}
	return &cfg, nil
}

func jsonBody(v any) *bytes.Buffer {
	b := new(bytes.Buffer)
	m, _ := json.Marshal(v)
	b.Write(m)
	return b
}

// SaveSession updates a session in a workspace, returning a proto type.
func (c *Client) SaveSession(ctx context.Context, id string, sess proto.Session) (*proto.Session, error) {
	rsp, err := c.put(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s", id, sess.ID), nil, jsonBody(sess), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return nil, fmt.Errorf("failed to save session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to save session: status code %d", rsp.StatusCode)
	}
	var saved proto.Session
	if err := json.NewDecoder(rsp.Body).Decode(&saved); err != nil {
		return nil, fmt.Errorf("failed to decode session: %w", err)
	}
	return &saved, nil
}

// DeleteSession deletes a session from a workspace.
func (c *Client) DeleteSession(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.delete(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s", id, sessionID), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete session: status code %d", rsp.StatusCode)
	}
	return nil
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
	if err != nil {
		return fmt.Errorf("failed to archive session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to archive session: status code %d", rsp.StatusCode)
	}
	return nil
}

// MarkSessionSeen marks a session as read in a workspace. When id is empty
// the request is routed to the detached workspace at root.
func (c *Client) MarkSessionSeen(ctx context.Context, id, root, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/seen", workspacePathID(id), sessionID), rootQuery(root), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to mark session seen: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to mark session seen: status code %d", rsp.StatusCode)
	}
	return nil
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
	if err != nil {
		return fmt.Errorf("failed to set session favorite: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set session favorite: status code %d", rsp.StatusCode)
	}
	return nil
}

// UnarchiveSession unarchives a session in a workspace. When id is empty
// the request is routed to the detached workspace at root.
func (c *Client) UnarchiveSession(ctx context.Context, id, root, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/unarchive", workspacePathID(id), sessionID), rootQuery(root), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to unarchive session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to unarchive session: status code %d", rsp.StatusCode)
	}
	return nil
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
	if err != nil {
		return fmt.Errorf("failed to cancel agent session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to cancel agent session: status code %d", rsp.StatusCode)
	}
	return nil
}

// SoftInterruptAgentSession asks the tools running in the session's
// current step to wrap up early without cancelling them: a running
// shell command is handed back to the model as a background job and the
// turn continues. No-op on an idle session.
func (c *Client) SoftInterruptAgentSession(ctx context.Context, id string, sessionID string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/interrupt", id, sessionID), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to soft-interrupt agent session: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		if msg := decodeErrorMessage(rsp.Body); msg != "" {
			return fmt.Errorf("failed to soft-interrupt agent session: %s", msg)
		}
		return fmt.Errorf("failed to soft-interrupt agent session: status code %d", rsp.StatusCode)
	}
	return nil
}

// BackgroundAgentToolCall asks one in-flight tool call to move its work to
// the background so the turn can continue; the tool returns a result
// naming the background job. Fails when the call is unknown, already
// finished, or cannot be backgrounded.
func (c *Client) BackgroundAgentToolCall(ctx context.Context, id, sessionID, toolCallID string) error {
	// Tool-call IDs come from the provider and are not guaranteed to be
	// path-safe.
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/tools/%s/background", id, sessionID, url.PathEscape(toolCallID)), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to background tool call: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		if msg := decodeErrorMessage(rsp.Body); msg != "" {
			return fmt.Errorf("failed to background tool call: %s", msg)
		}
		return fmt.Errorf("failed to background tool call: status code %d", rsp.StatusCode)
	}
	return nil
}

// CancelAgent cancels all ongoing agent operations for a workspace.
func (c *Client) CancelAgent(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/agent/cancel", id), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to cancel agent: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to cancel agent: status code %d", rsp.StatusCode)
	}
	return nil
}

// GetAgentSessionQueuedPromptsList retrieves the list of queued prompt
// strings for a session.
func (c *Client) GetAgentSessionQueuedPromptsList(ctx context.Context, id string, sessionID string) ([]string, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/sessions/%s/prompts/list", id, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get queued prompts list: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get queued prompts list: status code %d", rsp.StatusCode)
	}
	var prompts []string
	if err := json.NewDecoder(rsp.Body).Decode(&prompts); err != nil {
		return nil, fmt.Errorf("failed to decode queued prompts list: %w", err)
	}
	return prompts, nil
}

// GetDefaultSmallModel retrieves the default small model for a provider.
func (c *Client) GetDefaultSmallModel(ctx context.Context, id string, providerID string) (*config.SelectedModel, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/agent/default-small-model", id), url.Values{"provider_id": []string{providerID}}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get default small model: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get default small model: status code %d", rsp.StatusCode)
	}
	var model config.SelectedModel
	if err := json.NewDecoder(rsp.Body).Decode(&model); err != nil {
		return nil, fmt.Errorf("failed to decode default small model: %w", err)
	}
	return &model, nil
}

// FileTrackerRecordRead records a file read for a session.
func (c *Client) FileTrackerRecordRead(ctx context.Context, id string, sessionID, path string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/filetracker/read", id), nil, jsonBody(struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
	}{SessionID: sessionID, Path: path}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to record file read: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to record file read: status code %d", rsp.StatusCode)
	}
	return nil
}

// FileTrackerLastReadTime returns the last read time for a file in a
// session.
func (c *Client) FileTrackerLastReadTime(ctx context.Context, id string, sessionID, path string) (time.Time, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/filetracker/lastread", id), url.Values{
		"session_id": []string{sessionID},
		"path":       []string{path},
	}, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get last read time: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("failed to get last read time: status code %d", rsp.StatusCode)
	}
	var t time.Time
	if err := json.NewDecoder(rsp.Body).Decode(&t); err != nil {
		return time.Time{}, fmt.Errorf("failed to decode last read time: %w", err)
	}
	return t, nil
}

// FileTrackerListReadFiles returns the list of read files for a session.
func (c *Client) FileTrackerListReadFiles(ctx context.Context, id string, sessionID string) ([]string, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sessions/%s/filetracker/files", id, sessionID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get read files: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get read files: status code %d", rsp.StatusCode)
	}
	var files []string
	if err := json.NewDecoder(rsp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("failed to decode read files: %w", err)
	}
	return files, nil
}

// LSPStart starts an LSP server for a path.
func (c *Client) LSPStart(ctx context.Context, id string, path string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/lsps/start", id), nil, jsonBody(struct {
		Path string `json:"path"`
	}{Path: path}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to start LSP: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to start LSP: status code %d", rsp.StatusCode)
	}
	return nil
}

// LSPStopAll stops all LSP servers for a workspace.
func (c *Client) LSPStopAll(ctx context.Context, id string) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/lsps/stop", id), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to stop LSPs: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to stop LSPs: status code %d", rsp.StatusCode)
	}
	return nil
}
