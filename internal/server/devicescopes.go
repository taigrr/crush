package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/server/devicescope"
)

//go:generate go run ./gendevicescopes

// Routes with bridge-enforced body rules, and the event-stream routes
// (reachable only through .sub, never buffered through .api).
const (
	routePostAgent       = "POST /v1/workspaces/{id}/agent"
	routePermissionGrant = "POST /v1/workspaces/{id}/permissions/grant"
	routeWorkspaceEvents = "GET /v1/workspaces/{id}/events"
	routeGlobalEvents    = "GET /v1/events"
)

var streamRoutes = map[string]bool{routeWorkspaceEvents: true, routeGlobalEvents: true}

// deviceClientPrefix prefixes the client_id the bridge stamps on turns a
// device starts.
const deviceClientPrefix = "device:"

// maxDevicePath bounds a device-supplied path.
const maxDevicePath = 2048

// deviceRouter matches bridged requests against deviceRoutes (generated
// from the x-crush-scope annotations in swagger).
type deviceRouter struct{ mux *http.ServeMux }

func newDeviceRouter() *deviceRouter {
	mux := http.NewServeMux()
	for pattern := range deviceRoutes {
		mux.Handle(pattern, http.NotFoundHandler())
	}
	return &deviceRouter{mux: mux}
}

// cleanDevicePath validates a device-supplied path: absolute, under /v1,
// already canonical (no dot segments, doubled or encoded slashes), and
// free of query, fragment, escapes, backslashes, and control or space
// characters, so the path the router matches is exactly the path the
// handler sees.
func cleanDevicePath(p string) (string, bool) {
	if !strings.HasPrefix(p, "/v1/") || len(p) > maxDevicePath || strings.ContainsAny(p, "?#%\\") {
		return "", false
	}
	for i := range len(p) {
		if c := p[i]; c <= ' ' || c == 0x7f {
			return "", false
		}
	}
	if path.Clean(p) != p {
		return "", false
	}
	return p, true
}

// authorize returns the route pattern for method and path, or the bridge
// error that refuses it.
func (r *deviceRouter) authorize(method, p string, scopes []string) (string, *bridgeError) {
	if method != http.MethodGet && method != http.MethodPost {
		return "", newBridgeError(codeMethodNotAllowed, "only GET and POST are routable")
	}
	clean, ok := cleanDevicePath(p)
	if !ok {
		return "", newBridgeError(codeBadRequest, "path must be a canonical /v1 path")
	}
	req := &http.Request{Method: method, URL: &url.URL{Path: clean}, Host: "bridge"}
	_, pattern := r.mux.Handler(req)
	need, ok := deviceRoutes[pattern]
	if !ok {
		return "", newBridgeError(codeNotFound, "unknown route")
	}
	if need == devicescope.Never {
		return "", newBridgeError(codeRouteNotAllowed, "route is not available to devices")
	}
	if !slices.Contains(scopes, need) {
		return "", scopeDenied(need)
	}
	return pattern, nil
}

func scopeDenied(scope string) *bridgeError {
	return &bridgeError{Code: codeDeviceScopeDenied, Message: "route requires scope " + scope, Scope: scope}
}

// applyBodyRules enforces per-route body rules on top of the route
// scope and returns the body to forward: a device's turns always carry
// its own client identity, and it cannot loosen a turn's permission
// policy or make persistent grants without permissions:always.
func applyBodyRules(pattern string, d pairing.Device, body []byte) ([]byte, *bridgeError) {
	switch pattern {
	case routePostAgent:
		var msg map[string]json.RawMessage
		if json.Unmarshal(body, &msg) != nil || msg == nil {
			return nil, newBridgeError(codeBadRequest, "body must be a JSON object")
		}
		var policy string
		if raw, ok := msg["permission_policy"]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &policy) != nil {
				return nil, newBridgeError(codeBadRequest, "permission_policy must be a string")
			}
		}
		if policy != "" && policy != string(proto.PermissionPolicyPrompt) && !slices.Contains(d.Scopes, devicescope.PermissionsAlways) {
			return nil, scopeDenied(devicescope.PermissionsAlways)
		}
		id, _ := json.Marshal(deviceClientPrefix + d.DeviceID)
		msg["client_id"] = id
		out, err := json.Marshal(msg)
		if err != nil {
			return nil, newBridgeError(codeBadRequest, "invalid body")
		}
		return out, nil
	case routePermissionGrant:
		var grant struct {
			Action string `json:"action"`
		}
		if json.Unmarshal(body, &grant) != nil {
			return nil, newBridgeError(codeBadRequest, "body must be a JSON object")
		}
		switch proto.PermissionAction(grant.Action) {
		case proto.PermissionAllow, proto.PermissionAllowForSession, proto.PermissionDeny:
		default:
			// Anything beyond a one-off or per-session answer persists.
			if !slices.Contains(d.Scopes, devicescope.PermissionsAlways) {
				return nil, scopeDenied(devicescope.PermissionsAlways)
			}
		}
	}
	return body, nil
}
