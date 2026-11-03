package server

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Device scopes (docs/specs/MOBILE_PAIRING.md, Permissions).
const (
	scopeSessionsRead      = "sessions:read"
	scopeSessionsPrompt    = "sessions:prompt"
	scopePermissionsAnswer = "permissions:answer"
	scopePermissionsAlways = "permissions:always"
	scopeConfigModel       = "config:model"
	scopeSessionsMove      = "sessions:move"
	scopeShell             = "shell"
)

// deviceRoutes is the allow-list of /v1 routes a device may call through
// the bridge, each with the scope it needs. Anything not listed is denied:
// provider keys and OAuth, config writes, cluster and key operations,
// debug, replication, transfer, and the member API are never reachable.
var deviceRoutes = map[string]string{
	"GET /v1/workspaces":                                          scopeSessionsRead,
	"GET /v1/workspace-overviews":                                 scopeSessionsRead,
	"GET /v1/workspaces/{id}":                                     scopeSessionsRead,
	"GET /v1/workspaces/{id}/events":                              scopeSessionsRead,
	"GET /v1/events":                                              scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions":                            scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions/archived":                   scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions/{sid}":                      scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions/{sid}/history":              scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions/{sid}/pending":              scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions/{sid}/messages":             scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions/{sid}/messages/user":        scopeSessionsRead,
	"GET /v1/workspaces/{id}/sessions/{sid}/milestones":           scopeSessionsRead,
	"GET /v1/workspaces/{id}/agent":                               scopeSessionsRead,
	"GET /v1/workspaces/{id}/agent/sessions/{sid}":                scopeSessionsRead,
	"GET /v1/workspaces/{id}/agent/sessions/{sid}/goal":           scopeSessionsRead,
	"GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/list":   scopeSessionsRead,
	"GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/queued": scopeSessionsRead,
	"GET /v1/cluster/dashboard":                                   scopeSessionsRead,
	"GET /v1/swarm/resolve":                                       scopeSessionsRead,

	"POST /v1/workspaces/{id}/agent":                                    scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/sessions":                                 scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/cancel":              scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/tools/cancel":        scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/goal":                scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/goal/clear":          scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/title":               scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/summarize":           scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/prompts/clear":       scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/prompts/promote-btw": scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/runs/{rid}/drop":     scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/sessions/{sid}/seen":                      scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/sessions/{sid}/favorite":                  scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/sessions/{sid}/archive":                   scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/sessions/{sid}/unarchive":                 scopeSessionsPrompt,
	"POST /v1/workspaces/{id}/permissions/grant":                        scopePermissionsAnswer,
	"POST /v1/workspaces/{id}/questions/answer":                         scopePermissionsAnswer,
	"POST /v1/workspaces/{id}/permissions/skip":                         scopePermissionsAlways,
	"POST /v1/workspaces/{id}/config/model":                             scopeConfigModel,
	"POST /v1/sessions/move":                                            scopeSessionsMove,
	"POST /v1/workspaces/{id}/agent/sessions/{sid}/shell":               scopeShell,
}

// streamRoutes are the routes a device may open as an event stream.
var streamRoutes = map[string]bool{
	"GET /v1/workspaces/{id}/events": true,
	"GET /v1/events":                 true,
}

// deviceRouter matches bridged requests against deviceRoutes.
type deviceRouter struct{ mux *http.ServeMux }

func newDeviceRouter() *deviceRouter {
	mux := http.NewServeMux()
	for pattern := range deviceRoutes {
		mux.Handle(pattern, http.NotFoundHandler())
	}
	return &deviceRouter{mux: mux}
}

// cleanDevicePath validates a device-supplied path: absolute, under /v1,
// already canonical (no dot segments, doubled or encoded slashes).
func cleanDevicePath(p string) (string, bool) {
	if !strings.HasPrefix(p, "/v1/") || strings.ContainsAny(p, "?#%\\") {
		return "", false
	}
	if path.Clean(p) != p {
		return "", false
	}
	return p, true
}

// authorize returns the route pattern for method and path if scopes allow
// it, or "" when it is denied or unknown.
func (r *deviceRouter) authorize(method, p string, scopes []string) string {
	clean, ok := cleanDevicePath(p)
	if !ok {
		return ""
	}
	req := &http.Request{Method: method, URL: &url.URL{Path: clean}, Host: "bridge"}
	_, pattern := r.mux.Handler(req)
	need, ok := deviceRoutes[pattern]
	if !ok {
		return ""
	}
	for _, s := range scopes {
		if s == need {
			return pattern
		}
	}
	return ""
}
