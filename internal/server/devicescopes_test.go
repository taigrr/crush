package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/server/devicescope"
	"github.com/taigrr/crush/internal/swagger"
)

// TestSwaggerRoutesHaveDeviceScope fails when a documented route lacks an
// x-crush-scope annotation, or when devicescopes_gen.go is stale
// (run `task swag` then `go generate ./internal/server`).
func TestSwaggerRoutesHaveDeviceScope(t *testing.T) {
	t.Parallel()
	routes, err := devicescope.FromSwagger([]byte(swagger.SwaggerInfo.ReadDoc()))
	require.NoError(t, err)
	require.Equal(t, routes, deviceRoutes, "devicescopes_gen.go is stale; run go generate ./internal/server")
}

// TestDeviceRoutesExistOnServer checks every device-reachable route is a
// real /v1 handler, so the allow-list cannot drift from the server.
func TestDeviceRoutesExistOnServer(t *testing.T) {
	t.Parallel()
	srv, _ := newClusterTestServer(t)
	mux := srv.mux
	r := newDeviceRouter()
	for pattern, scope := range deviceRoutes {
		if scope == devicescope.Never {
			continue
		}
		method, p, _ := cutPattern(pattern)
		req, err := http.NewRequest(method, concrete(p), nil)
		require.NoError(t, err)
		_, got := mux.Handler(req)
		require.Equal(t, pattern, got, "route %s is not served", pattern)
		_, berr := r.authorize(method, concrete(p), []string{scope})
		require.Nil(t, berr, pattern)
	}
}

func cutPattern(p string) (string, string, bool) {
	for i := range len(p) {
		if p[i] == ' ' {
			return p[:i], p[i+1:], true
		}
	}
	return "", p, false
}

// concrete replaces {name} wildcards with sample values.
func concrete(p string) string {
	out := []byte{}
	for i := 0; i < len(p); i++ {
		if p[i] == '{' {
			for i < len(p) && p[i] != '}' {
				i++
			}
			out = append(out, 'x')
			continue
		}
		out = append(out, p[i])
	}
	return string(out)
}

func TestDeviceRouterAllowList(t *testing.T) {
	t.Parallel()
	r := newDeviceRouter()
	viewer := pairing.RoleViewer.Scopes()
	operator := pairing.RoleOperator.Scopes()
	admin := pairing.RoleAdmin.Scopes()
	code := func(method, p string, scopes []string) bridgeCode {
		_, e := r.authorize(method, p, scopes)
		if e == nil {
			return ""
		}
		return e.Code
	}
	require.Empty(t, code("GET", "/v1/workspace-overviews", viewer))
	require.Empty(t, code("GET", "/v1/workspaces/w/sessions/s/messages", viewer))
	require.Equal(t, codeDeviceScopeDenied, code("POST", "/v1/workspaces/w/agent", viewer), "viewers cannot prompt")
	require.Empty(t, code("POST", "/v1/workspaces/w/agent", operator))
	require.Empty(t, code("POST", "/v1/workspaces/w/permissions/grant", operator))
	require.Equal(t, codeDeviceScopeDenied, code("POST", "/v1/workspaces/w/permissions/skip", operator), "yolo needs permissions:always")
	require.Equal(t, codeDeviceScopeDenied, code("POST", "/v1/workspaces/w/agent/sessions/s/shell", admin), "shell is opt-in")
	_, e := r.authorize("POST", "/v1/workspaces/w/questions/answer", viewer)
	require.Equal(t, devicescope.PermissionsAnswer, e.Scope)

	// "never" routes are refused whatever the scopes.
	everything := devicescope.Known
	for _, p := range []string{
		"/v1/workspaces/w/config/provider-key", "/v1/workspaces/w/config/set", "/v1/workspaces/w/config/refresh-oauth",
		"/v1/cluster/devices/d/revoke", "/v1/workspaces/w/permissions/sysadmin",
	} {
		require.Equal(t, codeRouteNotAllowed, code("POST", p, everything), p)
	}
	require.Equal(t, codeRouteNotAllowed, code("GET", "/v1/workspaces/w/config", everything))
	// Cluster keys, debug, replication, and transfer are documented as
	// "never".
	for _, route := range []string{
		"GET /v1/cluster/token", "POST /v1/cluster/rotate", "GET /v1/debug/logs", "POST /v1/transfer/import",
		"GET /v1/replication/changes", "POST /v1/migrate/execute", "POST /v1/cluster/join", "GET /v1/cluster",
	} {
		m, p, _ := cutPattern(route)
		require.Equal(t, codeRouteNotAllowed, code(m, p, everything), route)
	}
	// The member proxy is unknown to the device router.
	require.Equal(t, codeNotFound, code("GET", "/v1/nodes/x/v1/workspaces", everything))
	require.Equal(t, codeNotFound, code("POST", "/v1/nodes/x/v1/workspaces", everything))
	// History search is a read.
	require.Empty(t, code("POST", "/v1/workspaces/w/history/search", viewer))
	_, e = r.authorize("POST", "/v1/workspaces/w/history/search", nil)
	require.Equal(t, devicescope.SessionsRead, e.Scope)
	require.Equal(t, codeDeviceScopeDenied, e.Code)
	// Path tricks never reach a different route.
	for _, p := range []string{
		"/v1/workspaces/w/../../cluster/token", "/v1/workspaces//w", "/v1/workspaces/w%2Fx", "/v1/workspaces/w/",
		"/v1/workspaces/w/./sessions", "/v1/workspaces/w?x=1", "/v1/workspaces/w#f", `/v1/workspaces\w`,
		"/v1/workspaces/w x", "/v1/workspaces/w\x00", "v1/workspaces", "/v2/workspaces", "/v1",
		"/V1/workspaces",
	} {
		require.Contains(t, []bridgeCode{codeBadRequest, codeNotFound}, code("GET", p, admin), p)
	}
	// Only GET and POST are routable: no HEAD, method override, or lower
	// case smuggling.
	for _, m := range []string{"HEAD", "PUT", "DELETE", "PATCH", "OPTIONS", "get", "CONNECT", ""} {
		require.Equal(t, codeMethodNotAllowed, code(m, "/v1/workspaces", admin), m)
	}
	require.Equal(t, codeNotFound, code("POST", "/v1/workspaces/w/sessions/s/messages", admin), "a GET route is not POSTable")
}

func TestApplyBodyRules(t *testing.T) {
	t.Parallel()
	op := pairing.Device{DeviceID: "dev1", Scopes: pairing.RoleOperator.Scopes()}
	admin := pairing.Device{DeviceID: "dev2", Scopes: pairing.RoleAdmin.Scopes()}

	out, e := applyBodyRules(routePostAgent, op, []byte(`{"session_id":"s","prompt":"hi","client_id":"victim"}`))
	require.Nil(t, e)
	var msg map[string]any
	require.NoError(t, json.Unmarshal(out, &msg))
	require.Equal(t, "device:dev1", msg["client_id"])
	require.Equal(t, "hi", msg["prompt"])

	for _, ok := range []string{`{"permission_policy":""}`, `{"permission_policy":"prompt"}`, `{"permission_policy":null}`} {
		_, e = applyBodyRules(routePostAgent, op, []byte(ok))
		require.Nil(t, e, ok)
	}
	for _, loose := range []string{`{"permission_policy":"allow"}`, `{"permission_policy":"deny"}`} {
		_, e = applyBodyRules(routePostAgent, op, []byte(loose))
		require.Equal(t, codeDeviceScopeDenied, e.Code, loose)
		require.Equal(t, devicescope.PermissionsAlways, e.Scope)
		_, e = applyBodyRules(routePostAgent, admin, []byte(loose))
		require.Nil(t, e, loose)
	}
	// Field names match case-insensitively, as the handler decodes them.
	for _, sneaky := range []string{`{"Permission_Policy":"allow"}`, `{"PERMISSION_POLICY":"allow"}`, `{"permission_policy":"prompt","Permission_policy":"allow"}`} {
		_, e = applyBodyRules(routePostAgent, op, []byte(sneaky))
		require.NotNil(t, e, sneaky)
		require.Equal(t, codeDeviceScopeDenied, e.Code, sneaky)
	}
	out, e = applyBodyRules(routePostAgent, op, []byte(`{"Client_ID":"victim","CLIENT_ID":"victim2","swarm_parts":[{"text":"spoof"}],"run_id":"r1"}`))
	require.Nil(t, e)
	var decoded proto.AgentMessage
	require.NoError(t, json.Unmarshal(out, &decoded))
	require.Equal(t, "device:dev1", decoded.ClientID)
	require.Empty(t, decoded.SwarmParts, "devices cannot impersonate a swarm sender")
	require.Equal(t, "r1", decoded.RunID)
	_, e = applyBodyRules(routePermissionGrant, op, []byte(`{"ACTION":"allow_always"}`))
	require.Equal(t, codeDeviceScopeDenied, e.Code)

	for _, bad := range []string{``, `[]`, `"x"`, `{"permission_policy":1}`, `null`} {
		_, e = applyBodyRules(routePostAgent, op, []byte(bad))
		require.Equal(t, codeBadRequest, e.Code, bad)
	}

	for _, ok := range []string{"allow", "allow_session", "deny"} {
		_, e = applyBodyRules(routePermissionGrant, op, []byte(`{"action":"`+ok+`"}`))
		require.Nil(t, e, ok)
	}
	_, e = applyBodyRules(routePermissionGrant, op, []byte(`{"action":"allow_always"}`))
	require.Equal(t, codeDeviceScopeDenied, e.Code)
	_, e = applyBodyRules(routePermissionGrant, op, []byte(`nope`))
	require.Equal(t, codeBadRequest, e.Code)

	// Other routes pass bodies through untouched.
	out, e = applyBodyRules("POST /v1/workspaces/{id}/sessions", op, []byte(`{"title":"t"}`))
	require.Nil(t, e)
	require.JSONEq(t, `{"title":"t"}`, string(out))
}

// prefixRoutes are method-less catch-all patterns that OpenAPI cannot
// describe. The device router must never match them.
var prefixRoutes = map[string]bool{
	"/v1/nodes/{node}/{rest...}": true,
	"/v1/docs/":                  true,
}

// TestServerRoutesHaveDeviceScope fails when any route registered on the
// server mux is missing from swagger and therefore from the device scope
// table, so a new handler cannot ship without a scope decision.
func TestServerRoutesHaveDeviceScope(t *testing.T) {
	t.Parallel()
	srv, _ := newClusterTestServer(t)
	require.NotEmpty(t, srv.routes)
	r := newDeviceRouter()
	var missing []string
	for _, pattern := range srv.routes {
		_, p, ok := cutPattern(pattern)
		if !ok {
			require.True(t, prefixRoutes[pattern], "method-less route %s must be listed in prefixRoutes", pattern)
			for _, m := range []string{http.MethodGet, http.MethodPost} {
				for _, sample := range []string{concrete(p), concrete(p) + "x", concrete(p) + "/x"} {
					_, berr := r.authorize(m, sample, devicescope.Known)
					require.NotNil(t, berr, "%s %s", m, sample)
					require.Contains(t, []bridgeCode{codeNotFound, codeBadRequest}, berr.Code, "%s %s", m, sample)
				}
			}
			continue
		}
		if _, ok := deviceRoutes[pattern]; !ok {
			missing = append(missing, pattern)
		}
	}
	require.Empty(t, missing, "routes without swagger docs and x-crush-scope")
	for pattern := range prefixRoutes {
		require.Contains(t, srv.routes, pattern, "stale prefixRoutes entry")
	}
}
