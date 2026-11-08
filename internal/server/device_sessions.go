package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/proto"
)

// Session-addressed routes (docs/specs/MOBILE_DEVICE_BRIDGE.md, Session
// routes). They name a session by ID, plus optionally its workspace root,
// so devices never depend on runtime workspace IDs.

// queryLimit parses the limit query parameter; 0 means the default.
func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get(proto.SessionOverviewsParamLimit)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		jsonError(w, http.StatusBadRequest, "limit must be a positive integer")
		return 0, false
	}
	return n, true
}

// handleGetSessionOverviews lists every session of the cluster (or, with
// local, this node), attached or detached, newest first.
//
//	@Summary		List session overviews
//	@Description	Flat, cluster-wide session list sorted by updated_at desc then id, paged by an opaque keyset cursor. With since, only the rows changed since that sync cursor plus tombstones (delta sync).
//	@Tags			sessions
//	@Produce		json
//	@Param			limit	query		int		false	"Page size (1..200, default 50)"
//	@Param			cursor	query		string	false	"Opaque cursor from next_cursor"
//	@Param			local	query		string	false	"Only this node's sessions"
//	@Param			since	query		string	false	"Opaque sync_cursor of an earlier response: return only changes since it"
//	@Param			fields	query		string	false	"row: slim rows (no data_dir); cursor: only sync_cursor, no rows"	Enums(row,cursor)
//	@Success		200		{object}	proto.DeviceSessionOverviewPage
//	@Failure		400		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"sessions:read"
//	@Router			/session-overviews [get]
func (c *controllerV1) handleGetSessionOverviews(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	fields := proto.SessionOverviewFields(q.Get(proto.SessionOverviewsParamFields))
	if fields != "" && fields != proto.SessionOverviewFieldsRow && fields != proto.SessionOverviewFieldsCursor {
		jsonError(w, http.StatusBadRequest, "fields must be row or cursor")
		return
	}
	page, err := c.backend.ListSessionOverviews(r.Context(), backend.SessionOverviewQuery{
		Local:  q.Get(proto.SessionOverviewsParamLocal) != "",
		Limit:  limit,
		Cursor: q.Get(proto.SessionOverviewsParamCursor),
		Since:  q.Get(proto.SessionOverviewsParamSince),
		Fields: fields,
	})
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	// Node-to-node fan-out and HTTP clients ask for gzip; the device
	// bridge does not, and gets plain JSON.
	jsonEncodeGzip(w, r, page)
}

// handleGetDeviceSession returns a session's metadata by session ID.
//
//	@Summary		Get session by id
//	@Tags			sessions
//	@Produce		json
//	@Param			sid		path		string	true	"Session ID"
//	@Param			root	query		string	false	"Workspace root"
//	@Success		200		{object}	proto.DeviceSession
//	@Failure		404		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"sessions:read"
//	@Router			/sessions/{sid} [get]
func (c *controllerV1) handleGetDeviceSession(w http.ResponseWriter, r *http.Request) {
	sess, err := c.backend.DeviceSession(r.Context(), r.PathValue("sid"), r.URL.Query().Get("root"))
	c.respond(w, r, sess, err)
}

// handleGetDeviceSessionMessages returns a page of a session's messages
// by session ID, in chronological order.
//
//	@Summary		Get session messages by session id
//	@Tags			sessions
//	@Produce		json
//	@Param			sid		path		string	true	"Session ID"
//	@Param			root	query		string	false	"Workspace root"
//	@Param			limit	query		int		false	"Page size (1..500, default 200)"
//	@Param			before	query		string	false	"Return messages older than this message id"
//	@Success		200		{object}	proto.DeviceSessionMessages
//	@Failure		400		{object}	proto.Error
//	@Failure		404		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"sessions:read"
//	@Router			/sessions/{sid}/messages [get]
func (c *controllerV1) handleGetDeviceSessionMessages(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	msgs, nextBefore, wsID, err := c.backend.DeviceSessionMessages(r.Context(), r.PathValue("sid"), q.Get("root"), limit, q.Get("before"))
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	out := proto.DeviceSessionMessages{Messages: messagesToProto(msgs), WorkspaceID: wsID}
	if out.Messages == nil {
		out.Messages = []proto.Message{}
	}
	if nextBefore != "" {
		out.NextBefore = &nextBefore
	}
	jsonEncode(w, out)
}

// handlePostDeviceSessionOpen attaches a session's registry-known
// workspace on this node if needed and returns its runtime workspace ID.
//
//	@Summary		Open a session's workspace
//	@Tags			sessions
//	@Accept			json
//	@Produce		json
//	@Param			sid		path		string							true	"Session ID"
//	@Param			request	body		proto.DeviceSessionOpenParams	true	"Workspace root"
//	@Success		200		{object}	proto.DeviceSessionOpen
//	@Failure		400		{object}	proto.Error
//	@Failure		404		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"sessions:prompt"
//	@Router			/sessions/{sid}/open [post]
func (c *controllerV1) handlePostDeviceSessionOpen(w http.ResponseWriter, r *http.Request) {
	params, ok := decodeJSON[proto.DeviceSessionOpenParams](c, w, r)
	if !ok {
		return
	}
	if params.Root == "" {
		jsonError(w, http.StatusBadRequest, "root is required")
		return
	}
	res, err := c.backend.OpenDeviceSession(r.Context(), r.PathValue("sid"), params.Root)
	c.respond(w, r, res, err)
}

// handlePostDeviceSession creates a session in a registry-known workspace
// root and sends its first prompt.
//
//	@Summary		Create a session and prompt it
//	@Tags			sessions
//	@Accept			json
//	@Produce		json
//	@Param			request	body		proto.DeviceCreateSessionParams	true	"Workspace root, first prompt, optional model"
//	@Success		200		{object}	proto.DeviceCreatedSession
//	@Failure		400		{object}	proto.Error
//	@Failure		404		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"sessions:prompt"
//	@Router			/sessions [post]
func (c *controllerV1) handlePostDeviceSession(w http.ResponseWriter, r *http.Request) {
	params, ok := decodeJSON[proto.DeviceCreateSessionParams](c, w, r)
	if !ok {
		return
	}
	if params.Root == "" || strings.TrimSpace(params.Prompt) == "" {
		jsonError(w, http.StatusBadRequest, "root and prompt are required")
		return
	}
	res, err := c.backend.CreateDeviceSession(r.Context(), params)
	c.respond(w, r, res, err)
}

// handlePostNodeHistorySearch searches the history of every workspace on
// this node, attached or detached.
//
//	@Summary		Search history across this node
//	@Tags			sessions
//	@Accept			json
//	@Produce		json
//	@Param			request	body		proto.NodeSearchHistoryParams	true	"Query, limit, scope"
//	@Success		200		{object}	proto.NodeSearchHistoryResult
//	@Failure		400		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"sessions:read"
//	@Router			/history/search [post]
func (c *controllerV1) handlePostNodeHistorySearch(w http.ResponseWriter, r *http.Request) {
	params, ok := decodeJSON[proto.NodeSearchHistoryParams](c, w, r)
	if !ok {
		return
	}
	res, err := c.backend.SearchNodeHistory(r.Context(), params)
	c.respond(w, r, res, err)
}

// handleGetWorkspaceRoots lists this node's registry-known workspace
// roots, most recently used first.
//
//	@Summary		List workspace roots
//	@Tags			workspaces
//	@Produce		json
//	@Success		200	{object}	proto.WorkspaceRoots
//	@Failure		500	{object}	proto.Error
//	@x-crush-scope	"sessions:read"
//	@Router			/workspace-roots [get]
func (c *controllerV1) handleGetWorkspaceRoots(w http.ResponseWriter, r *http.Request) {
	roots, err := c.backend.WorkspaceRoots(r.Context())
	c.respond(w, r, roots, err)
}
