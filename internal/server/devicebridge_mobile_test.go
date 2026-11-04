package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/oauth"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/voice"
)

// TestDeviceBridgeMobileRoutes drives the routes rush needs (providers,
// LSPs, MCP states, per-session model, voice secrets) through a paired
// device.
func TestDeviceBridgeMobileRoutes(t *testing.T) {
	isolateHome(t)
	t.Setenv("XAI_API_KEY", "")
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	dev := newFakeDevice(t, ph.id, func(o ...nats.Option) (*nats.Conn, error) { return ph.connect(t, o...) })
	ws, bws, alpha, _ := newBridgeWorkspace(t, srv, c)
	base := "/v1/workspaces/" + ws.ID
	cfg := bws.Cfg.Config()

	const (
		fakeKey     = "sk-fake-SECRET-key"
		fakeAccess  = "fake-SECRET-access"
		fakeRefresh = "fake-SECRET-refresh"
		fakeHeader  = "fake-SECRET-header"
	)
	cfg.Providers.Set("fakeprov", config.ProviderConfig{
		ID: "fakeprov", Name: "Fake", Type: catwalk.TypeOpenAI, APIKey: fakeKey,
		ExtraHeaders: map[string]string{"X-Api-Token": fakeHeader},
		Models:       []catwalk.Model{{ID: "m1", Name: "Model One"}, {ID: "m2", Name: "Model Two"}},
	})
	cfg.Providers.Set("fakeoauth", config.ProviderConfig{
		ID: "fakeoauth", Name: "Fake OAuth", APIKey: fakeAccess,
		OAuthToken: &oauth.Token{AccessToken: fakeAccess, RefreshToken: fakeRefresh},
		Models:     []catwalk.Model{{ID: "o1"}},
	})
	cfg.Models[config.SelectedModelTypeLarge] = config.SelectedModel{Provider: "fakeprov", Model: "m1"}
	cfg.Models[config.SelectedModelTypeSmall] = config.SelectedModel{Provider: "fakeprov", Model: "m2"}
	// The LSP routes are exercised for routing and scopes only. Auto-start
	// would launch whatever language servers the host has installed: on
	// Windows CI that alone took 5-8 s of the 10 s device wait on feature
	// and timed out under load.
	autoLSP := false
	cfg.Options.AutoLSP = &autoLSP

	ok := func(t *testing.T, resp bridgeResponse) {
		t.Helper()
		require.Equal(t, http.StatusOK, resp.Status, "%+v %s", resp.Error, resp.JSON)
	}

	t.Run("providers are redacted", func(t *testing.T) {
		resp := dev.get(base + "/providers")
		ok(t, resp)
		require.NotContains(t, string(resp.JSON), "SECRET")
		require.NotContains(t, string(resp.JSON), "api_key")
		require.NotContains(t, string(resp.JSON), "oauth\":{")
		var infos []proto.ProviderInfo
		require.NoError(t, json.Unmarshal(resp.JSON, &infos))
		byID := map[string]proto.ProviderInfo{}
		for _, p := range infos {
			byID[p.ID] = p
		}
		require.True(t, byID["fakeprov"].Configured)
		require.True(t, byID["fakeprov"].HasCredential)
		require.False(t, byID["fakeprov"].OAuth)
		require.Len(t, byID["fakeprov"].Models, 2)
		require.True(t, byID["fakeoauth"].OAuth)
		require.True(t, byID["fakeoauth"].HasCredential)
	})

	t.Run("lsps and mcp", func(t *testing.T) {
		ok(t, dev.get(base+"/lsps"))
		ok(t, dev.get(base+"/mcp/states"))
		ok(t, dev.post(base+"/lsps/stop", map[string]any{}))
		ok(t, dev.post(base+"/lsps/start", proto.LSPStartRequest{Path: ws.Path}))
		resp := dev.get(base + "/lsps/nope/diagnostics")
		if resp.Error != nil {
			require.NotEqual(t, codeRouteNotAllowed, resp.Error.Code)
			require.NotEqual(t, codeDeviceScopeDenied, resp.Error.Code)
			require.NotEqual(t, "unknown route", resp.Error.Message)
		}
	})

	t.Run("session model", func(t *testing.T) {
		path := base + "/sessions/" + alpha.ID + "/model"
		get := func(t *testing.T) proto.SessionModel {
			t.Helper()
			resp := dev.get(path)
			ok(t, resp)
			var m proto.SessionModel
			require.NoError(t, json.Unmarshal(resp.JSON, &m))
			return m
		}
		m := get(t)
		require.True(t, m.Default)
		require.Equal(t, "m1", m.Large.Model)
		require.Equal(t, "m2", m.Small.Model)

		resp := dev.post(path, proto.SessionModelRequest{Model: &config.SelectedModel{Provider: "fakeprov", Model: "m2"}})
		ok(t, resp)
		m = get(t)
		require.False(t, m.Default)
		require.Equal(t, "fakeprov/m2", m.ModelRef)
		require.Equal(t, "m2", m.Large.Model)
		require.Equal(t, "m1", cfg.Models[config.SelectedModelTypeLarge].Model, "workspace default is unchanged")
		sess, err := bws.Sessions.Get(t.Context(), alpha.ID)
		require.NoError(t, err)
		require.Equal(t, "fakeprov/m2", sess.ModelRef)

		require.Equal(t, http.StatusBadRequest, dev.post(path, proto.SessionModelRequest{ModelRef: "nope/none"}).Status)
		require.Equal(t, http.StatusBadRequest, dev.post(path, proto.SessionModelRequest{ModelType: config.SelectedModelTypeSmall, ModelRef: "fakeprov/m1"}).Status)
		require.Equal(t, http.StatusNotFound, dev.get(base+"/sessions/no-such-session/model").Status)

		ok(t, dev.post(path, proto.SessionModelRequest{}))
		require.True(t, get(t).Default)
	})

	t.Run("voice client secret", func(t *testing.T) {
		var hits atomic.Int32
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.URL.Path != "/v1/realtime/client_secrets" || r.Header.Get("Authorization") != "Bearer fake-xai-key" {
				http.Error(w, "nope", http.StatusUnauthorized)
				return
			}
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				ExpiresAfter struct {
					Seconds int `json:"seconds"`
				} `json:"expires_after"`
			}
			if json.Unmarshal(raw, &body) != nil || body.ExpiresAfter.Seconds <= 0 {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"value":"eph-fake-secret","expires_at":1893456000}`)
		}))
		t.Cleanup(fake.Close)
		srv.backend.SetVoiceMinter(&voice.Minter{BaseURL: fake.URL + "/v1", Client: fake.Client()})
		var xaiKey atomic.Value
		xaiKey.Store("")
		srv.backend.SetVoiceEnv(func(k string) string {
			if k == "XAI_API_KEY" {
				return xaiKey.Load().(string)
			}
			return ""
		})

		resp := dev.post("/v1/voice/client-secret", proto.VoiceClientSecretRequest{SessionID: alpha.ID})
		require.Equal(t, http.StatusFailedDependency, resp.Status, "%s", resp.JSON)
		require.Zero(t, hits.Load(), "no request without a credential")

		require.Equal(t, http.StatusBadRequest, dev.post("/v1/voice/client-secret", proto.VoiceClientSecretRequest{}).Status)

		xaiKey.Store("fake-xai-key")
		resp = dev.post("/v1/voice/client-secret", proto.VoiceClientSecretRequest{SessionID: alpha.ID})
		ok(t, resp)
		var sec proto.VoiceClientSecret
		require.NoError(t, json.Unmarshal(resp.JSON, &sec))
		require.Equal(t, "eph-fake-secret", sec.Value)
		require.EqualValues(t, 1893456000, sec.ExpiresAt)
		require.False(t, strings.Contains(string(resp.JSON), "fake-xai-key"))

		xaiKey.Store("wrong-key")
		require.Equal(t, http.StatusBadGateway, dev.post("/v1/voice/client-secret", proto.VoiceClientSecretRequest{SessionID: alpha.ID}).Status)
	})
}
