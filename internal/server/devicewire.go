package server

import (
	"encoding/json"
	"net/http"

	"github.com/taigrr/crush/internal/proto"
)

// Device bridge wire format v1 (docs/specs/MOBILE_DEVICE_BRIDGE.md).

// wireVersion is the envelope major version this node speaks.
const wireVersion = 1

// Error codes (MOBILE_DEVICE_BRIDGE.md, Errors). Stable public API.
type bridgeCode string

const (
	codeBadRequest          bridgeCode = "bad_request"
	codeDeviceRevoked       bridgeCode = "device_revoked"
	codeDeviceScopeDenied   bridgeCode = "device_scope_denied"
	codeRouteNotAllowed     bridgeCode = "route_not_allowed"
	codeNotFound            bridgeCode = "not_found"
	codeNodeNotFound        bridgeCode = "node_not_found"
	codeSessionNotOnNode    bridgeCode = proto.ErrCodeSessionNotOnNode
	codeUnknownRoot         bridgeCode = proto.ErrCodeUnknownRoot
	codeInvalidCursor       bridgeCode = proto.ErrCodeInvalidCursor
	codeMethodNotAllowed    bridgeCode = "method_not_allowed"
	codeConflict            bridgeCode = "conflict"
	codeIdempotencyConflict bridgeCode = "idempotency_conflict"
	codePayloadTooLarge     bridgeCode = "payload_too_large"
	codeRateLimited         bridgeCode = "rate_limited"
	codeResumeUnavailable   bridgeCode = "resume_unavailable"
	codeInternal            bridgeCode = "internal"
	codeNodeUnreachable     bridgeCode = "node_unreachable"
	codeUnavailable         bridgeCode = "unavailable"
	codeTimeout             bridgeCode = "timeout"
)

// codeStatus is the status each bridge error code carries.
var codeStatus = map[bridgeCode]int{
	codeBadRequest:          http.StatusBadRequest,
	codeDeviceRevoked:       http.StatusUnauthorized,
	codeDeviceScopeDenied:   http.StatusForbidden,
	codeRouteNotAllowed:     http.StatusForbidden,
	codeNotFound:            http.StatusNotFound,
	codeNodeNotFound:        http.StatusNotFound,
	codeSessionNotOnNode:    http.StatusNotFound,
	codeUnknownRoot:         http.StatusNotFound,
	codeInvalidCursor:       http.StatusBadRequest,
	codeMethodNotAllowed:    http.StatusMethodNotAllowed,
	codeConflict:            http.StatusConflict,
	codeIdempotencyConflict: http.StatusConflict,
	codePayloadTooLarge:     http.StatusRequestEntityTooLarge,
	codeRateLimited:         http.StatusTooManyRequests,
	codeResumeUnavailable:   http.StatusGone,
	codeInternal:            http.StatusInternalServerError,
	codeNodeUnreachable:     http.StatusBadGateway,
	codeUnavailable:         http.StatusServiceUnavailable,
	codeTimeout:             http.StatusGatewayTimeout,
}

// bridgeError is the "error" object of a response envelope.
type bridgeError struct {
	Code         bridgeCode `json:"code"`
	Message      string     `json:"message,omitempty"`
	RetryAfterMS int64      `json:"retry_after_ms,omitempty"`
	Scope        string     `json:"scope,omitempty"`
}

func newBridgeError(code bridgeCode, msg string) *bridgeError {
	return &bridgeError{Code: code, Message: msg}
}

func (e *bridgeError) status() int {
	if s, ok := codeStatus[e.Code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// handlerCodes are the /v1 error codes the bridge passes through to
// devices in place of the status-derived code.
var handlerCodes = map[string]bridgeCode{
	proto.ErrCodeSessionNotOnNode: codeSessionNotOnNode,
	proto.ErrCodeUnknownRoot:      codeUnknownRoot,
	proto.ErrCodeInvalidCursor:    codeInvalidCursor,
}

// codeForStatus derives an error code from a /v1 handler's status.
func codeForStatus(status int) bridgeCode {
	switch {
	case status == http.StatusNotFound:
		return codeNotFound
	case status == http.StatusConflict:
		return codeConflict
	case status == http.StatusRequestEntityTooLarge:
		return codePayloadTooLarge
	case status == http.StatusTooManyRequests:
		return codeRateLimited
	case status == http.StatusMethodNotAllowed:
		return codeMethodNotAllowed
	case status == http.StatusServiceUnavailable:
		return codeUnavailable
	case status == http.StatusGatewayTimeout:
		return codeTimeout
	case status >= 500:
		return codeInternal
	default:
		return codeBadRequest
	}
}

// bridgeTarget routes a request on api.any or a stream open.
type bridgeTarget struct {
	Node  string `json:"node,omitempty"`
	Swarm string `json:"swarm,omitempty"`
}

// bridgeRequest is the request envelope on crush.client.v1.<dev>.api.<node>.
type bridgeRequest struct {
	V              int               `json:"v,omitempty"`
	ID             string            `json:"id"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          json.RawMessage   `json:"query,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	JSON           json.RawMessage   `json:"json,omitempty"`
	Body           string            `json:"body,omitempty"`
	Target         *bridgeTarget     `json:"target,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	TimeoutMS      int64             `json:"timeout_ms,omitempty"`
}

// bridgeResponse is the response envelope.
type bridgeResponse struct {
	V        int               `json:"v"`
	ID       string            `json:"id"`
	Status   int               `json:"status"`
	NodeID   string            `json:"node_id"`
	Headers  map[string]string `json:"headers,omitempty"`
	JSON     json.RawMessage   `json:"json,omitempty"`
	Body     string            `json:"body,omitempty"`
	Error    *bridgeError      `json:"error,omitempty"`
	Replayed bool              `json:"replayed,omitempty"`
	// Versions lists the supported versions when v was not one of them.
	Versions []int `json:"versions,omitempty"`
}

// errorResponse answers id with a bridge error.
func errorResponse(id, node string, e *bridgeError) bridgeResponse {
	return bridgeResponse{V: wireVersion, ID: id, Status: e.status(), NodeID: node, Error: e}
}

// streamResume asks to reattach to a running stream.
type streamResume struct {
	Stream   string `json:"stream"`
	AfterSeq int64  `json:"after_seq"`
}

// streamRequest opens an event stream (crush.client.v1.<dev>.sub).
type streamRequest struct {
	V      int             `json:"v,omitempty"`
	ID     string          `json:"id"`
	Node   string          `json:"node,omitempty"`
	Target *bridgeTarget   `json:"target,omitempty"`
	Path   string          `json:"path"`
	Query  json.RawMessage `json:"query,omitempty"`
	Credit int             `json:"credit,omitempty"`
	Resume *streamResume   `json:"resume,omitempty"`
}

// streamOpened answers a streamRequest.
type streamOpened struct {
	V        int          `json:"v"`
	ID       string       `json:"id"`
	Status   int          `json:"status"`
	NodeID   string       `json:"node_id"`
	Stream   string       `json:"stream,omitempty"`
	FirstSeq int64        `json:"first_seq"`
	Error    *bridgeError `json:"error,omitempty"`
}

// streamAck is flow control on crush.client.v1.<dev>.ack.<stream>.
type streamAck struct {
	V      int    `json:"v,omitempty"`
	Stream string `json:"stream,omitempty"`
	Seq    int64  `json:"seq"`
	Credit int    `json:"credit"`
}

// unsubRequest closes a stream (crush.client.v1.<dev>.unsub).
type unsubRequest struct {
	V      int    `json:"v,omitempty"`
	ID     string `json:"id,omitempty"`
	Stream string `json:"stream"`
}

// Stream frame types.
const (
	frameEvent     = "event"
	frameHeartbeat = "heartbeat"
	frameEnd       = "end"
)

// Stream end reasons.
const (
	endClosed        = "closed"
	endRevoked       = "revoked"
	endCreditTimeout = "credit_timeout"
	endIdle          = "idle"
	endNodeShutdown  = "node_shutdown"
	endError         = "error"
)

// streamFrame is one message on crush.client.v1.<dev>.ev.<stream>.
type streamFrame struct {
	V         int    `json:"v"`
	Stream    string `json:"stream"`
	Seq       int64  `json:"seq"`
	Type      string `json:"type"`
	At        string `json:"at"`
	SessionID string `json:"session_id,omitempty"`
	Data      string `json:"data,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Feature flags advertised by info.
const (
	featureJSONBody            = "json_body"
	featureSwarmTarget         = "swarm_target"
	featureStreamResume        = "stream_resume"
	featureStreamSessionFilter = "stream_session_filter"
	featureHeartbeat           = "heartbeat"
	featureDeviceAttention     = "device_attention"
	featureIdempotency         = "idempotency"
)

var bridgeFeatures = []string{
	featureJSONBody, featureSwarmTarget, featureStreamResume, featureStreamSessionFilter,
	featureHeartbeat, featureDeviceAttention, featureIdempotency,
}

// bridgeLimits are the per device, per node limits advertised by info.
type bridgeLimits struct {
	MaxRequestBytes     int   `json:"max_request_bytes"`
	MaxResponseBytes    int   `json:"max_response_bytes"`
	MaxInflightRequests int   `json:"max_inflight_requests"`
	MaxStreams          int   `json:"max_streams"`
	MaxCredit           int   `json:"max_credit"`
	RequestTimeoutMS    int64 `json:"request_timeout_ms"`
	HeartbeatMS         int64 `json:"heartbeat_ms"`
	ResumeWindowMS      int64 `json:"resume_window_ms"`
	ResumeBufferFrames  int   `json:"resume_buffer_frames"`
}

// infoReply answers crush.client.v1.<dev>.info.
type infoReply struct {
	V        int          `json:"v"`
	Versions []int        `json:"versions"`
	NodeID   string       `json:"node_id"`
	NodeName string       `json:"node_name"`
	DeviceID string       `json:"device_id"`
	Role     string       `json:"role"`
	Scopes   []string     `json:"scopes"`
	Features []string     `json:"features"`
	Limits   bridgeLimits `json:"limits"`
}

// Control notice types and revocation reasons.
const (
	controlRevoked       = "revoked"
	controlScopesChanged = "scopes_changed"
	controlReauth        = "reauth"

	revokeReasonOperator = "operator"
	reauthKeyRotation    = "key_rotation"
)

// controlNotice is published on crush.client.v1.<dev>.control.
type controlNotice struct {
	V        int      `json:"v"`
	Type     string   `json:"type"`
	DeviceID string   `json:"device_id"`
	Reason   string   `json:"reason,omitempty"`
	ByNode   string   `json:"by_node,omitempty"`
	Role     string   `json:"role,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
	At       string   `json:"at"`
}

// attentionNotice is published on crush.client.v1.<dev>.attention.
type attentionNotice struct {
	V          int             `json:"v"`
	Type       string          `json:"type"`
	At         string          `json:"at"`
	Event      json.RawMessage `json:"event"`
	Swarm      string          `json:"swarm,omitempty"`
	Actionable bool            `json:"actionable"`
}

// fwdKind selects what a forwarded device message asks the owner to do.
type fwdKind string

const (
	fwdAPI fwdKind = "api"
	fwdSub fwdKind = "sub"
)

// fwdEnvelope carries a device message from the entry node to the
// owning node on crush.client.fwd.<node> (mesh-only, global account).
// DeviceID is trusted only because device connections can never reach
// that account.
type fwdEnvelope struct {
	V         int             `json:"v"`
	Kind      fwdKind         `json:"kind"`
	DeviceID  string          `json:"device_id"`
	EntryNode string          `json:"entry_node"`
	Reply     string          `json:"reply,omitempty"`
	Request   json.RawMessage `json:"request"`
}
